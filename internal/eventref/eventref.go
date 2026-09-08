// Package eventref reads the short event references a person can see on the
// workroom's own displays and turns them into the one canonical identifier a
// durable act may carry.
//
// The whole of this lives at a human-input boundary. The kernel, the fold and
// every signed payload know canonical identifiers and nothing else; a record
// number or a hash fragment is a way of typing one, never a way of storing
// one. A boundary resolves every reference of one act against one verified
// event set, shows the caller what it resolved, and only then signs. That
// order is the safety: a mis-resolution is visible while it is still
// correctable, and what lands is always the full identifier.
//
// The searched population is the workroom's own verified durable events, taken
// from the projection the caller already holds. Git's object database is never
// searched: a hex fragment can name a blob, a tree or an ordinary commit, and
// none of those is an event.
package eventref

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	gitseqhost "github.com/generalbusiness-ai/gitseq/host"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// MaxCandidates bounds how many events an ambiguity refusal names. A person
// disambiguating by hand reads a few lines; a fragment matching hundreds is
// answered by saying how many, not by printing them.
const MaxCandidates = 8

// Form is what a boundary makes of one typed string before it looks anything
// up. Only two forms are resolved. The rest are carried through exactly as
// they were typed, which is what keeps actor fingerprints, implementation
// heads, ephemeral handles and free prose from being read as event references.
type Form int

const (
	// FormOpaque is a string this boundary does not read as an event
	// reference at all. It is carried through untouched.
	FormOpaque Form = iota
	// FormCanonical is a full canonical identifier of this workroom.
	FormCanonical
	// FormExternal is a full canonical identifier of another workroom, or of
	// this genesis under another object format. It names an event this room
	// cannot verify, and is preserved exactly as typed.
	FormExternal
	// FormNumber is "#N": the record number every display prints beside an
	// event. It is resolved.
	FormNumber
	// FormHash is a lowercase hexadecimal prefix or suffix of an event hash.
	// It is resolved.
	FormHash
)

// Room is the identity of one workroom: enough to classify a typed string
// without reading the log. Classification is cheap on purpose, so a boundary
// given nothing but canonical identifiers never pays for a fold.
type Room struct {
	Genesis      string
	ObjectFormat string
}

// hashWidth is how many hexadecimal characters name one object here.
func (r Room) hashWidth() int {
	if r.ObjectFormat == "sha256" {
		return 64
	}
	return 40
}

// prefix is the workroom half of every canonical identifier of this room,
// including the separator.
func (r Room) prefix() string {
	return "git:" + r.ObjectFormat + ":" + r.Genesis + "#"
}

// GenesisEvent is the canonical identifier of this workroom's founding commit.
//
// It is the root of the sequence, so it is in the log the sequencer resolves
// citations against and a citation of it is admitted. It is not an application
// record, so the fold projects no decision for it, it carries no record
// number, and no short reference resolves to it. Citing it is the ordinary way
// to rest on the founding of a workroom, and a boundary that called it
// unresolved would be warning about the one basis every new workroom starts
// from.
func (r Room) GenesisEvent() string {
	return r.prefix() + "git:" + r.ObjectFormat + ":" + r.Genesis
}

// Form classifies one typed string. It reads no log and allocates nothing.
func (r Room) Form(selector string) Form {
	switch {
	case selector == "":
		return FormOpaque
	case strings.HasPrefix(selector, "#"):
		// Nothing else in this vocabulary begins with a number sign, so the
		// intent is unmistakable even when what follows is not a number. A
		// malformed one is refused rather than carried through as prose,
		// because carrying it through would sign a citation naming nothing.
		return FormNumber
	case gitseqhost.ValidEventID(selector):
		if strings.HasPrefix(selector, r.prefix()) {
			return FormCanonical
		}
		return FormExternal
	case r.hashFragment(selector):
		return FormHash
	}
	return FormOpaque
}

// hashFragment reports whether this string could be part of an event hash
// here: lowercase hexadecimal, no longer than one object name in this
// repository's format. The length bound is what keeps an actor fingerprint out
// of the search in a sha1 workroom, where a fingerprint is 64 characters and
// no object name is.
func (r Room) hashFragment(selector string) bool {
	if len(selector) == 0 || len(selector) > r.hashWidth() {
		return false
	}
	for index := 0; index < len(selector); index++ {
		character := selector[index]
		hex := (character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')
		if !hex {
			return false
		}
	}
	return true
}

// Event is one durable record of this workroom, as a display names it.
type Event struct {
	// Sequence is the record number the displays print: the founding seed is
	// #1. It is the fold's own position, not a second name the log accepts.
	Sequence int
	// ID is the full canonical identifier, and the only thing ever signed.
	ID string
	// hash is the event half of ID, which is what a fragment is matched
	// against.
	hash string
}

// Membership answers the one question every citation disclosure asks: does
// this identifier name an event this workroom holds. It is deliberately
// separable from Set, because a surface that only reports on citations needs
// no room identity and should pay for none.
//
// Decisions, not statements. There is exactly one decision per durable record,
// while statements hold only utterances — so ratify and supersede are events
// with no statement, and the fold explicitly allows superseding a
// supersession. Searching statements would call those citations unresolved: a
// check written to catch fabricated identifiers, reporting real ones as
// fabricated, which is worse than not checking at all because it teaches
// readers to ignore it.
type Membership struct {
	held map[string]bool
}

// NewMembership indexes the events one verified projection holds.
func NewMembership(projection workroom.Projection) Membership {
	held := make(map[string]bool, len(projection.Decisions))
	for _, decision := range projection.Decisions {
		held[decision.Event] = true
	}
	return Membership{held: held}
}

// Has reports whether this identifier names an event this workroom holds.
func (m Membership) Has(id string) bool { return m.held[id] }

// Unresolved names the citations this workroom holds no event for, one entry
// per citation, in citation order.
//
// It reports the act's rests_on as it was written, with nothing collapsed and
// nothing dropped: a citation repeated twice appears twice, and an empty
// string is a citation naming no event like any other. This is a projection
// note about one signed act, and a reader comparing it against the act's own
// rests_on must find the same list. Deduplicating belongs to the sentences a
// person reads, not to the list a program does.
func (m Membership) Unresolved(restsOn []string) []string {
	var out []string
	for _, basis := range restsOn {
		if !m.Has(basis) {
			out = append(out, basis)
		}
	}
	return out
}

// Set is the verified durable event set of one workroom: everything a
// selector may name, and nothing else.
type Set struct {
	Room
	Membership
	events     []Event
	bySequence map[int]Event
	highest    int
}

// FromProjection builds the searchable set from a verified projection. It
// searches the same decisions Membership indexes, and for the same reason: the
// displays number every record, so a set built from statements alone would
// deny a number a reader can see.
func FromProjection(room Room, projection workroom.Projection) Set {
	set := Set{
		Room:       room,
		Membership: NewMembership(projection),
		events:     make([]Event, 0, len(projection.Decisions)),
		bySequence: make(map[int]Event, len(projection.Decisions)),
	}
	prefix := room.prefix()
	for _, decision := range projection.Decisions {
		event := Event{Sequence: decision.Sequence, ID: decision.Event}
		event.hash = strings.TrimPrefix(strings.TrimPrefix(decision.Event, prefix), "git:"+room.ObjectFormat+":")
		set.events = append(set.events, event)
		set.bySequence[event.Sequence] = event
		if event.Sequence > set.highest {
			set.highest = event.Sequence
		}
	}
	return set
}

// Resolvable reports whether the sequencer will resolve this citation here:
// an event of this workroom, or its genesis. That is the kernel's question,
// and Membership.Has is the fold's — the two differ by exactly the genesis,
// and they are kept apart because the kernel is what refuses. Warning about a
// citation nothing refuses would be noise on the ordinary path.
func (s Set) Resolvable(id string) bool {
	return s.Has(id) || id == s.GenesisEvent()
}

// Resolve turns one typed reference into the canonical identifier to sign.
// Anything that is not a number or a hash fragment comes back exactly as it
// was typed, including a canonical identifier of another workroom.
func (s Set) Resolve(selector string) (string, error) {
	switch s.Form(selector) {
	case FormNumber:
		return s.resolveNumber(selector)
	case FormHash:
		return s.resolveHash(selector)
	default:
		return selector, nil
	}
}

func (s Set) resolveNumber(selector string) (string, error) {
	digits := strings.TrimPrefix(selector, "#")
	number, err := strconv.Atoi(digits)
	// The round trip rejects "#01", "#+1" and "# 1": a display prints exactly
	// one spelling of each number, and accepting others would let two strings
	// mean one record.
	if err != nil || digits != strconv.Itoa(number) || number < 1 {
		return "", &Refusal{Selector: selector,
			Detail: fmt.Sprintf("is not a record number; records are numbered from #1 to #%d", s.highest)}
	}
	event, found := s.bySequence[number]
	if !found {
		return "", &Refusal{Selector: selector,
			Detail: fmt.Sprintf("names no record in this workroom; records are numbered from #1 to #%d", s.highest)}
	}
	return event.ID, nil
}

func (s Set) resolveHash(selector string) (string, error) {
	var matched []Event
	for _, event := range s.events {
		// One event, one entry. A fragment that is both a prefix and a suffix
		// of the same hash names that event once; counting it twice would
		// report a single event as an ambiguity.
		if strings.HasPrefix(event.hash, selector) || strings.HasSuffix(event.hash, selector) {
			matched = append(matched, event)
		}
	}
	switch len(matched) {
	case 1:
		return matched[0].ID, nil
	case 0:
		return "", &Refusal{Selector: selector,
			Detail: "matches no event hash in this workroom; a hash reference is a prefix or suffix of one event hash, and only this room's durable events are searched"}
	}
	sort.Slice(matched, func(left, right int) bool { return matched[left].Sequence < matched[right].Sequence })
	refusal := &Refusal{Selector: selector,
		Detail:     fmt.Sprintf("matches %d events here; name one exactly", len(matched)),
		Candidates: matched}
	if len(refusal.Candidates) > MaxCandidates {
		refusal.Omitted = len(refusal.Candidates) - MaxCandidates
		refusal.Candidates = refusal.Candidates[:MaxCandidates]
	}
	return "", refusal
}

// Refusal is what a boundary says when a typed reference names no event, or
// more than one. Nothing is appended: the caller is asked to name the event
// they meant. This is deliberately not the fold's silence-admits contract,
// which governs signed citations to absent events and not what a person types.
type Refusal struct {
	Selector   string
	Detail     string
	Candidates []Event
	Omitted    int
}

func (r *Refusal) Error() string {
	var out strings.Builder
	fmt.Fprintf(&out, "event reference %q %s", r.Selector, r.Detail)
	for _, candidate := range r.Candidates {
		fmt.Fprintf(&out, "\n  #%d %s", candidate.Sequence, candidate.ID)
	}
	if r.Omitted > 0 {
		fmt.Fprintf(&out, "\n  and %d more", r.Omitted)
	}
	if len(r.Candidates) == 0 {
		out.WriteString("\n  copy the number or the identifier a display shows, or the full identifier from `gs work --json`")
	}
	return out.String()
}
