package identity

import (
	"bytes"
	"crypto/ed25519"

	"github.com/generalbusiness-ai/gitseq/host"
)

// maxChain bounds how far an inherited endorsement is followed. A delegation
// can only rest on an anchor that already stood when it was recorded, so the
// chain is acyclic by construction and this bound is never reached by an
// honest log; it is here so that a log built to be pathological costs a reader
// a constant rather than a stack.
const maxChain = 16

// instant is one canonical point in a verified log. Position decides every
// ordering question; timestamp exists only for externally meaningful expiry.
type instant struct {
	position  int
	timestamp int64
}

type recordInstant struct {
	actor string
	at    instant
}

// Resolved is what a signing key turns out to be.
//
// The zero value is the honest answer for a key nobody has vouched for, and it
// is a complete answer rather than a failure: an unanchored key is a
// first-class actor, bound to whoever holds it, and the caller already knows
// its fingerprint.
type Resolved struct {
	// Anchored says whether an endorsement was in force at the instant asked
	// about. When it is false, every other field is zero.
	Anchored bool
	// Identity is who the key belongs to.
	Identity Identity
	// Vouching and Verification are the two axes, already reduced along any
	// chain of inheritance: a delegation is never stronger on either axis than
	// the anchor it descends from.
	Vouching     Vouching
	Verification Verification
	// Scope is what the endorsement was for, in the application's own words.
	Scope string
	// Record is the identifier of the anchor record that answered, so a caller
	// can show its provenance or cite it.
	Record string
}

// anchorRecord is one endorsement that qualified, with what it needs to be
// judged at an instant.
type anchorRecord struct {
	record       string
	identity     Identity
	vouching     Vouching
	verification Verification
	scope        string
	// parent is the anchor this one inherited from, empty for a witnessed
	// anchor. It is followed at lookup time rather than flattened at resolve
	// time, because withdrawing an anchor has to withdraw what it minted.
	parent string
	// from is the endorsement's verified position: it says nothing about
	// records that came before it, even when their timestamps tie.
	from instant
	// notAfter is the stated expiry, zero for none.
	notAfter int64
	// revoked is the verified position of the first effective withdrawal.
	revoked *instant
	// endorserKey is the hex-encoded key that signed the endorsement, and the
	// only key that can withdraw it.
	endorserKey string
	// nostrKey is the root key that may withdraw a self-signed Nostr anchor.
	// It is empty for witnessed anchors and inherited delegations.
	nostrKey string
}

// Resolution is the identity state of one verified log, ready to be asked
// about any instant in it.
//
// It is a fold of the log's identity records and nothing else: no clock, no
// network, no local state. Two clones resolving the same log produce the same
// answers.
type Resolution struct {
	genesis   string
	witness   *WitnessDeclaration
	bySubject map[string][]*anchorRecord
	byRecord  map[string]*anchorRecord
	records   map[string]recordInstant
}

// Resolve folds the identity records in a verified log.
//
// Nothing is raised. A record that does not qualify — unauthorized,
// unparseable, malformed, naming another repository, or claiming an identity
// its signer cannot hand on — is passed over and leaves the previous answer
// standing, so nobody able to append can make a repository's identities
// unreadable by recording one.
func Resolve(log host.Log) *Resolution {
	resolution := &Resolution{
		genesis:   log.Genesis,
		bySubject: map[string][]*anchorRecord{},
		byRecord:  map[string]*anchorRecord{},
		records:   map[string]recordInstant{},
	}
	if len(log.Records) == 0 {
		return resolution
	}
	// The initializing key is the actor on the log's first record, which is
	// the same authority the host binding answers to. Using it here rather
	// than an application role keeps identity readable by a host that holds no
	// application profile.
	initializing := log.Records[0].ActorKey
	var witnessKey ed25519.PublicKey
	for position, record := range log.Records {
		at := instant{position: position, timestamp: record.Timestamp}
		resolution.records[record.ID] = recordInstant{actor: record.Actor, at: at}
		switch record.Schema {
		case WitnessSchema:
			declared, err := decodeBody[WitnessDeclaration](record.Payload)
			if err != nil || declared.Genesis != log.Genesis {
				continue
			}
			if !bytes.Equal(record.ActorKey, initializing) {
				continue
			}
			key, err := decodeWitnessKey(declared.Key)
			if err != nil {
				continue
			}
			// The last authorized declaration wins, so rotating the key or
			// widening its schemes is one more record. Anchors the previous
			// key signed keep the force they had where they stand.
			resolution.witness, witnessKey = &declared, key
		case AnchorSchema:
			resolution.admitAnchor(record, at, witnessKey)
		case RevokeSchema:
			resolution.admitRevocation(record, at, log.Genesis)
		}
	}
	return resolution
}

func (r *Resolution) admitAnchor(record host.Record, at instant, witnessKey ed25519.PublicKey) {
	anchor, err := decodeBody[Anchor](record.Payload)
	if err != nil || anchor.Genesis != r.genesis {
		return
	}
	stated, err := verificationFromWire(anchor.Verification)
	if err != nil {
		return
	}
	entry := &anchorRecord{
		record:      record.ID,
		scope:       anchor.Scope,
		from:        at,
		notAfter:    anchor.NotAfter,
		endorserKey: hexKey(record.ActorKey),
	}
	switch {
	case anchor.Nostr != nil:
		// A Nostr root key endorsed this Gitseq actor. The record must also be
		// signed by the subject so a copied root proof cannot claim a key whose
		// holder never accepted the binding. Anchor validation has already
		// checked the BIP-340 signature over repository, subject, scope and
		// expiry; no network or application profile participates here.
		if record.Actor != anchor.Subject {
			return
		}
		entry.identity = Identity{Scheme: NostrScheme, Subject: anchor.Nostr.PublicKey}
		entry.vouching, entry.verification = SelfSigned, InLog
		entry.nostrKey = anchor.Nostr.PublicKey
	case witnessKey != nil && bytes.Equal(record.ActorKey, witnessKey):
		// The deployment's word about what a provider said. It is the only
		// endorsement that may name an identity, because the provider it
		// checked is the only source of one.
		if anchor.Identity == nil || !r.witness.witnesses(anchor.Identity.Scheme) {
			return
		}
		entry.identity, entry.vouching, entry.verification = *anchor.Identity, Witnessed, stated
	default:
		// A delegation: a new device, or an agent credential. It carries the
		// endorser's own identity and can be no stronger than the endorsement
		// the endorser holds, on either axis. An endorser with nothing to hand
		// on hands on nothing.
		if anchor.Identity != nil || anchor.Nostr != nil {
			return
		}
		parent := r.effectiveAt(record.Actor, at)
		if parent == nil {
			return
		}
		entry.identity = parent.identity
		// Signing a delegation is not itself a vouching rung, so the endorser
		// hands on exactly what it holds. Both axes remain no stronger than the
		// parent anchor that minted this credential.
		entry.vouching = parent.vouching
		entry.verification = min(stated, parent.verification)
		entry.parent = parent.record
	}
	r.bySubject[anchor.Subject] = append(r.bySubject[anchor.Subject], entry)
	r.byRecord[entry.record] = entry
}

func (r *Resolution) admitRevocation(record host.Record, at instant, genesis string) {
	revocation, err := decodeBody[Revocation](record.Payload)
	if err != nil || revocation.Genesis != genesis {
		return
	}
	target, ok := r.byRecord[revocation.Anchor]
	if !ok {
		return
	}
	// Withdrawing is the endorser's act. An ordinary anchor answers to the
	// Ed25519 key that made it; a Nostr anchor also answers to a fresh BIP-340
	// proof from the root key that endorsed it.
	if revocation.Nostr != nil {
		if target.nostrKey == "" || target.nostrKey != revocation.Nostr.PublicKey {
			return
		}
	} else {
		if target.endorserKey != hexKey(record.ActorKey) {
			return
		}
	}
	if target.revoked == nil || at.position < target.revoked.position {
		target.revoked = &at
	}
}

// Witness reports the witness declaration in force, if the log declares one.
func (r *Resolution) Witness() (WitnessDeclaration, bool) {
	if r.witness == nil {
		return WitnessDeclaration{}, false
	}
	return *r.witness, true
}

// LookupAt reports who signed one exact record at that record's verified log
// position. An unknown or changed identifier resolves empty; there is no
// timestamp-only fallback that could turn an ordering tie into authority.
func (r *Resolution) LookupAt(recordID string) Resolved {
	record, ok := r.records[recordID]
	if !ok {
		return Resolved{}
	}
	return r.lookup(record.actor, record.at)
}

func (r *Resolution) lookup(actor string, at instant) Resolved {
	candidates := r.bySubject[actor]
	// The most recent qualifying endorsement answers, so re-anchoring a key —
	// a wider scope, a later expiry — is one more record.
	for i := len(candidates) - 1; i >= 0; i-- {
		if r.effective(candidates[i], at, 0) {
			return Resolved{
				Anchored: true, Identity: candidates[i].identity,
				Vouching: candidates[i].vouching, Verification: candidates[i].verification,
				Scope: candidates[i].scope, Record: candidates[i].record,
			}
		}
	}
	return Resolved{}
}

// effectiveAt is lookup's internal form, used while folding so that a
// delegation is judged against the endorsements that already stood at its
// exact record position.
func (r *Resolution) effectiveAt(actor string, at instant) *anchorRecord {
	candidates := r.bySubject[actor]
	for i := len(candidates) - 1; i >= 0; i-- {
		if r.effective(candidates[i], at, 0) {
			return candidates[i]
		}
	}
	return nil
}

// effective judges one endorsement, and everything it inherits from, at one
// instant.
//
// Position decides both edges: an anchor cannot reach backward, and a
// revocation takes force at its own record and no earlier. Timestamp is read
// only for NotAfter expiry. Recursion carries the same instant into every
// parent, so a delegation cannot blur either boundary.
func (r *Resolution) effective(anchor *anchorRecord, at instant, depth int) bool {
	if depth > maxChain {
		return false
	}
	if at.position < anchor.from.position {
		return false
	}
	if anchor.notAfter != 0 && at.timestamp > anchor.notAfter {
		return false
	}
	if anchor.revoked != nil && at.position >= anchor.revoked.position {
		return false
	}
	if anchor.parent == "" {
		return true
	}
	// An agent credential is exactly as strong as the anchor that minted it,
	// which has to include still standing at all: withdrawing a person's
	// anchor withdraws what that anchor minted, or revocation would leave the
	// keys it was called to stop.
	parent, ok := r.byRecord[anchor.parent]
	if !ok {
		return false
	}
	return r.effective(parent, at, depth+1)
}

func hexKey(key ed25519.PublicKey) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(key)*2)
	for _, b := range key {
		out = append(out, digits[b>>4], digits[b&0x0f])
	}
	return string(out)
}
