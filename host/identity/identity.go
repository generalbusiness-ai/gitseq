// Package identity resolves the persistent identity behind a signing key.
//
// The kernel already answers the only identity question it can answer without
// meaning: which key signed this record. That is enough to play — a key minted
// in a browser, with no account, no prompt and no ssh key file on disk, is a
// first-class actor. Everything above it is an upgrade, never a requirement,
// and this package is that upgrade, sitting in the host layer so that no
// application invents its own version of it.
//
// The upgrade is an anchor: a record saying that one signing key belongs to a
// persistent identity, for this repository, within a scope, until an expiry.
// It is an ordinary record in the ordinary log, so it replays identically
// forever from any clone.
//
// # Two axes
//
// An anchor is not simply strong or weak. Two independent things vary, and
// both are reported, because collapsing them into one number would hide which
// assumption a reader is actually making:
//
//   - [Vouching] says who stands behind the endorsement. [SelfSigned] means
//     the identity's own Nostr key signed the anchor; [Witnessed] means the
//     deployment's key says a provider said so.
//   - [Verification] says what a reader must trust to check it. [InLog] — a
//     signature carried in the log — verifies offline, forever. [LiveLookup] —
//     a claim needing a third party to answer again — verifies only while that
//     third party cooperates, and may answer differently later.
//
// Vouching is never claimed in a payload, only proved: it is derived from
// which key signed the record, against the witness key the log itself
// declares. A record cannot promote itself.
//
// # What is here
//
// Three acts, and one resolution:
//
//	rec, err := identity.DeclareWitness(ctx, ws, initializer, witnessPublic, []string{"github"})
//	rec, err := identity.Endorse(ctx, ws, endorser, identity.Anchor{...})
//	rec, err := identity.Revoke(ctx, ws, endorser, anchorRecordID)
//	res := identity.Resolve(log)
//	who := res.LookupAt(recordID)
//	label := who.Display(actorFingerprint)
//
// [Endorse] is one act for every rung. A deployment holding the witness key
// endorses a newcomer's session key and names the identity a provider
// reported. A Nostr user carries a [NostrProof] from their root key, and the
// session key also signs the record. A person already anchored endorses
// another key — a new device, or an agent — and names no identity, because the
// identity is inherited from the endorser and an endorser cannot mint one it
// does not have. Resolution derives the rung from signatures that actually
// verify; a payload cannot promote itself.
//
// # Resolution is the authority
//
// Nothing here gates appending, and that is deliberate: it is the same posture
// the [host] package takes. An anchor-shaped record signed by a key with no
// standing is recorded exactly as signed and resolves to nothing, forever. A
// witness declaration not signed by the key that initialized the repository
// has no force. So no appender can make a repository's identities unreadable
// by writing a record, and no admission check has to be trusted to keep one
// out.
//
// # Where the trust actually sits
//
// A self-signed Nostr anchor carries a BIP-340 signature from the identity it
// names. [NostrDelegation] constructs the deterministic, repository-bound
// message that signature covers. Its secp256k1 verification belongs here in
// the host identity interpreter and never enters the Ed25519 kernel.
//
// A witnessed anchor is the deployment's word. Whoever holds the witness key
// can mint an anchor for any identity in the schemes that key was declared
// for; that risk is inherent to witnessing, and what bounds it is that every
// such record occupies a signed position in the log, so a false one is visible
// and attributable rather than deniable. The public half is recorded in the
// log at setup, which is what lets bindings outlive the deployment that made
// them: a reader years later checks the signature against the log's own
// declaration, not against a server that may be gone.
//
// Custody of the witness private key is the deployment's, under the supported
// single-operator host posture: every process inside that boundary can act as
// every key the deployment holds, this one included. This package authenticates
// nobody and authorizes nothing. It says who a key belongs to, and leaves what
// that is worth to the application's fold.
//
// # Time
//
// Anchor, delegation and withdrawal boundaries are judged by verified record
// position. Expiry alone is judged against the sequencer's signed timestamp on
// the record being judged, never against the reader's clock. [Resolution.LookupAt]
// resolves that exact record id, so a timestamp tie cannot change authority.
package identity

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// The record families this package recognizes. They are host vocabulary, not
// application vocabulary: an application profile cannot rename or extend them,
// and a host that has never heard of the application still reads them.
const (
	// WitnessSchema declares the public half of a deployment's witnessing key.
	WitnessSchema = "gitseq/identity-witness@0"
	// AnchorSchema endorses one signing key as belonging to an identity.
	AnchorSchema = "gitseq/identity-anchor@0"
	// RevokeSchema withdraws an endorsement before its expiry.
	RevokeSchema = "gitseq/identity-revoke@0"
)

const (
	// GitHubScheme is the identity scheme the GitHub login rung produces.
	GitHubScheme = "github"
	// NostrScheme is the identity scheme a self-signed Nostr anchor produces.
	NostrScheme = "nostr"
)

// Bounds on the untrusted strings that reach this vocabulary. A provider's
// answer, a caller's scope, and a witness's scheme list all arrive from
// outside, and a payload with no ceiling is a payload an appender chooses the
// size of. These are generous for every real value and small enough that a
// record carrying one stays a record.
const (
	maxSchemeLen  = 32
	maxSubjectLen = 128
	maxHandleLen  = 128
	maxScopeLen   = 128
	maxSchemes    = 8
)

// Vouching says who stands behind an endorsement. Larger is stronger, so the
// weaker of two is their minimum, which is what a delegation inherits.
type Vouching uint8

const (
	// VouchingUnknown is the zero value: no endorsement at all.
	VouchingUnknown Vouching = iota
	// Witnessed means the deployment's witness key signed the endorsement,
	// saying a provider vouched for the identity. Readers weigh it exactly as
	// they weigh that deployment.
	Witnessed
	// SelfSigned means the identity's own root key signed the endorsement.
	// Nostr anchors produce this rung after their BIP-340 proof verifies.
	SelfSigned
)

func (v Vouching) String() string {
	switch v {
	case Witnessed:
		return "witnessed"
	case SelfSigned:
		return "self-signed"
	}
	return "unvouched"
}

// Verification says what a reader must trust to check an endorsement. Larger
// is stronger, on the same rule as [Vouching].
type Verification uint8

const (
	// VerificationUnknown is the zero value: no endorsement at all.
	VerificationUnknown Verification = iota
	// LiveLookup means checking the claim again needs a third party to answer
	// again. It may answer differently later, or not at all.
	LiveLookup
	// InLog means the signature that carries the claim is in the log. It
	// verifies offline, forever, from any clone.
	InLog
)

func (v Verification) String() string {
	switch v {
	case LiveLookup:
		return "live-lookup"
	case InLog:
		return "in-log"
	}
	return "unverified"
}

// wire values for the verification axis. Vouching has none on purpose: it is
// derived from the signature, so a payload has no way to state it.
const (
	wireInLog      = "in-log"
	wireLiveLookup = "live-lookup"
)

func (v Verification) wire() string {
	if v == LiveLookup {
		return wireLiveLookup
	}
	return wireInLog
}

func verificationFromWire(value string) (Verification, error) {
	switch value {
	case "", wireInLog:
		// Absent means in-log. The endorsement's own signature is in the log
		// by construction, so that is the honest default, and only a witness
		// whose underlying check needs a third party has anything else to say.
		return InLog, nil
	case wireLiveLookup:
		return LiveLookup, nil
	}
	return VerificationUnknown, fmt.Errorf("unknown verification %q", value)
}

// Identity is the persistent identity a signing key resolves to.
type Identity struct {
	// Scheme names the identity namespace, for example "github". Lowercase
	// ASCII letters, digits and hyphens.
	Scheme string `json:"scheme"`
	// Subject is the provider's own identifier for the account, and it is
	// what this identity means. Use the identifier the provider will not
	// reissue — GitHub's numeric account id, not the login — because a
	// reusable name would let a later account inherit an earlier one's
	// history.
	Subject string `json:"subject"`
	// Handle is the human-readable name the provider reported when the
	// endorsement was made. [Resolved.Display] may show it, but it may be stale
	// and it is never authority: two
	// identities are the same one when Scheme and Subject match, whatever
	// their handles say.
	Handle string `json:"handle,omitempty"`
}

func (i Identity) validate() error {
	if err := boundedName("identity scheme", i.Scheme, maxSchemeLen); err != nil {
		return err
	}
	if i.Subject == "" {
		return errors.New("identity subject is required")
	}
	if err := boundedText("identity subject", i.Subject, maxSubjectLen); err != nil {
		return err
	}
	return boundedText("identity handle", i.Handle, maxHandleLen)
}

// Anchor is one endorsement of a signing key, as it is recorded.
//
// Which rung it occupies is not in here, because a record cannot be trusted to
// say. [Resolve] decides it from signatures that actually verify.
type Anchor struct {
	// Genesis is the repository this endorsement is good for. [Endorse] fills
	// it in; the repository is not the caller's to choose. It is what stops an
	// endorsement from one log being replayed into another.
	Genesis string `json:"genesis"`
	// Subject is the fingerprint of the signing key being endorsed — the
	// browser-minted session key, the new device, or the agent.
	Subject string `json:"subject"`
	// Identity is who the subject is. A witness states it, because the
	// provider it checked is the only source of it. An endorser who is not the
	// witness must leave it empty: the identity is inherited from that
	// endorser's own anchor, and an endorser cannot mint one it does not
	// itself hold.
	Identity *Identity `json:"identity,omitempty"`
	// Scope is what the endorsement is for, in the application's own words. It
	// is carried unread here.
	Scope string `json:"scope,omitempty"`
	// NotAfter is when the endorsement expires, in Unix seconds. Zero means no
	// expiry of its own — an endorsement inherited from another anchor still
	// cannot outlive it.
	NotAfter int64 `json:"not_after,omitempty"`
	// Verification says how the claim underneath this endorsement checks: it
	// is [InLog] unless a witness says its provider check needs that provider
	// to answer again. Empty means in-log.
	Verification string `json:"verification,omitempty"`
	// Nostr carries a self-signed BIP-340 proof. Its public key becomes the
	// persistent identity, and its signature covers this anchor's repository,
	// subject, scope and expiry. It is mutually exclusive with Identity, which
	// is stated only by a declared deployment witness.
	Nostr *NostrProof `json:"nostr,omitempty"`
}

func (a Anchor) validate() error {
	if a.Genesis == "" {
		return errors.New("anchor genesis is required")
	}
	if a.Subject == "" {
		return errors.New("anchor subject is required")
	}
	if err := boundedText("anchor subject", a.Subject, maxSubjectLen); err != nil {
		return err
	}
	if a.Identity != nil {
		if err := a.Identity.validate(); err != nil {
			return err
		}
	}
	if err := boundedText("anchor scope", a.Scope, maxScopeLen); err != nil {
		return err
	}
	if a.NotAfter < 0 {
		return errors.New("anchor expiry cannot be negative")
	}
	verification, err := verificationFromWire(a.Verification)
	if err != nil {
		return err
	}
	if a.Nostr == nil {
		return nil
	}
	if a.Identity != nil {
		return errors.New("nostr anchor must not state a second identity")
	}
	if verification != InLog {
		return errors.New("nostr anchor verification must be in-log")
	}
	if err := a.Nostr.validate(); err != nil {
		return err
	}
	if !validNostrProof(a) {
		return errors.New("nostr anchor signature is invalid")
	}
	return nil
}

// WitnessDeclaration records the public half of a deployment's witnessing key,
// so that anchors it signs stay checkable after the deployment is gone.
type WitnessDeclaration struct {
	// Genesis is the repository this witness is declared for.
	Genesis string `json:"genesis"`
	// Key is the hex-encoded ed25519 public key of the witness.
	Key string `json:"key"`
	// Schemes are the identity schemes this witness may witness for, for
	// example "github". A witness declared for one provider cannot mint an
	// identity in another's namespace, so adding a provider is a visible act
	// rather than a silent widening.
	Schemes []string `json:"schemes"`
}

func (w WitnessDeclaration) validate() error {
	if w.Genesis == "" {
		return errors.New("witness genesis is required")
	}
	if _, err := decodeWitnessKey(w.Key); err != nil {
		return err
	}
	if len(w.Schemes) == 0 {
		return errors.New("witness declares no scheme, so it could witness for nothing")
	}
	if len(w.Schemes) > maxSchemes {
		return fmt.Errorf("witness declares more than %d schemes", maxSchemes)
	}
	seen := make(map[string]bool, len(w.Schemes))
	for _, scheme := range w.Schemes {
		if err := boundedName("witness scheme", scheme, maxSchemeLen); err != nil {
			return err
		}
		if seen[scheme] {
			return fmt.Errorf("witness scheme %q is listed twice", scheme)
		}
		seen[scheme] = true
	}
	return nil
}

func (w WitnessDeclaration) witnesses(scheme string) bool {
	for _, declared := range w.Schemes {
		if declared == scheme {
			return true
		}
	}
	return false
}

// Revocation withdraws an endorsement before its expiry, so that a key put
// beyond use is provable from the log alone rather than from a server's memory.
type Revocation struct {
	// Genesis is the repository the withdrawn anchor lives in.
	Genesis string `json:"genesis"`
	// Anchor is the record identifier of the endorsement being withdrawn.
	Anchor string `json:"anchor"`
	// Nostr carries the root key's withdrawal proof for a self-signed Nostr
	// anchor. Ordinary witnessed and delegated anchors leave it empty and are
	// withdrawn by the Ed25519 key that endorsed them.
	Nostr *NostrProof `json:"nostr,omitempty"`
}

func (r Revocation) validate() error {
	if r.Genesis == "" {
		return errors.New("revocation genesis is required")
	}
	if r.Anchor == "" {
		return errors.New("revocation names no anchor")
	}
	if err := boundedText("revoked anchor", r.Anchor, maxAnchorRefLen); err != nil {
		return err
	}
	if r.Nostr == nil {
		return nil
	}
	if err := r.Nostr.validate(); err != nil {
		return err
	}
	if !validNostrWithdrawal(r) {
		return errors.New("nostr withdrawal signature is invalid")
	}
	return nil
}

// maxAnchorRefLen bounds a record identifier: two format-qualified object IDs
// joined by a separator, with room to spare for a longer hash than Git has.
const maxAnchorRefLen = 256

// body is what every recorded payload in this package is: something that can
// say whether it is well formed before it is written or after it is read.
type body interface {
	validate() error
}

// encodeBody produces the exact bytes a record carries. Encoding refuses an
// invalid value rather than recording something no reader will accept.
func encodeBody[T body](value T) ([]byte, error) {
	if err := value.validate(); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

// decodeBody reads a recorded payload, admitting only the canonical encoding.
// Two different byte strings can therefore never mean the same thing, which is
// what keeps a fold deterministic when the same log is read by two builds.
//
// The byte comparison at the end is the whole rule, and it is deliberately the
// only one: anything a stricter decoder would reject — an unknown field, a
// duplicated key, a field spelled in another case, added whitespace — survives
// decoding only to re-encode as different bytes, and is refused there. Adding
// a second check for shapes this one already refuses would be a second place
// to keep the same rule.
func decodeBody[T body](data []byte) (T, error) {
	var zero, decoded T
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&decoded); err != nil {
		return zero, err
	}
	if decoder.More() {
		return zero, errors.New("multiple JSON values")
	}
	if err := decoded.validate(); err != nil {
		return zero, err
	}
	canonical, err := json.Marshal(decoded)
	if err != nil {
		return zero, err
	}
	if !bytes.Equal(canonical, data) {
		return zero, errors.New("payload is not canonical JSON")
	}
	return decoded, nil
}

// boundedName accepts a lowercase ASCII identifier of bounded length. A scheme
// names a namespace, so admitting only one spelling of it means two records
// cannot disagree about whether they are in the same one.
func boundedName(what, value string, limit int) error {
	if value == "" {
		return fmt.Errorf("%s is required", what)
	}
	if len(value) > limit {
		return fmt.Errorf("%s is longer than %d bytes", what, limit)
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			continue
		}
		return fmt.Errorf("%s %q is not lowercase ASCII letters, digits and hyphens", what, value)
	}
	return nil
}

// boundedText accepts arbitrary text of bounded length with no control
// characters. A provider's answer reaches a log record and then a display, and
// a control character in it is a way of making one thing read as another.
func boundedText(what, value string, limit int) error {
	if len(value) > limit {
		return fmt.Errorf("%s is longer than %d bytes", what, limit)
	}
	if strings.ContainsFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return fmt.Errorf("%s contains a control character", what)
	}
	return nil
}
