// Package live is an intentionally amnesiac collaboration rendezvous for an
// application hosted by Gitseq.
// Its cursor namespace dies with the process; clients must retain any frames
// they care about.
package live

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/fxamacker/cbor/v2"
	gitseqhost "github.com/generalbusiness-ai/gitseq/host"
)

const (
	actorFrameDomain   = "gitseq.nexus.actor-frame.v0\x00"
	nexusFrameDomain   = "gitseq.nexus.order-frame.v0\x00"
	sessionProofDomain = "gitseq.live.session-proof.v0\x00"
	credentialPrefix   = "credential:"
	credentialBytes    = 32
)

var ErrReset = errors.New("nexus cursor is no longer available; take a new snapshot")

// ErrStaleDraft reports a frame prepared against live state that moved before
// submission. The caller should prepare and sign a new draft.
var ErrStaleDraft = errors.New("live frame draft is stale; prepare it again")

type ActivityStatus string

const (
	ActivityAvailable ActivityStatus = "available"
	ActivityBusy      ActivityStatus = "busy"
	ActivityWaiting   ActivityStatus = "waiting"
	ActivityBlocked   ActivityStatus = "blocked"

	MaxFocusEvents              = 8
	MaxFocusEventBytes          = 256
	MaxActivityNoteBytes        = 160
	MaxInboxFrames              = 20
	MaxPendingInboxFrames       = 256
	MaxMessageTextBytes         = 16 << 10
	MaxMessageIDBytes           = 256
	MaxMessageRecipients        = 32
	MaxFramePayloadBytes        = 20 << 10
	MaxConversationGenesisBytes = 512
	MaxLiveSessions             = 256
	MaxSessionsPerActor         = 16
	MaxRetainedFrames           = 4096
	MaxRetainedBytes            = 8 << 20
	MaxActorNameBytes           = 256
	MaxPresenceValueBytes       = 512
	MaxPendingSessionChallenges = 256

	DefaultSessionTTL     = 30 * time.Second
	MaxSessionTTL         = 2 * time.Minute
	SessionChallengeTTL   = 30 * time.Second
	MaxCompositeWait      = 30 * time.Second
	DefaultCompositeWait  = 25 * time.Second
	compositePollInterval = 250 * time.Millisecond
)

// Activity is advisory, leased attention. It has no durable workflow force.
// Focus is a sorted set so snapshots and multi-session clients converge on one
// representation regardless of update order.
type Activity struct {
	Status ActivityStatus `json:"status"`
	Focus  []string       `json:"focus"`
	Note   string         `json:"note,omitempty"`
}

// ActivityUpdate distinguishes an omitted field from an explicit clear.
// Announce renewals omit all three fields and therefore preserve the session's
// prior activity until that same session changes it or its lease expires.
type ActivityUpdate struct {
	Status *ActivityStatus
	Focus  *[]string
	Note   *string
}

// mintHandle produces a public name for a session that is unrelated to the
// session itself.
//
// A session identifier authorizes speech and durable acts: whoever presents
// one has the service sign with that session's actor key. It is a bearer
// credential, so observers must never be handed one — but they still need to
// tell sessions apart, to follow a renewal or notice a departure, so presence
// is published under a handle.
//
// The handle is drawn from the system's randomness rather than derived from
// the identifier. Deriving it would publish an oracle: an observer could
// guess a candidate identifier, hash it, and compare against the published
// handle to confirm the guess, then present the confirmed identifier and act.
// That is only as strong as the weakest identifier any client ever chooses,
// and the service does not constrain that choice. An unrelated handle carries
// no such relation to test, so a guess can be confirmed only by using it,
// which is the check that already exists.
func mintHandle() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "session:" + hex.EncodeToString(raw), nil
}

// mintCredential creates the private authority for one resident lease. The
// resident, never its caller, chooses this value. Its fixed shape lets every
// credential-bearing endpoint reject malformed input before looking up a
// lease, while 256 bits of system randomness make online guessing irrelevant
// within the deliberately bounded lifetime of a resident process.
func mintCredential() (string, error) {
	raw := make([]byte, credentialBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return credentialPrefix + hex.EncodeToString(raw), nil
}

func validCredential(value string) bool {
	if len(value) != len(credentialPrefix)+(credentialBytes*2) || !strings.HasPrefix(value, credentialPrefix) {
		return false
	}
	_, err := hex.DecodeString(value[len(credentialPrefix):])
	return err == nil
}

type Cursor struct {
	Generation string `json:"generation"`
	Position   uint64 `json:"position"`
}

type Change struct {
	Cursor   Cursor      `json:"cursor"`
	Kind     string      `json:"kind"`
	ID       string      `json:"id"`
	Value    string      `json:"value,omitempty"`
	Activity *Activity   `json:"activity,omitempty"`
	Frame    *InboxFrame `json:"frame,omitempty"`
}

type Snapshot struct {
	Cursor        Cursor              `json:"cursor"`
	Presence      map[string]string   `json:"presence"`
	Activity      map[string]Activity `json:"activity"`
	Conversations []string            `json:"conversations"`
}

type Frame struct {
	_                   struct{} `cbor:",toarray"`
	Version             uint64
	Generation          string
	ConversationGenesis []byte
	Conversation        string
	Sequence            uint64
	PreviousHash        []byte
	Payload             []byte
	ActorKey            []byte
	ActorSignature      []byte
	NexusKey            []byte
	NexusSignature      []byte
}

// Draft is the exact application-neutral frame body an actor signs. Scope and
// ConversationGenesis are carried alongside the signed body; Conversation is
// the digest of that genesis, which commits the signature to the exact scope.
// A draft is optimistic and retained nowhere: concurrent publication can make
// it stale before submission.
type Draft struct {
	Scope               string `json:"scope"`
	Version             uint64 `json:"version"`
	Generation          string `json:"generation"`
	ConversationGenesis []byte `json:"conversation_genesis"`
	Conversation        string `json:"conversation"`
	Sequence            uint64 `json:"sequence"`
	PreviousHash        []byte `json:"previous_hash"`
	Payload             []byte `json:"payload"`
}

// Submission carries a draft signed outside the runtime. In particular, a
// browser can retain its private key and send only this public material.
type Submission struct {
	Draft          Draft             `json:"draft"`
	ActorKey       ed25519.PublicKey `json:"actor_key"`
	ActorSignature []byte            `json:"actor_signature"`
}

// SessionChallenge is an expiring, single-use proof-of-possession challenge.
// Preparing one publishes no presence and consumes no actor session quota.
// The private key corresponding to ActorKey signs SessionSigningBytes before
// OpenSession makes the lease visible.
type SessionChallenge struct {
	Version    uint64            `json:"version"`
	Generation string            `json:"generation"`
	Nonce      []byte            `json:"nonce"`
	ActorKey   ed25519.PublicKey `json:"actor_key"`
}

// DurableFrontier is the application-neutral identity of one verified
// durable read. The live runtime neither obtains nor interprets it.
type DurableFrontier struct {
	Genesis string `json:"genesis"`
	Head    string `json:"head"`
	Depth   int    `json:"depth"`
}

// CompositeCursor keeps durable and live progress distinct. Neither cursor
// is embedded in or inferred from the other.
type CompositeCursor struct {
	Durable DurableFrontier `json:"durable"`
	Live    Cursor          `json:"live"`
}

// CompositeObservation joins an application-owned durable value to one live
// observation without assigning meaning to either one.
type CompositeObservation[T any] struct {
	Durable T               `json:"durable"`
	Live    Observation     `json:"live"`
	Cursor  CompositeCursor `json:"cursor"`
}

type sessionChallengeBody struct {
	_          struct{} `cbor:",toarray"`
	Version    uint64
	Generation string
	Nonce      []byte
	ActorKey   []byte
}

// Message is the signed, application-neutral chat payload. Recipients are
// durable actor fingerprints resolved by the service before publication. A
// reply's exact parent author is added by the nexus under the same lock that
// sequences the new frame.
type Message struct {
	About      string   `json:"about"`
	Text       string   `json:"text"`
	Re         string   `json:"re,omitempty"`
	Recipients []string `json:"recipients,omitempty"`
}

// InboxFrame is the bounded decoded view delivered to an addressed session.
// Actor and Recipients are exact fingerprints; Thread is the exact reply
// handle "<conversation>:<sequence>".
type InboxFrame struct {
	Actor        string   `json:"actor"`
	Text         string   `json:"text"`
	About        string   `json:"about"`
	Conversation string   `json:"conversation"`
	Sequence     uint64   `json:"sequence"`
	Re           string   `json:"re,omitempty"`
	Recipients   []string `json:"recipients"`
	Thread       string   `json:"thread"`
}

type Inbox struct {
	Frames  []InboxFrame `json:"frames"`
	Skipped int          `json:"skipped,omitempty"`
}

// Observation is one live cursor barrier. Session-specific callers receive
// the current unacknowledged inbox and addressed inline changes without a race
// between separately captured snapshots and deltas.
type Observation struct {
	Snapshot Snapshot
	Inbox    Inbox
	Changes  []Change
	Reset    bool
	// Actor and Fingerprint are captured under the same lock as Inbox. They
	// never cross the nexus wire boundary; the resident uses them to build a
	// bounded actor view without accepting an identity alongside the bearer
	// session or racing a session expiry and rebind.
	Actor       string `json:"-"`
	Fingerprint string `json:"-"`
}

type frameActorBody struct {
	_            struct{} `cbor:",toarray"`
	Version      uint64
	Generation   string
	Conversation string
	Sequence     uint64
	PreviousHash []byte
	Payload      []byte
}

type frameNexusBody struct {
	_              struct{} `cbor:",toarray"`
	ActorBody      frameActorBody
	ActorKey       []byte
	ActorSignature []byte
}

type conversationGenesis struct {
	_          struct{} `cbor:",toarray"`
	Version    uint64
	Generation string
	NexusKey   []byte
	Scope      string
	Nonce      []byte
}

type conversation struct {
	next    uint64
	last    []byte
	genesis []byte
	frames  []Frame
	about   string
}

type inboxState struct {
	refs []frameRef
}

type frameRef struct {
	conversation string
	sequence     uint64
}

type presenceEntry struct {
	value       string
	actor       string
	fingerprint string
	inbox       bool
	handle      string
	activity    Activity
	expiresAt   time.Time
	// activityChangedAt is when this session's activity last actually moved,
	// observed by the resident rather than claimed by the client. A heartbeat
	// renewal carries the lease forward and leaves this alone, so a reader can
	// tell "still working on it, said so an hour ago" from "just picked it up".
	// Renewals are frequent and changes are not; a timestamp that moved on
	// every renewal would report the polling interval instead of the activity.
	activityChangedAt time.Time
}

type pendingSession struct {
	actor       string
	fingerprint string
	value       string
	ttl         time.Duration
	activity    Activity
	expiresAt   time.Time
}

type Hub struct {
	mu              sync.Mutex
	generation      string
	position        uint64
	base            uint64
	historyCap      int
	history         []Change
	presence        map[string]presenceEntry
	convs           map[string]*conversation
	about           map[string]string
	participants    map[string]map[string]bool
	inboxes         map[string]*inboxState
	pendingSessions map[string]pendingSession
	publicKey       ed25519.PublicKey
	privateKey      ed25519.PrivateKey
	retainedFrames  int
	retainedBytes   int
	now             func() time.Time
}

func New(historyCap int) (*Hub, error) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return NewWithSigningKey(historyCap, privateKey)
}

// NewWithSigningKey models the profile's durable issuer-key anchor while all
// conversations, presence and cursors remain process-local.
func NewWithSigningKey(historyCap int, privateKey ed25519.PrivateKey) (*Hub, error) {
	if historyCap < 1 {
		return nil, errors.New("history capacity must be positive")
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("invalid nexus private key")
	}
	generationBytes := make([]byte, 16)
	if _, err := rand.Read(generationBytes); err != nil {
		return nil, err
	}
	publicKey := privateKey.Public().(ed25519.PublicKey)
	return &Hub{
		generation:      "generation:" + hex.EncodeToString(generationBytes),
		historyCap:      historyCap,
		presence:        make(map[string]presenceEntry),
		convs:           make(map[string]*conversation),
		about:           make(map[string]string),
		participants:    make(map[string]map[string]bool),
		inboxes:         make(map[string]*inboxState),
		pendingSessions: make(map[string]pendingSession),
		publicKey:       bytes.Clone(publicKey),
		privateKey:      bytes.Clone(privateKey),
		now:             time.Now,
	}, nil
}

func (h *Hub) Generation() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.generation
}

func (h *Hub) PublicKey() ed25519.PublicKey {
	h.mu.Lock()
	defer h.mu.Unlock()
	return bytes.Clone(h.publicKey)
}

func (h *Hub) append(kind, id, value string) Change {
	h.position++
	change := Change{Cursor: Cursor{Generation: h.generation, Position: h.position}, Kind: kind, ID: id, Value: value}
	h.history = append(h.history, change)
	if len(h.history) > h.historyCap {
		h.history = h.history[1:]
		h.base = h.history[0].Cursor.Position - 1
	}
	return change
}

// announceSession is a white-box compatibility helper for legacy tests.
func (h *Hub) announceSession(id, actor, value string, ttl time.Duration) (Change, error) {
	return h.announceSessionActivity(id, actor, value, ttl, ActivityUpdate{})
}

// announceSessionActivity renews one test session and optionally changes only that
// session's advisory activity. The private session identifier remains the
// authority boundary; callers cannot address another lease by public handle.
func (h *Hub) announceSessionActivity(id, actor, value string, ttl time.Duration, update ActivityUpdate) (Change, error) {
	return h.announceSessionIdentity(id, actor, "", value, ttl, update)
}

// announceSessionIdentity binds a test lease to both its custodial actor name
// and its durable actor fingerprint. The name remains private service routing;
// addressed delivery uses only the fingerprint.
func (h *Hub) announceSessionIdentity(id, actor, fingerprint, value string, ttl time.Duration, update ActivityUpdate) (Change, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if id == "" || actor == "" {
		return Change{}, errors.New("session and actor are required")
	}
	// Historical white-box tests use long leases so expiry is not their
	// concern. Production entry points reject, rather than cap, this bound.
	if ttl > MaxSessionTTL {
		ttl = MaxSessionTTL
	}
	h.expire(h.now())
	if existing, exists := h.presence[id]; exists && (existing.actor != actor || existing.fingerprint != fingerprint) {
		return Change{}, errors.New("session is already bound to another actor")
	}
	return h.announceFor(id, actor, fingerprint, value, ttl, update)
}

func (h *Hub) openSessionIdentityLocked(actor, fingerprint, value string, ttl time.Duration, update ActivityUpdate) (string, Change, error) {
	for {
		credential, err := mintCredential()
		if err != nil {
			return "", Change{}, err
		}
		if _, exists := h.presence[credential]; exists {
			continue
		}
		change, err := h.announceFor(credential, actor, fingerprint, value, ttl, update)
		return credential, change, err
	}
}

// openSessionIdentity is retained for white-box compatibility tests whose
// deliberately synthetic fingerprints cannot be derived from a public key.
func (h *Hub) openSessionIdentity(actor, fingerprint, value string, ttl time.Duration, update ActivityUpdate) (string, Change, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.expire(h.now())
	return h.openSessionIdentityLocked(actor, fingerprint, value, ttl, update)
}

// OpenTrustedSession mints a lease for an in-process custodial adapter that has
// already authenticated the actor or holds custody of actorKey. It has no
// proof challenge and therefore must not be routed from public or browser
// input. Such transports must use PrepareSession and OpenSession; merely
// knowing a public key proves no authority to publish presence under it.
func (h *Hub) OpenTrustedSession(actor string, actorKey ed25519.PublicKey, value string, ttl time.Duration, update ActivityUpdate) (string, Change, error) {
	fingerprint, err := ActorFingerprint(actorKey)
	if err != nil {
		return "", Change{}, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.expire(h.now())
	return h.openSessionIdentityLocked(actor, fingerprint, value, ttl, update)
}

// PrepareSession creates a bounded, expiring proof-of-possession challenge.
// It retains the requested lease privately but publishes no presence and uses
// no actor session quota. OpenSession consumes the challenge exactly once.
func (h *Hub) PrepareSession(actor string, actorKey ed25519.PublicKey, value string, ttl time.Duration, update ActivityUpdate) (SessionChallenge, error) {
	fingerprint, err := ActorFingerprint(actorKey)
	if err != nil {
		return SessionChallenge{}, err
	}
	ttl, err = normalizeLease(actor, value, ttl)
	if err != nil {
		return SessionChallenge{}, err
	}
	activity, err := normalizeActivity(Activity{}, update, false)
	if err != nil {
		return SessionChallenge{}, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	h.expire(now)
	if len(h.pendingSessions) >= MaxPendingSessionChallenges {
		return SessionChallenge{}, fmt.Errorf("pending session challenge limit of %d reached", MaxPendingSessionChallenges)
	}
	for {
		nonce := make([]byte, 32)
		if _, err := rand.Read(nonce); err != nil {
			return SessionChallenge{}, err
		}
		challenge := SessionChallenge{
			Version: 0, Generation: h.generation, Nonce: nonce,
			ActorKey: bytes.Clone(actorKey),
		}
		key, err := sessionChallengeKey(challenge)
		if err != nil {
			return SessionChallenge{}, err
		}
		if _, exists := h.pendingSessions[key]; exists {
			continue
		}
		h.pendingSessions[key] = pendingSession{
			actor: actor, fingerprint: fingerprint, value: value, ttl: ttl,
			activity: cloneActivity(activity), expiresAt: now.Add(SessionChallengeTTL),
		}
		return cloneSessionChallenge(challenge), nil
	}
}

// OpenSession verifies private-key possession and atomically makes the
// prepared lease visible. A failed or replayed submission cannot reuse the
// challenge; prepare a new one after any failure.
func (h *Hub) OpenSession(challenge SessionChallenge, actorSignature []byte) (string, Change, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.expire(h.now())
	key, err := sessionChallengeKey(challenge)
	if err != nil {
		return "", Change{}, errors.New("session challenge is not valid")
	}
	pending, exists := h.pendingSessions[key]
	if !exists {
		return "", Change{}, errors.New("session challenge is not valid")
	}
	delete(h.pendingSessions, key)
	if len(actorSignature) != ed25519.SignatureSize {
		return "", Change{}, errors.New("invalid actor signature")
	}
	signingBytes, err := SessionSigningBytes(challenge)
	if err != nil || !ed25519.Verify(challenge.ActorKey, signingBytes, actorSignature) {
		return "", Change{}, errors.New("invalid actor signature")
	}
	return h.openSessionIdentityLocked(
		pending.actor, pending.fingerprint, pending.value, pending.ttl,
		activityUpdateFrom(pending.activity),
	)
}

// renewSessionIdentity extends one exact live binding. A malformed, expired,
// revoked, cross-actor or cross-resident credential receives the same fixed
// refusal, so errors cannot be used as a credential oracle.
func (h *Hub) renewSessionIdentity(credential, actor, fingerprint, value string, ttl time.Duration, update ActivityUpdate) (Change, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.expire(h.now())
	if !validCredential(credential) {
		return Change{}, errors.New("credential is not valid")
	}
	entry, exists := h.presence[credential]
	if !exists || entry.actor != actor || entry.fingerprint != fingerprint {
		return Change{}, errors.New("credential is not valid")
	}
	return h.announceFor(credential, actor, fingerprint, value, ttl, update)
}

// RenewSession extends one exact credential without allowing either its actor
// name or public key binding to move.
func (h *Hub) RenewSession(credential, actor string, actorKey ed25519.PublicKey, value string, ttl time.Duration, update ActivityUpdate) (Change, error) {
	fingerprint, err := ActorFingerprint(actorKey)
	if err != nil {
		return Change{}, errors.New("credential is not valid")
	}
	return h.renewSessionIdentity(credential, actor, fingerprint, value, ttl, update)
}

// RevokeSession ends one exact lease and its inbox immediately. Departure is
// credential-bearing and therefore fails closed instead of publishing a
// meaningless departure for an unknown value.
func (h *Hub) RevokeSession(credential string) (Change, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.expire(h.now())
	if !validCredential(credential) {
		return Change{}, errors.New("credential is not valid")
	}
	entry, exists := h.presence[credential]
	if !exists {
		return Change{}, errors.New("credential is not valid")
	}
	delete(h.presence, credential)
	h.deleteInbox(credential)
	change := h.append("departure", entry.handle, "")
	h.removeParticipant(credential)
	h.forgetAllIfEmpty()
	return change, nil
}

func normalizeLease(actor, value string, ttl time.Duration) (time.Duration, error) {
	if actor == "" || len(actor) > MaxActorNameBytes || !utf8.ValidString(actor) {
		return 0, fmt.Errorf("actor must be non-empty UTF-8 of at most %d bytes", MaxActorNameBytes)
	}
	if len(value) > MaxPresenceValueBytes || !utf8.ValidString(value) {
		return 0, fmt.Errorf("presence value must be UTF-8 of at most %d bytes", MaxPresenceValueBytes)
	}
	if ttl <= 0 {
		ttl = DefaultSessionTTL
	}
	if ttl > MaxSessionTTL {
		return 0, fmt.Errorf("session TTL must not exceed %s", MaxSessionTTL)
	}
	return ttl, nil
}

func (h *Hub) announceFor(id, actor, fingerprint, value string, ttl time.Duration, update ActivityUpdate) (Change, error) {
	var err error
	ttl, err = normalizeLease(actor, value, ttl)
	if err != nil {
		return Change{}, err
	}
	existing, exists := h.presence[id]
	if !exists {
		if len(h.presence) >= MaxLiveSessions {
			return Change{}, fmt.Errorf("live session limit of %d reached", MaxLiveSessions)
		}
		sameActor := 0
		for _, entry := range h.presence {
			if (fingerprint != "" && entry.fingerprint == fingerprint) || (fingerprint == "" && entry.fingerprint == "" && entry.actor == actor) {
				sameActor++
			}
		}
		if sameActor >= MaxSessionsPerActor {
			return Change{}, fmt.Errorf("actor session limit of %d reached", MaxSessionsPerActor)
		}
		// A reused identifier begins a new leased session. Never carry an old
		// recipient's inbox across expiry or an identity switch.
		h.deleteInbox(id)
	}
	activity, err := normalizeActivity(existing.activity, update, exists)
	if err != nil {
		return Change{}, err
	}
	handle := existing.handle
	if handle == "" {
		minted, err := mintHandle()
		if err != nil {
			return Change{}, err
		}
		handle = minted
	}
	now := h.now()
	// The activity clock moves only when the activity does. A new session
	// starts it; a renewal that changes nothing carries the old value forward.
	changedAt := existing.activityChangedAt
	if !exists || changedAt.IsZero() || !activityEqual(existing.activity, activity) {
		changedAt = now
	}
	h.presence[id] = presenceEntry{value: value, actor: actor, fingerprint: fingerprint, inbox: existing.inbox, handle: handle, activity: activity, expiresAt: now.Add(ttl), activityChangedAt: changedAt}
	if exists && existing.value == value && existing.actor == actor && existing.fingerprint == fingerprint && activityEqual(existing.activity, activity) {
		copy := cloneActivity(activity)
		return Change{Cursor: Cursor{Generation: h.generation, Position: h.position}, Kind: "renewal", ID: handle, Value: value, Activity: &copy}, nil
	}
	kind := "presence"
	if exists && existing.value == value && existing.actor == actor && existing.fingerprint == fingerprint {
		kind = "activity"
	}
	change := h.append(kind, handle, value)
	copy := cloneActivity(activity)
	change.Activity = &copy
	// append records the change before the typed payload is known. Replace the
	// just-appended history value so wait observes the same transition returned
	// to the updater.
	h.history[len(h.history)-1] = change
	return change, nil
}

func normalizeActivity(current Activity, update ActivityUpdate, exists bool) (Activity, error) {
	if !exists || current.Status == "" {
		current = Activity{Status: ActivityAvailable, Focus: []string{}}
	} else {
		current = cloneActivity(current)
	}
	if update.Status != nil {
		switch *update.Status {
		case ActivityAvailable, ActivityBusy, ActivityWaiting, ActivityBlocked:
			current.Status = *update.Status
		default:
			return Activity{}, fmt.Errorf("unknown activity status %q", *update.Status)
		}
	}
	if update.Focus != nil {
		if len(*update.Focus) > MaxFocusEvents {
			return Activity{}, fmt.Errorf("focus has %d events; maximum is %d", len(*update.Focus), MaxFocusEvents)
		}
		seen := make(map[string]bool, len(*update.Focus))
		current.Focus = current.Focus[:0]
		for _, event := range *update.Focus {
			if event == "" || len(event) > MaxFocusEventBytes || !utf8.ValidString(event) {
				return Activity{}, errors.New("focus events must be non-empty UTF-8 identifiers of at most 256 bytes")
			}
			if !seen[event] {
				seen[event] = true
				current.Focus = append(current.Focus, event)
			}
		}
		sort.Strings(current.Focus)
	}
	if update.Note != nil {
		note := strings.TrimSpace(*update.Note)
		if len(note) > MaxActivityNoteBytes || !utf8.ValidString(note) {
			return Activity{}, fmt.Errorf("activity note must be UTF-8 and at most %d bytes", MaxActivityNoteBytes)
		}
		current.Note = note
	}
	return current, nil
}

func activityUpdateFrom(activity Activity) ActivityUpdate {
	status := activity.Status
	focus := append([]string(nil), activity.Focus...)
	note := activity.Note
	return ActivityUpdate{Status: &status, Focus: &focus, Note: &note}
}

func cloneActivity(activity Activity) Activity {
	activity.Focus = append([]string(nil), activity.Focus...)
	if activity.Focus == nil {
		activity.Focus = []string{}
	}
	return activity
}

func cloneInboxFrame(frame InboxFrame) InboxFrame {
	frame.Recipients = append([]string(nil), frame.Recipients...)
	if frame.Recipients == nil {
		frame.Recipients = []string{}
	}
	return frame
}

func (h *Hub) cloneInbox(state *inboxState) Inbox {
	inbox := Inbox{Frames: []InboxFrame{}}
	if state == nil {
		return inbox
	}
	visible := len(state.refs)
	if visible > MaxInboxFrames {
		visible = MaxInboxFrames
		inbox.Skipped = len(state.refs) - visible
	}
	for _, ref := range state.refs[:visible] {
		if frame, ok := h.inboxFrame(ref); ok {
			inbox.Frames = append(inbox.Frames, frame)
		}
	}
	return inbox
}

func (h *Hub) inboxFrame(ref frameRef) (InboxFrame, bool) {
	conversation := h.convs[ref.conversation]
	if conversation == nil || ref.sequence >= uint64(len(conversation.frames)) {
		return InboxFrame{}, false
	}
	frame := conversation.frames[ref.sequence]
	var message Message
	if json.Unmarshal(frame.Payload, &message) != nil {
		return InboxFrame{}, false
	}
	return InboxFrame{
		Actor: actorFingerprint(frame.ActorKey), Text: message.Text, About: message.About,
		Conversation: ref.conversation, Sequence: ref.sequence, Re: message.Re,
		Recipients: append([]string(nil), message.Recipients...),
		Thread:     ref.conversation + ":" + strconv.FormatUint(ref.sequence, 10),
	}, true
}

func activityEqual(left, right Activity) bool {
	if left.Status != right.Status || left.Note != right.Note || len(left.Focus) != len(right.Focus) {
		return false
	}
	for index := range left.Focus {
		if left.Focus[index] != right.Focus[index] {
			return false
		}
	}
	return true
}

func (h *Hub) depart(id string) Change {
	h.mu.Lock()
	defer h.mu.Unlock()
	handle := h.presence[id].handle
	delete(h.presence, id)
	h.deleteInbox(id)
	change := h.append("departure", handle, "")
	h.removeParticipant(id)
	h.forgetAllIfEmpty()
	return change
}

func (h *Hub) expire(now time.Time) {
	for key, pending := range h.pendingSessions {
		if !pending.expiresAt.After(now) {
			delete(h.pendingSessions, key)
		}
	}
	for id, entry := range h.presence {
		if !entry.expiresAt.After(now) {
			delete(h.presence, id)
			h.deleteInbox(id)
			h.append("expiration", entry.handle, "")
			h.removeParticipant(id)
		}
	}
	h.forgetAllIfEmpty()
}

func (h *Hub) deleteInbox(session string) {
	delete(h.inboxes, session)
}

func (h *Hub) forgetAllIfEmpty() {
	if len(h.presence) != 0 {
		return
	}
	for conversation := range h.convs {
		h.forgetConversation(conversation)
	}
}

// HandleFor reports the public handle of a live session, for callers that
// hold the identifier legitimately. There is no inverse: a handle cannot be
// turned back into the identifier that authorizes acts.
func (h *Hub) HandleFor(id string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.presence[id].handle
}

func (h *Hub) SessionActor(id string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.expire(h.now())
	entry, exists := h.presence[id]
	return entry.actor, exists && entry.actor != ""
}

// LiveSessionsForActor reports how many unexpired leases belong to one exact
// durable actor. It deliberately returns only an aggregate: callers do not
// receive the private session identifiers or the public handles minted for
// those leases.
func (h *Hub) LiveSessionsForActor(fingerprint string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.expire(h.now())
	held := 0
	for _, entry := range h.presence {
		if entry.fingerprint == fingerprint {
			held++
		}
	}
	return held
}

// EnableInbox opts one exact live lease into addressed delivery. Presence by
// itself is intentionally insufficient: browser and older adapter sessions do
// not have the private status/wait/ack protocol needed to consume an inbox.
func (h *Hub) EnableInbox(id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.expire(h.now())
	entry, exists := h.presence[id]
	if !exists {
		return errors.New("credential is not valid")
	}
	entry.inbox = true
	h.presence[id] = entry
	return nil
}

func (h *Hub) forgetConversation(id string) bool {
	conversation, exists := h.convs[id]
	if !exists {
		return false
	}
	h.retainedFrames -= len(conversation.frames)
	for _, frame := range conversation.frames {
		h.retainedBytes -= len(frame.Payload)
	}
	delete(h.convs, id)
	delete(h.participants, id)
	for _, inbox := range h.inboxes {
		kept := inbox.refs[:0]
		for _, ref := range inbox.refs {
			if ref.conversation != id {
				kept = append(kept, ref)
			}
		}
		inbox.refs = kept
	}
	for about, conversation := range h.about {
		if conversation == id {
			delete(h.about, about)
		}
	}
	h.append("forgotten", id, "")
	return true
}

func (h *Hub) removeParticipant(session string) {
	for conversation, participants := range h.participants {
		delete(participants, session)
		if len(participants) == 0 {
			h.forgetConversation(conversation)
		}
	}
}

func (h *Hub) conversationGenesis(scope string, nonce []byte) ([]byte, string, error) {
	genesis, err := encode(conversationGenesis{
		Version: 1, Generation: h.generation, NexusKey: h.publicKey,
		Scope: scope, Nonce: nonce,
	})
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(genesis)
	id := "eph:sha256:" + hex.EncodeToString(digest[:])
	return genesis, id, nil
}

// Snapshot and ChangesSince form the barrier: the returned cursor is captured
// under the same lock as the projected state.
func (h *Hub) Snapshot() Snapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.expire(h.now())
	return h.snapshotLocked()
}

func (h *Hub) snapshotLocked() Snapshot {
	// Keyed by handle: an observer learns who is here and can follow each
	// session across renewals without receiving the credential itself.
	presence := make(map[string]string, len(h.presence))
	activity := make(map[string]Activity, len(h.presence))
	for _, entry := range h.presence {
		presence[entry.handle] = entry.value
		activity[entry.handle] = cloneActivity(entry.activity)
	}
	conversations := make([]string, 0, len(h.convs))
	for id := range h.convs {
		conversations = append(conversations, id)
	}
	sort.Strings(conversations)
	return Snapshot{
		Cursor: Cursor{Generation: h.generation, Position: h.position}, Presence: presence,
		Activity: activity, Conversations: conversations,
	}
}

// SnapshotForSession captures global live state and one exact session's
// unacknowledged addressed inbox under the same cursor barrier. A caller must
// hold the private session identifier; absent or expired sessions are refused
// rather than represented as an empty inbox.
func (h *Hub) SnapshotForSession(session string) (Snapshot, Inbox, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.expire(h.now())
	if _, exists := h.presence[session]; !exists {
		return Snapshot{}, Inbox{}, errors.New("credential is not valid")
	}
	return h.snapshotLocked(), h.cloneInbox(h.inboxes[session]), nil
}

// Observe atomically captures global live state and, when session is nonempty,
// that exact lease's private inbox. Supplying a cursor also returns changes;
// an unavailable cursor sets Reset while still returning current state.
func (h *Hub) Observe(session string, cursor *Cursor) (Observation, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.expire(h.now())
	var identity presenceEntry
	if session != "" {
		var exists bool
		identity, exists = h.presence[session]
		if !exists {
			return Observation{}, errors.New("credential is not valid")
		}
	}
	observation := Observation{
		Snapshot: h.snapshotLocked(), Inbox: h.cloneInbox(h.inboxes[session]),
		Actor: identity.actor, Fingerprint: identity.fingerprint,
	}
	if cursor == nil {
		return observation, nil
	}
	changes, _, err := h.changesSinceLocked(*cursor, session)
	if err != nil {
		if errors.Is(err, ErrReset) {
			observation.Reset = true
			return observation, nil
		}
		return Observation{}, err
	}
	observation.Changes = changes
	return observation, nil
}

// Acknowledge removes exact thread handles from one leased session's inbox.
// Repeated or already-expired handles are harmless; malformed handles fail so
// a client cannot believe it acknowledged something the service could not
// identify.
func (h *Hub) Acknowledge(session string, handles []string) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.expire(h.now())
	if _, exists := h.presence[session]; !exists {
		return 0, errors.New("credential is not valid")
	}
	if len(handles) > MaxInboxFrames {
		return 0, fmt.Errorf("acknowledgement has %d handles; maximum is %d", len(handles), MaxInboxFrames)
	}
	wanted := make(map[string]bool, len(handles))
	for _, handle := range handles {
		if _, _, err := parseThreadHandle(handle); err != nil {
			return 0, err
		}
		wanted[handle] = true
	}
	state := h.inboxes[session]
	if state == nil || len(wanted) == 0 {
		return 0, nil
	}
	removed := 0
	kept := state.refs[:0]
	for _, ref := range state.refs {
		handle := ref.conversation + ":" + strconv.FormatUint(ref.sequence, 10)
		if !wanted[handle] {
			kept = append(kept, ref)
		} else {
			removed++
		}
	}
	state.refs = kept
	return removed, nil
}

func parseThreadHandle(handle string) (string, uint64, error) {
	if len(handle) > MaxMessageIDBytes {
		return "", 0, fmt.Errorf("thread handle exceeds %d bytes", MaxMessageIDBytes)
	}
	separator := strings.LastIndexByte(handle, ':')
	if separator <= 0 || separator == len(handle)-1 {
		return "", 0, errors.New("thread handle must be <conversation>:<sequence>")
	}
	sequenceText := handle[separator+1:]
	sequence, err := strconv.ParseUint(sequenceText, 10, 64)
	if err != nil || strconv.FormatUint(sequence, 10) != sequenceText {
		return "", 0, errors.New("thread handle has a non-canonical sequence")
	}
	return handle[:separator], sequence, nil
}

func (h *Hub) ChangesSince(cursor Cursor) ([]Change, Cursor, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.expire(h.now())
	return h.changesSinceLocked(cursor, "")
}

// ChangesSinceForSession keeps ordinary live changes intact but includes a
// decoded frame only when it is still pending for this exact session.
func (h *Hub) ChangesSinceForSession(cursor Cursor, session string) ([]Change, Cursor, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.expire(h.now())
	if _, exists := h.presence[session]; !exists {
		return nil, Cursor{Generation: h.generation, Position: h.position}, errors.New("credential is not valid")
	}
	return h.changesSinceLocked(cursor, session)
}

// WaitComposite joins a caller-supplied durable read with this Hub's live
// observation. The callback owns all durable interpretation; host/live sees
// only its opaque value and verified frontier. The returned cursor preserves
// the two namespaces separately. Polling is bounded because another process
// may advance ordinary Git storage without notifying this process.
func WaitComposite[T any](ctx context.Context, hub *Hub, session string, cursor CompositeCursor, timeout time.Duration, read func(context.Context) (T, DurableFrontier, error)) (CompositeObservation[T], bool, error) {
	var latest CompositeObservation[T]
	if hub == nil || read == nil {
		return latest, false, errors.New("live runtime and durable reader are required")
	}
	if timeout <= 0 || timeout > MaxCompositeWait {
		timeout = DefaultCompositeWait
	}
	check := func() (CompositeObservation[T], bool, error) {
		observed, err := hub.Observe(session, &cursor.Live)
		if err != nil {
			return CompositeObservation[T]{}, false, err
		}
		durable, frontier, err := read(ctx)
		if err != nil {
			return CompositeObservation[T]{}, false, err
		}
		result := CompositeObservation[T]{
			Durable: durable, Live: observed,
			Cursor: CompositeCursor{Durable: frontier, Live: observed.Snapshot.Cursor},
		}
		pending := len(observed.Inbox.Frames) != 0
		changed := observed.Reset || len(observed.Changes) != 0 || pending || frontier != cursor.Durable
		return result, changed, nil
	}

	var changed bool
	var err error
	if latest, changed, err = check(); err != nil || changed {
		return latest, changed, err
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(compositePollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return latest, false, ctx.Err()
		case <-timer.C:
			return latest, false, nil
		case <-ticker.C:
			if latest, changed, err = check(); err != nil || changed {
				return latest, changed, err
			}
		}
	}
}

func (h *Hub) changesSinceLocked(cursor Cursor, session string) ([]Change, Cursor, error) {
	current := Cursor{Generation: h.generation, Position: h.position}
	if cursor.Generation != h.generation || cursor.Position < h.base || cursor.Position > h.position {
		return nil, current, ErrReset
	}
	changes := make([]Change, 0, h.position-cursor.Position)
	pending := make(map[string]bool)
	if inbox := h.inboxes[session]; inbox != nil {
		for _, ref := range inbox.refs {
			pending[ref.conversation+":"+strconv.FormatUint(ref.sequence, 10)] = true
		}
	}
	for _, change := range h.history {
		if change.Cursor.Position > cursor.Position {
			copy := change
			if change.Activity != nil {
				activity := cloneActivity(*change.Activity)
				copy.Activity = &activity
			}
			copy.Frame = nil
			if session != "" && change.Frame != nil && pending[change.Frame.Thread] {
				frame := cloneInboxFrame(*change.Frame)
				copy.Frame = &frame
			}
			changes = append(changes, copy)
		}
	}
	return changes, current, nil
}

func actorBody(generation, conversationID string, sequence uint64, previousHash, payload []byte) frameActorBody {
	return frameActorBody{Version: 0, Generation: generation, Conversation: conversationID, Sequence: sequence, PreviousHash: previousHash, Payload: payload}
}

func sessionBody(challenge SessionChallenge) sessionChallengeBody {
	return sessionChallengeBody{
		Version: challenge.Version, Generation: challenge.Generation,
		Nonce: challenge.Nonce, ActorKey: challenge.ActorKey,
	}
}

// SessionSigningBytes returns the deterministic bytes that prove possession
// of the key named by an expiring session challenge. A transport can return
// these bytes directly to a browser, which needs only WebCrypto Ed25519 and no
// CBOR encoder.
func SessionSigningBytes(challenge SessionChallenge) ([]byte, error) {
	if challenge.Version != 0 || !validPrefixedHex(challenge.Generation, "generation:", 16) || len(challenge.Nonce) != 32 || len(challenge.ActorKey) != ed25519.PublicKeySize {
		return nil, errors.New("session challenge is not valid")
	}
	encoded, err := encode(sessionBody(challenge))
	if err != nil {
		return nil, err
	}
	return append([]byte(sessionProofDomain), encoded...), nil
}

func sessionChallengeKey(challenge SessionChallenge) (string, error) {
	signingBytes, err := SessionSigningBytes(challenge)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(signingBytes)
	return hex.EncodeToString(digest[:]), nil
}

// ActorSigningBytes returns the public deterministic bytes covered by an
// actor signature. The encoding is the v0 domain followed by RFC 8949 core
// deterministic CBOR of the six-field actor-frame tuple.
func ActorSigningBytes(draft Draft) ([]byte, error) {
	encoded, err := encode(actorBody(draft.Generation, draft.Conversation, draft.Sequence, draft.PreviousHash, draft.Payload))
	if err != nil {
		return nil, err
	}
	return append([]byte(actorFrameDomain), encoded...), nil
}

// ActorFingerprint returns the full lowercase fingerprint bound to a session.
func ActorFingerprint(key ed25519.PublicKey) (string, error) {
	return gitseqhost.ActorFingerprint(key)
}

func encode(value any) ([]byte, error) {
	mode, err := cbor.CoreDetEncOptions().EncMode()
	if err != nil {
		return nil, err
	}
	return mode.Marshal(value)
}

func signIssuerBytes(domain string, key ed25519.PrivateKey, value any) ([]byte, error) {
	encoded, err := encode(value)
	if err != nil {
		return nil, err
	}
	return ed25519.Sign(key, append([]byte(domain), encoded...)), nil
}

func verifyBytes(domain string, key ed25519.PublicKey, value any, signature []byte) bool {
	encoded, err := encode(value)
	return err == nil && ed25519.Verify(key, append([]byte(domain), encoded...), signature)
}

func cloneDraft(draft Draft) Draft {
	draft.ConversationGenesis = bytes.Clone(draft.ConversationGenesis)
	draft.PreviousHash = bytes.Clone(draft.PreviousHash)
	draft.Payload = bytes.Clone(draft.Payload)
	return draft
}

func cloneSessionChallenge(challenge SessionChallenge) SessionChallenge {
	challenge.Nonce = bytes.Clone(challenge.Nonce)
	challenge.ActorKey = bytes.Clone(challenge.ActorKey)
	return challenge
}

func cloneFrame(frame Frame) Frame {
	frame.ConversationGenesis = bytes.Clone(frame.ConversationGenesis)
	frame.PreviousHash = bytes.Clone(frame.PreviousHash)
	frame.Payload = bytes.Clone(frame.Payload)
	frame.ActorKey = bytes.Clone(frame.ActorKey)
	frame.ActorSignature = bytes.Clone(frame.ActorSignature)
	frame.NexusKey = bytes.Clone(frame.NexusKey)
	frame.NexusSignature = bytes.Clone(frame.NexusSignature)
	return frame
}

func validScope(scope string) bool {
	return scope != "" && len(scope) <= MaxMessageIDBytes && utf8.ValidString(scope)
}

func decodeConversationGenesis(raw []byte) (conversationGenesis, error) {
	if len(raw) == 0 || len(raw) > MaxConversationGenesisBytes {
		return conversationGenesis{}, fmt.Errorf("conversation genesis must be between 1 and %d bytes", MaxConversationGenesisBytes)
	}
	var genesis conversationGenesis
	if err := cbor.Unmarshal(raw, &genesis); err != nil {
		return conversationGenesis{}, errors.New("conversation genesis is invalid")
	}
	canonical, err := encode(genesis)
	if err != nil || !bytes.Equal(canonical, raw) {
		return conversationGenesis{}, errors.New("conversation genesis is not canonical")
	}
	return genesis, nil
}

func (h *Hub) validateSessionActorLocked(session string, actorKey ed25519.PublicKey) (presenceEntry, error) {
	entry, exists := h.presence[session]
	fingerprint, err := ActorFingerprint(actorKey)
	if !exists || err != nil || entry.fingerprint == "" || entry.fingerprint != fingerprint {
		return presenceEntry{}, errors.New("credential is not valid")
	}
	return entry, nil
}

// PrepareFrameForSession snapshots the exact live chain position an actor must
// sign. It can expire old leases as every live read does, but it does not open
// or reserve a conversation; only submission can publish draft state.
func (h *Hub) PrepareFrameForSession(session, scope, conversationID string, payload []byte, actorKey ed25519.PublicKey) (Draft, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.expire(h.now())
	return h.prepareFrameLocked(session, scope, conversationID, payload, actorKey)
}

func (h *Hub) prepareFrameLocked(session, scope, conversationID string, payload []byte, actorKey ed25519.PublicKey) (Draft, error) {
	if _, err := h.validateSessionActorLocked(session, actorKey); err != nil {
		return Draft{}, err
	}
	if !validScope(scope) {
		return Draft{}, fmt.Errorf("scope must be non-empty UTF-8 of at most %d bytes", MaxMessageIDBytes)
	}
	if err := h.canPublish(len(payload)); err != nil {
		return Draft{}, err
	}

	resolved, existing, err := h.findConversation(scope, conversationID)
	if err != nil {
		return Draft{}, err
	}
	var genesis, previous []byte
	var sequence uint64
	if existing == nil {
		nonce := make([]byte, 32)
		if _, err := rand.Read(nonce); err != nil {
			return Draft{}, err
		}
		genesis, resolved, err = h.conversationGenesis(scope, nonce)
		if err != nil {
			return Draft{}, err
		}
	} else {
		genesis = existing.genesis
		sequence = existing.next
		previous = existing.last
	}
	return cloneDraft(Draft{
		Scope: scope, Version: 0, Generation: h.generation,
		ConversationGenesis: genesis, Conversation: resolved,
		Sequence: sequence, PreviousHash: previous, Payload: payload,
	}), nil
}

func (h *Hub) validateDraft(draft Draft) (*conversation, bool, error) {
	if draft.Version != 0 || draft.Generation != h.generation || !validScope(draft.Scope) {
		return nil, false, ErrStaleDraft
	}
	genesis, err := decodeConversationGenesis(draft.ConversationGenesis)
	if err != nil {
		return nil, false, err
	}
	if genesis.Version != 1 || genesis.Generation != h.generation || genesis.Scope != draft.Scope || len(genesis.Nonce) != 32 || !bytes.Equal(genesis.NexusKey, h.publicKey) {
		return nil, false, errors.New("draft conversation genesis is invalid")
	}
	digest := sha256.Sum256(draft.ConversationGenesis)
	if draft.Conversation != "eph:sha256:"+hex.EncodeToString(digest[:]) {
		return nil, false, errors.New("draft conversation does not match its genesis")
	}
	anchored := h.about[draft.Scope]
	existing := h.convs[draft.Conversation]
	if existing == nil {
		if anchored != "" || draft.Sequence != 0 || len(draft.PreviousHash) != 0 {
			return nil, false, ErrStaleDraft
		}
		return nil, true, nil
	}
	if anchored != draft.Conversation || existing.about != draft.Scope || !bytes.Equal(existing.genesis, draft.ConversationGenesis) || existing.next != draft.Sequence || !bytes.Equal(existing.last, draft.PreviousHash) {
		return nil, false, ErrStaleDraft
	}
	return existing, false, nil
}

// SubmitFrameForSession verifies and sequences a frame signed outside the
// runtime. Replays and drafts invalidated by another publication fail closed.
func (h *Hub) SubmitFrameForSession(session string, submission Submission) (Frame, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.expire(h.now())
	return h.submitFrameLocked(session, submission)
}

func (h *Hub) submitFrameLocked(session string, submission Submission) (Frame, error) {
	if _, err := h.validateSessionActorLocked(session, submission.ActorKey); err != nil {
		return Frame{}, err
	}
	if len(submission.ActorSignature) != ed25519.SignatureSize {
		return Frame{}, errors.New("invalid actor signature")
	}
	if err := h.canPublish(len(submission.Draft.Payload)); err != nil {
		return Frame{}, err
	}
	current, install, err := h.validateDraft(submission.Draft)
	if err != nil {
		return Frame{}, err
	}
	body := actorBody(submission.Draft.Generation, submission.Draft.Conversation, submission.Draft.Sequence, submission.Draft.PreviousHash, submission.Draft.Payload)
	if !verifyBytes(actorFrameDomain, submission.ActorKey, body, submission.ActorSignature) {
		return Frame{}, errors.New("invalid actor signature")
	}
	if install {
		current = &conversation{
			genesis: bytes.Clone(submission.Draft.ConversationGenesis), about: submission.Draft.Scope,
		}
	}
	frame, digest, err := h.buildSignedFrame(current, body, submission.ActorKey, submission.ActorSignature)
	if err != nil {
		return Frame{}, err
	}
	// From here onward the operation is a no-fail commit. A first conversation
	// becomes visible only after both signatures and the frame hash exist.
	if install {
		h.convs[submission.Draft.Conversation] = current
		h.about[submission.Draft.Scope] = submission.Draft.Conversation
		h.append("conversation", submission.Draft.Conversation, "")
	}
	h.commitSignedFrame(current, frame, digest)
	if h.participants[submission.Draft.Conversation] == nil {
		h.participants[submission.Draft.Conversation] = make(map[string]bool)
	}
	h.participants[submission.Draft.Conversation][session] = true
	return frame, nil
}

// PrepareMessageForSession resolves the final recipient list, including an
// exact reply parent's author, before returning the bytes the actor signs.
func (h *Hub) PrepareMessageForSession(session, conversationID string, message Message, actorKey ed25519.PublicKey) (Draft, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.expire(h.now())
	if _, err := h.validateSessionActorLocked(session, actorKey); err != nil {
		return Draft{}, err
	}
	if err := validateMessage(message); err != nil {
		return Draft{}, err
	}
	recipients, err := normalizeRecipients(message.Recipients)
	if err != nil {
		return Draft{}, err
	}
	message.Recipients = recipients
	if message.Re != "" && conversationID == "" {
		conversationID, _, err = parseThreadHandle(message.Re)
		if err != nil {
			return Draft{}, err
		}
	}
	resolvedConversationID, existingConversation, err := h.findConversation(message.About, conversationID)
	if err != nil {
		return Draft{}, err
	}
	recipients = append([]string(nil), message.Recipients...)
	if message.Re != "" {
		parentConversation, parentSequence, err := parseThreadHandle(message.Re)
		if err != nil {
			return Draft{}, err
		}
		if existingConversation == nil || parentConversation != resolvedConversationID || parentSequence >= uint64(len(existingConversation.frames)) {
			return Draft{}, errors.New("reply target is not an existing frame in this conversation")
		}
		recipients = append(recipients, actorFingerprint(existingConversation.frames[parentSequence].ActorKey))
	}
	message.Recipients, err = normalizeRecipients(recipients)
	if err != nil {
		return Draft{}, err
	}
	payload, err := json.Marshal(message)
	if err != nil {
		return Draft{}, err
	}
	return h.prepareFrameLocked(session, message.About, resolvedConversationID, payload, actorKey)
}

// SubmitMessageForSession verifies an externally signed message and then
// publishes its bounded addressed delivery atomically with the frame.
func (h *Hub) SubmitMessageForSession(session string, submission Submission) (Frame, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.expire(h.now())
	if _, err := h.validateSessionActorLocked(session, submission.ActorKey); err != nil {
		return Frame{}, err
	}
	var message Message
	if err := json.Unmarshal(submission.Draft.Payload, &message); err != nil {
		return Frame{}, errors.New("signed message payload is invalid")
	}
	if err := validateMessage(message); err != nil || message.About != submission.Draft.Scope {
		return Frame{}, errors.New("signed message does not match its scope")
	}
	normalized, err := normalizeRecipients(message.Recipients)
	if err != nil || !equalStrings(normalized, message.Recipients) {
		return Frame{}, errors.New("signed message recipients are not canonical")
	}
	canonicalPayload, err := json.Marshal(message)
	if err != nil || !bytes.Equal(canonicalPayload, submission.Draft.Payload) {
		return Frame{}, errors.New("signed message payload is not canonical")
	}
	if message.Re != "" {
		parentConversation, parentSequence, err := parseThreadHandle(message.Re)
		existing := h.convs[submission.Draft.Conversation]
		if err != nil || existing == nil || parentConversation != submission.Draft.Conversation || parentSequence >= uint64(len(existing.frames)) {
			return Frame{}, errors.New("reply target is not an existing frame in this conversation")
		}
		parentAuthor := actorFingerprint(existing.frames[parentSequence].ActorKey)
		if !containsExact(message.Recipients, parentAuthor) {
			return Frame{}, errors.New("signed reply omits its exact parent author")
		}
	}
	recipientSet := make(map[string]bool, len(message.Recipients))
	for _, recipient := range message.Recipients {
		recipientSet[recipient] = true
	}
	recipientParticipants := make([]string, 0)
	recipientSessions := make([]string, 0)
	for recipientSession, recipientEntry := range h.presence {
		if recipientSession == session || !recipientSet[recipientEntry.fingerprint] {
			continue
		}
		recipientParticipants = append(recipientParticipants, recipientSession)
		if recipientEntry.inbox {
			recipientSessions = append(recipientSessions, recipientSession)
		}
	}
	if err := h.canEnqueue(recipientSessions); err != nil {
		return Frame{}, err
	}
	frame, err := h.submitFrameLocked(session, submission)
	if err != nil {
		return Frame{}, err
	}
	author := actorFingerprint(submission.ActorKey)
	delivery := InboxFrame{
		Actor: author, Text: message.Text, About: message.About,
		Conversation: frame.Conversation, Sequence: frame.Sequence, Re: message.Re,
		Recipients: append([]string(nil), message.Recipients...),
		Thread:     frame.Conversation + ":" + strconv.FormatUint(frame.Sequence, 10),
	}
	last := len(h.history) - 1
	h.history[last].Frame = &delivery
	ref := frameRef{conversation: frame.Conversation, sequence: frame.Sequence}
	for _, recipientSession := range recipientParticipants {
		h.participants[frame.Conversation][recipientSession] = true
	}
	for _, recipientSession := range recipientSessions {
		h.addInbox(recipientSession, ref)
	}
	return frame, nil
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func containsExact(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func actorFingerprint(key []byte) string {
	fingerprint, err := gitseqhost.ActorFingerprint(ed25519.PublicKey(key))
	if err != nil {
		panic("live runtime received an invalid verified actor key")
	}
	return fingerprint
}

func validateMessage(message Message) error {
	if message.About == "" || len(message.About) > MaxMessageIDBytes || !utf8.ValidString(message.About) {
		return fmt.Errorf("about must be non-empty UTF-8 of at most %d bytes", MaxMessageIDBytes)
	}
	if message.Text == "" || len(message.Text) > MaxMessageTextBytes || !utf8.ValidString(message.Text) {
		return fmt.Errorf("text must be non-empty UTF-8 of at most %d bytes", MaxMessageTextBytes)
	}
	if len(message.Re) > MaxMessageIDBytes || !utf8.ValidString(message.Re) {
		return fmt.Errorf("reply handle must be UTF-8 and at most %d bytes", MaxMessageIDBytes)
	}
	if len(message.Recipients) > MaxMessageRecipients {
		return fmt.Errorf("message has %d recipients; maximum is %d", len(message.Recipients), MaxMessageRecipients)
	}
	return nil
}

func normalizeRecipients(recipients []string) ([]string, error) {
	if len(recipients) > MaxMessageRecipients+1 {
		return nil, fmt.Errorf("message has more than %d recipients", MaxMessageRecipients)
	}
	seen := make(map[string]bool, len(recipients))
	normalized := make([]string, 0, len(recipients))
	for _, recipient := range recipients {
		if len(recipient) != gitseqhost.ActorFingerprintLength {
			return nil, errors.New("recipient must be a full actor fingerprint")
		}
		if !gitseqhost.ValidActorFingerprint(recipient) {
			return nil, errors.New("recipient must be a lowercase hexadecimal actor fingerprint")
		}
		if !seen[recipient] {
			seen[recipient] = true
			normalized = append(normalized, recipient)
		}
	}
	if len(normalized) > MaxMessageRecipients {
		return nil, fmt.Errorf("message has %d recipients; maximum is %d", len(normalized), MaxMessageRecipients)
	}
	sort.Strings(normalized)
	return normalized, nil
}

func (h *Hub) canEnqueue(sessions []string) error {
	for _, session := range sessions {
		if inbox := h.inboxes[session]; inbox != nil && len(inbox.refs) >= MaxPendingInboxFrames {
			return errors.New("a recipient inbox is full; retry after it is acknowledged or expires")
		}
	}
	return nil
}

func (h *Hub) addInbox(session string, ref frameRef) {
	state := h.inboxes[session]
	if state == nil {
		state = &inboxState{}
		h.inboxes[session] = state
	}
	for _, pending := range state.refs {
		if pending == ref {
			return
		}
	}
	state.refs = append(state.refs, ref)
}

// findConversation resolves an existing about/conversation pair without
// changing the room. A blank result means publication may open a new
// conversation after every payload and inbox bound has passed.
func (h *Hub) findConversation(about, conversationID string) (string, *conversation, error) {
	anchored := h.about[about]
	if conversationID == "" {
		conversationID = anchored
	}
	if conversationID == "" {
		return "", nil, nil
	}
	conversation, exists := h.convs[conversationID]
	if !exists {
		return "", nil, errors.New("unknown conversation")
	}
	if conversation.about != "" && conversation.about != about {
		return "", nil, errors.New("conversation does not match the about anchor")
	}
	if anchored != "" && anchored != conversationID {
		return "", nil, errors.New("conversation does not match the about anchor")
	}
	return conversationID, conversation, nil
}

func (h *Hub) canPublish(payloadBytes int) error {
	if payloadBytes > MaxFramePayloadBytes {
		return fmt.Errorf("frame payload exceeds %d bytes", MaxFramePayloadBytes)
	}
	if h.retainedFrames >= MaxRetainedFrames || h.retainedBytes+payloadBytes > MaxRetainedBytes {
		return errors.New("live conversation capacity is full; retry after participants leave")
	}
	return nil
}

func (h *Hub) buildSignedFrame(conversation *conversation, body frameActorBody, actorKey ed25519.PublicKey, actorSignature []byte) (Frame, []byte, error) {
	if conversation == nil || body.Generation != h.generation || body.Sequence != conversation.next || !bytes.Equal(body.PreviousHash, conversation.last) {
		return Frame{}, nil, ErrStaleDraft
	}
	if err := h.canPublish(len(body.Payload)); err != nil {
		return Frame{}, nil, err
	}
	nexusBody := frameNexusBody{ActorBody: body, ActorKey: actorKey, ActorSignature: actorSignature}
	nexusSignature, err := signIssuerBytes(nexusFrameDomain, h.privateKey, nexusBody)
	if err != nil {
		return Frame{}, nil, err
	}
	frame := Frame{
		Version: 0, Generation: h.generation, Conversation: body.Conversation,
		ConversationGenesis: bytes.Clone(conversation.genesis),
		Sequence:            conversation.next, PreviousHash: bytes.Clone(conversation.last), Payload: bytes.Clone(body.Payload),
		ActorKey: bytes.Clone(actorKey), ActorSignature: bytes.Clone(actorSignature),
		NexusKey: bytes.Clone(h.publicKey), NexusSignature: nexusSignature,
	}
	digest, err := FrameHash(frame)
	if err != nil {
		return Frame{}, nil, err
	}
	return cloneFrame(frame), digest, nil
}

func (h *Hub) commitSignedFrame(conversation *conversation, frame Frame, digest []byte) {
	conversation.last = bytes.Clone(digest)
	conversation.next++
	conversation.frames = append(conversation.frames, cloneFrame(frame))
	h.retainedFrames++
	h.retainedBytes += len(frame.Payload)
	h.append("frame", frame.Conversation, fmt.Sprint(frame.Sequence))
}

func (h *Hub) Frames(conversationID string) ([]Frame, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.expire(h.now())
	conversation, ok := h.convs[conversationID]
	if !ok {
		return nil, errors.New("unknown conversation")
	}
	frames := make([]Frame, len(conversation.frames))
	for index := range conversation.frames {
		frames[index] = cloneFrame(conversation.frames[index])
	}
	return frames, nil
}

func FrameHash(frame Frame) ([]byte, error) {
	encoded, err := encode(frame)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(encoded)
	return digest[:], nil
}

func validPrefixedHex(value, prefix string, bytesCount int) bool {
	if len(value) != len(prefix)+(bytesCount*2) || !strings.HasPrefix(value, prefix) {
		return false
	}
	decoded, err := hex.DecodeString(value[len(prefix):])
	return err == nil && hex.EncodeToString(decoded) == value[len(prefix):]
}

func VerifyFrame(frame Frame, trustedNexusKey ed25519.PublicKey) error {
	validPrevious := (frame.Sequence == 0 && len(frame.PreviousHash) == 0) || (frame.Sequence > 0 && len(frame.PreviousHash) == sha256.Size)
	if frame.Version != 0 || !validPrefixedHex(frame.Generation, "generation:", 16) || !validPrefixedHex(frame.Conversation, "eph:sha256:", sha256.Size) ||
		len(frame.Payload) > MaxFramePayloadBytes || len(frame.ConversationGenesis) == 0 || len(frame.ConversationGenesis) > MaxConversationGenesisBytes || !validPrevious ||
		len(frame.ActorKey) != ed25519.PublicKeySize || len(frame.ActorSignature) != ed25519.SignatureSize || len(frame.NexusKey) != ed25519.PublicKeySize || len(frame.NexusSignature) != ed25519.SignatureSize || len(trustedNexusKey) != ed25519.PublicKeySize {
		return errors.New("invalid frame shape")
	}
	if !bytes.Equal(frame.NexusKey, trustedNexusKey) {
		return errors.New("frame is not signed by the trusted nexus")
	}
	genesis, err := decodeConversationGenesis(frame.ConversationGenesis)
	if err != nil || genesis.Version != 1 || genesis.Generation != frame.Generation || len(genesis.Nonce) != 32 || !bytes.Equal(genesis.NexusKey, frame.NexusKey) || !validScope(genesis.Scope) {
		return errors.New("invalid conversation genesis envelope")
	}
	genesisHash := sha256.Sum256(frame.ConversationGenesis)
	if frame.Conversation != "eph:sha256:"+hex.EncodeToString(genesisHash[:]) {
		return errors.New("conversation ID does not match its genesis envelope")
	}
	body := actorBody(frame.Generation, frame.Conversation, frame.Sequence, frame.PreviousHash, frame.Payload)
	if !verifyBytes(actorFrameDomain, ed25519.PublicKey(frame.ActorKey), body, frame.ActorSignature) {
		return errors.New("invalid actor signature")
	}
	nexusBody := frameNexusBody{ActorBody: body, ActorKey: frame.ActorKey, ActorSignature: frame.ActorSignature}
	if !verifyBytes(nexusFrameDomain, ed25519.PublicKey(frame.NexusKey), nexusBody, frame.NexusSignature) {
		return errors.New("invalid nexus signature")
	}
	return nil
}

func VerifyChain(frames []Frame, trustedNexusKey ed25519.PublicKey) error {
	var previous []byte
	for index, frame := range frames {
		if frame.Sequence != uint64(index) || !bytes.Equal(frame.PreviousHash, previous) {
			return fmt.Errorf("frame %d breaks conversation chain", index)
		}
		if index > 0 && (frame.Generation != frames[0].Generation || frame.Conversation != frames[0].Conversation || !bytes.Equal(frame.ConversationGenesis, frames[0].ConversationGenesis) || !bytes.Equal(frame.NexusKey, frames[0].NexusKey)) {
			return fmt.Errorf("frame %d changes conversation identity", index)
		}
		if err := VerifyFrame(frame, trustedNexusKey); err != nil {
			return fmt.Errorf("frame %d: %w", index, err)
		}
		var err error
		previous, err = FrameHash(frame)
		if err != nil {
			return err
		}
	}
	return nil
}

// MaxAttentionActors bounds one attention read. Focus is already bounded per
// session and sessions per actor are bounded, but the number of distinct actors
// is not, so the aggregate needs its own ceiling or a busy workroom would put an
// unbounded list on every tool result.
const MaxAttentionActors = 16

// AttentionActor is one actor whose leased focus names an event the caller is
// looking at. It is advisory: it reports who says they are attending to the
// same thing, and confers nothing.
type AttentionActor struct {
	// Fingerprint is the full durable fingerprint, never a prefix. A truncated
	// identity invites the reader to match it against another truncation.
	Fingerprint string `json:"fingerprint"`
	Name        string `json:"name"`
	// Sessions counts this actor's live sessions that matched, so one person
	// working from two windows reads as one actor rather than two people.
	Sessions int    `json:"sessions"`
	Status   string `json:"status"`
	Note     string `json:"note,omitempty"`
	// Matched lists the caller's event identifiers this actor's focus contains,
	// sorted, so the reason for the row is visible rather than inferred.
	Matched []string `json:"matched"`
	// ActivityChangedAt is resident-observed and moves only when the activity
	// moves, so an old timestamp means an old decision, not a quiet client.
	ActivityChangedAt time.Time `json:"activity_changed_at"`
}

// FocusedOn reports live actors whose leased focus exactly contains one or more
// of the given event identifiers, excluding the calling session.
//
// Three properties this deliberately holds. Matching is exact string equality
// on identifiers the caller already has: no prefix, no normalisation, no
// inference about what relates to what, because a guess about relatedness is
// exactly the kind of helpfulness that becomes a claim nobody made. The
// caller's own sessions are filtered out before actors are aggregated rather
// than after, so an actor who is present in two windows and matches in one does
// not have the other silently counted. And the result is capped, with the
// overflow reported rather than dropped.
func (h *Hub) FocusedOn(exclude string, events []string) ([]AttentionActor, int) {
	if len(events) == 0 {
		return nil, 0
	}
	wanted := make(map[string]bool, len(events))
	for _, event := range events {
		if event != "" {
			wanted[event] = true
		}
	}
	if len(wanted) == 0 {
		return nil, 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.expire(h.now())

	type aggregate struct {
		actor     AttentionActor
		matched   map[string]bool
		changedAt time.Time
	}
	byActor := map[string]*aggregate{}
	for session, entry := range h.presence {
		if session == exclude {
			continue
		}
		var hits []string
		for _, event := range entry.activity.Focus {
			if wanted[event] {
				hits = append(hits, event)
			}
		}
		if len(hits) == 0 {
			continue
		}
		// Key on the fingerprint where there is one: it is the durable identity,
		// and two sessions under one name but different keys are two actors.
		key := entry.fingerprint
		if key == "" {
			key = "name:" + entry.actor
		}
		found, held := byActor[key]
		if !held {
			found = &aggregate{
				actor:   AttentionActor{Fingerprint: entry.fingerprint, Name: entry.actor},
				matched: map[string]bool{},
			}
			byActor[key] = found
		}
		found.actor.Sessions++
		for _, hit := range hits {
			found.matched[hit] = true
		}
		// Across an actor's sessions, report the most recent decision and the
		// status that goes with it, so two windows do not race to describe one
		// person by whichever map iteration happened to land last.
		if found.changedAt.IsZero() || entry.activityChangedAt.After(found.changedAt) {
			found.changedAt = entry.activityChangedAt
			found.actor.Status = string(entry.activity.Status)
			found.actor.Note = entry.activity.Note
		}
	}

	actors := make([]AttentionActor, 0, len(byActor))
	for _, found := range byActor {
		found.actor.ActivityChangedAt = found.changedAt
		found.actor.Matched = make([]string, 0, len(found.matched))
		for event := range found.matched {
			found.actor.Matched = append(found.actor.Matched, event)
		}
		sort.Strings(found.actor.Matched)
		actors = append(actors, found.actor)
	}
	// Order by most recently moved, then by fingerprint so the tie is stable
	// rather than dependent on map iteration.
	sort.Slice(actors, func(i, j int) bool {
		if !actors[i].ActivityChangedAt.Equal(actors[j].ActivityChangedAt) {
			return actors[i].ActivityChangedAt.After(actors[j].ActivityChangedAt)
		}
		return actors[i].Fingerprint < actors[j].Fingerprint
	})
	omitted := 0
	if len(actors) > MaxAttentionActors {
		omitted = len(actors) - MaxAttentionActors
		actors = actors[:MaxAttentionActors]
	}
	return actors, omitted
}
