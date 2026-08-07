// Package nexus is an intentionally amnesiac collaboration rendezvous.
// Its cursor namespace dies with the process; clients must retain any frames
// they care about.
package nexus

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/fxamacker/cbor/v2"
)

const (
	actorFrameDomain = "gitseq.nexus.actor-frame.v0\x00"
	nexusFrameDomain = "gitseq.nexus.order-frame.v0\x00"
)

var ErrReset = errors.New("nexus cursor is no longer available; take a new snapshot")

// sessionHandleDomain separates handle hashing from every other use of
// SHA-256 in this package, so a handle can never collide with a digest
// computed for some other purpose.
const sessionHandleDomain = "gitseq.nexus.session-handle.v0\x00"

// SessionHandle names a session in public without disclosing it. A session
// identifier authorizes speech: whoever presents one has the service sign
// frames with that session's actor key, and can end its lease. It is
// therefore a bearer credential, and observers must never be handed one.
//
// Observers still need to tell sessions apart — to follow a renewal, notice a
// departure, or count who is here — so presence is published under this
// stable, one-way handle instead. Knowing a handle grants nothing.
func SessionHandle(id string) string {
	if id == "" {
		return ""
	}
	sum := sha256.Sum256(append([]byte(sessionHandleDomain), id...))
	return "session:" + hex.EncodeToString(sum[:8])
}

type Cursor struct {
	Generation string `json:"generation"`
	Position   uint64 `json:"position"`
}

type Change struct {
	Cursor Cursor `json:"cursor"`
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Value  string `json:"value,omitempty"`
}

type Snapshot struct {
	Cursor        Cursor            `json:"cursor"`
	Presence      map[string]string `json:"presence"`
	Conversations []string          `json:"conversations"`
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

type conversation struct {
	next    uint64
	last    []byte
	genesis []byte
	frames  []Frame
}

type presenceEntry struct {
	value     string
	actor     string
	expiresAt time.Time
}

type Hub struct {
	mu           sync.Mutex
	generation   string
	position     uint64
	base         uint64
	historyCap   int
	history      []Change
	presence     map[string]presenceEntry
	convs        map[string]*conversation
	about        map[string]string
	participants map[string]map[string]bool
	publicKey    ed25519.PublicKey
	privateKey   ed25519.PrivateKey
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
		generation:   "generation:" + hex.EncodeToString(generationBytes),
		historyCap:   historyCap,
		presence:     make(map[string]presenceEntry),
		convs:        make(map[string]*conversation),
		about:        make(map[string]string),
		participants: make(map[string]map[string]bool),
		publicKey:    bytes.Clone(publicKey),
		privateKey:   bytes.Clone(privateKey),
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

// AnnounceSession leases a session to exactly one custodial actor. Presence,
// binding and expiry are updated under the same lock as conversation retention.
func (h *Hub) AnnounceSession(id, actor, value string, ttl time.Duration) (Change, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if id == "" || actor == "" {
		return Change{}, errors.New("session and actor are required")
	}
	h.expire(time.Now())
	if existing, exists := h.presence[id]; exists && existing.actor != actor {
		return Change{}, errors.New("session is already bound to another actor")
	}
	return h.announceFor(id, actor, value, ttl), nil
}

func (h *Hub) announceFor(id, actor, value string, ttl time.Duration) Change {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	existing, exists := h.presence[id]
	h.presence[id] = presenceEntry{value: value, actor: actor, expiresAt: time.Now().Add(ttl)}
	if exists && existing.value == value && existing.actor == actor {
		return Change{Cursor: Cursor{Generation: h.generation, Position: h.position}, Kind: "renewal", ID: SessionHandle(id), Value: value}
	}
	return h.append("presence", SessionHandle(id), value)
}

func (h *Hub) Depart(id string) Change {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.presence, id)
	change := h.append("departure", SessionHandle(id), "")
	h.removeParticipant(id)
	h.forgetAllIfEmpty()
	return change
}

func (h *Hub) Expire(now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.expire(now)
}

func (h *Hub) expire(now time.Time) {
	for id, entry := range h.presence {
		if !entry.expiresAt.After(now) {
			delete(h.presence, id)
			h.append("expiration", SessionHandle(id), "")
			h.removeParticipant(id)
		}
	}
	h.forgetAllIfEmpty()
}

func (h *Hub) forgetAllIfEmpty() {
	if len(h.presence) != 0 {
		return
	}
	for conversation := range h.convs {
		h.forgetConversation(conversation)
	}
}

func (h *Hub) SessionActor(id string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.expire(time.Now())
	entry, exists := h.presence[id]
	return entry.actor, exists && entry.actor != ""
}

func (h *Hub) forgetConversation(id string) bool {
	if _, exists := h.convs[id]; !exists {
		return false
	}
	delete(h.convs, id)
	delete(h.participants, id)
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

func (h *Hub) openConversation(nonce []byte) (string, Change, error) {
	genesis, err := encode(struct {
		_          struct{} `cbor:",toarray"`
		Version    uint64
		Generation string
		NexusKey   []byte
		Nonce      []byte
	}{Version: 0, Generation: h.generation, NexusKey: h.publicKey, Nonce: nonce})
	if err != nil {
		return "", Change{}, err
	}
	digest := sha256.Sum256(genesis)
	id := "eph:sha256:" + hex.EncodeToString(digest[:])
	h.convs[id] = &conversation{genesis: genesis}
	return id, h.append("conversation", id, ""), nil
}

// Snapshot and ChangesSince form the barrier: the returned cursor is captured
// under the same lock as the projected state.
func (h *Hub) Snapshot() Snapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.expire(time.Now())
	// Keyed by handle: an observer learns who is here and can follow each
	// session across renewals without receiving the credential itself.
	presence := make(map[string]string, len(h.presence))
	for id, entry := range h.presence {
		presence[SessionHandle(id)] = entry.value
	}
	conversations := make([]string, 0, len(h.convs))
	for id := range h.convs {
		conversations = append(conversations, id)
	}
	sort.Strings(conversations)
	return Snapshot{
		Cursor:   Cursor{Generation: h.generation, Position: h.position},
		Presence: presence, Conversations: conversations,
	}
}

func (h *Hub) ChangesSince(cursor Cursor) ([]Change, Cursor, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.expire(time.Now())
	current := Cursor{Generation: h.generation, Position: h.position}
	if cursor.Generation != h.generation || cursor.Position < h.base || cursor.Position > h.position {
		return nil, current, ErrReset
	}
	changes := make([]Change, 0, h.position-cursor.Position)
	for _, change := range h.history {
		if change.Cursor.Position > cursor.Position {
			changes = append(changes, change)
		}
	}
	return changes, current, nil
}

func actorBody(generation, conversationID string, sequence uint64, previousHash, payload []byte) frameActorBody {
	return frameActorBody{Version: 0, Generation: generation, Conversation: conversationID, Sequence: sequence, PreviousHash: previousHash, Payload: payload}
}

func encode(value any) ([]byte, error) {
	mode, err := cbor.CoreDetEncOptions().EncMode()
	if err != nil {
		return nil, err
	}
	return mode.Marshal(value)
}

func signBytes(domain string, key ed25519.PrivateKey, value any) ([]byte, error) {
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

// PublishForSession resolves the about anchor, opens a conversation when
// necessary, records participation and publishes as one live-room operation.
func (h *Hub) PublishForSession(session, about, conversationID string, payload []byte, actorPrivateKey ed25519.PrivateKey) (Frame, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.expire(time.Now())
	if _, exists := h.presence[session]; !exists {
		return Frame{}, errors.New("session is not present")
	}
	if conversationID == "" {
		conversationID = h.about[about]
	}
	if conversationID == "" {
		nonce := make([]byte, 32)
		if _, err := rand.Read(nonce); err != nil {
			return Frame{}, err
		}
		var err error
		conversationID, _, err = h.openConversation(nonce)
		if err != nil {
			return Frame{}, err
		}
		h.about[about] = conversationID
	}
	frame, err := h.publish(conversationID, payload, actorPrivateKey)
	if err != nil {
		return Frame{}, err
	}
	if h.participants[conversationID] == nil {
		h.participants[conversationID] = make(map[string]bool)
	}
	h.participants[conversationID][session] = true
	return frame, nil
}

func (h *Hub) publish(conversationID string, payload []byte, actorPrivateKey ed25519.PrivateKey) (Frame, error) {
	conversation, ok := h.convs[conversationID]
	if !ok {
		return Frame{}, errors.New("unknown conversation")
	}
	body := actorBody(h.generation, conversationID, conversation.next, conversation.last, payload)
	actorSignature, err := signBytes(actorFrameDomain, actorPrivateKey, body)
	if err != nil {
		return Frame{}, err
	}
	actorKey := actorPrivateKey.Public().(ed25519.PublicKey)
	nexusBody := frameNexusBody{ActorBody: body, ActorKey: actorKey, ActorSignature: actorSignature}
	nexusSignature, err := signBytes(nexusFrameDomain, h.privateKey, nexusBody)
	if err != nil {
		return Frame{}, err
	}
	frame := Frame{
		Version: 0, Generation: h.generation, Conversation: conversationID,
		ConversationGenesis: bytes.Clone(conversation.genesis),
		Sequence:            conversation.next, PreviousHash: bytes.Clone(conversation.last), Payload: bytes.Clone(payload),
		ActorKey: bytes.Clone(actorKey), ActorSignature: actorSignature,
		NexusKey: bytes.Clone(h.publicKey), NexusSignature: nexusSignature,
	}
	digest, err := FrameHash(frame)
	if err != nil {
		return Frame{}, err
	}
	conversation.last = digest
	conversation.next++
	conversation.frames = append(conversation.frames, frame)
	h.append("frame", conversationID, fmt.Sprint(frame.Sequence))
	return frame, nil
}

func (h *Hub) Frames(conversationID string) ([]Frame, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.expire(time.Now())
	conversation, ok := h.convs[conversationID]
	if !ok {
		return nil, errors.New("unknown conversation")
	}
	frames := make([]Frame, len(conversation.frames))
	copy(frames, conversation.frames)
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

func VerifyFrame(frame Frame, trustedNexusKey ed25519.PublicKey) error {
	if frame.Version != 0 || frame.Generation == "" || frame.Conversation == "" || len(frame.ActorKey) != ed25519.PublicKeySize || len(frame.NexusKey) != ed25519.PublicKeySize {
		return errors.New("invalid frame shape")
	}
	if !bytes.Equal(frame.NexusKey, trustedNexusKey) {
		return errors.New("frame is not signed by the trusted nexus")
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
