package eventref

import "fmt"

// Resolver rewrites the event references of one act against one verified event
// set, and remembers what it rewrote so the boundary can show the caller the
// identifiers it is about to sign.
//
// One resolver serves one act. Every reference of that act is answered from a
// single set, so a frontier that moves between two references cannot make the
// same act cite two different worlds. The set is read at most once, and never
// before something asks for it: a read command handed a canonical identifier
// resolves nothing and reads nothing, which is what keeps the
// resident-answered reads as cheap as they were. A filing verb is the other
// case — it asks for the set to describe its citations, whatever form they
// were typed in — so there the read is paid once and not avoided.
type Resolver struct {
	room   Room
	load   func() (Set, error)
	set    *Set
	failed error
	lines  []string
	said   map[string]bool
}

// New returns a resolver for one act. load reads the verified event set of the
// selected workroom, and is called at most once.
func New(room Room, load func() (Set, error)) *Resolver {
	return &Resolver{room: room, load: load, said: make(map[string]bool)}
}

// Set forces the verified event set to be read. Disclosure needs it even when
// every reference was already canonical.
func (r *Resolver) Set() (Set, error) {
	if r.set == nil && r.failed == nil {
		set, err := r.load()
		if err != nil {
			r.failed = err
		} else {
			r.set = &set
		}
	}
	return r.setOrZero(), r.failed
}

func (r *Resolver) setOrZero() Set {
	if r.set == nil {
		return Set{Room: r.room}
	}
	return *r.set
}

// One resolves a single reference. A reference that needs no resolution comes
// back unchanged without the log being read.
func (r *Resolver) One(selector string) (string, error) {
	switch r.room.Form(selector) {
	case FormNumber, FormHash:
	default:
		return selector, nil
	}
	set, err := r.Set()
	if err != nil {
		return "", fmt.Errorf("read the durable event set to resolve %q: %w", selector, err)
	}
	resolved, err := set.Resolve(selector)
	if err != nil {
		return "", err
	}
	// One sentence per distinct selector. An act citing the same reference
	// twice says so once, and one set always answers a selector the same way.
	if !r.said[selector] {
		r.said[selector] = true
		r.lines = append(r.lines, fmt.Sprintf("resolved %s -> %s", selector, resolved))
	}
	return resolved, nil
}

// Many resolves a list in order, keeping every entry in place. The first
// refusal stops the act.
func (r *Resolver) Many(selectors []string) ([]string, error) {
	if len(selectors) == 0 {
		return selectors, nil
	}
	resolved := make([]string, 0, len(selectors))
	for _, selector := range selectors {
		one, err := r.One(selector)
		if err != nil {
			return nil, err
		}
		resolved = append(resolved, one)
	}
	return resolved, nil
}

// EventBodyFields names every body field a consumer in this repository reads
// as exactly one durable event identifier. A field is here because something
// resolves its value against the log, and the list was derived from those
// consumers rather than from what the field names look like:
//
//	artifact                         the artifact a request names, and the one
//	                                 an approved report must rest on
//	                                 (internal/app, internal/statusview)
//	authorizes_request               the held landing request a release lifts
//	authorizes_approval              the approval that release covers
//	                                 (internal/workroom/landing.go)
//	merge_approval                   the approval a merge receipt was sealed on
//	merge_authorization              the release or authorization it carried
//	merge_authorization_ratification the ratification of that authorization
//	                                 (internal/workroom/fold.go, cmd/gs/succession.go)
//
// What is deliberately absent is the other half of the contract.
// authorizes_candidate, merge_candidate, merge_head and merge_target_pre_head
// name ordinary Git commits. merge_target_repo, merge_target_ref, target_ref,
// target_repo and target_head name repositories, refs and heads. to,
// hold_owner and actor name principals. merge_retirements, merge_successors,
// merge_changed_paths and merge_left_live are JSON documents rather than one
// identifier, and are composed by the merge path from values already canonical.
// admitted_by is written by the GitHub connector and read back by nothing.
// basis, in a kind definition, constrains kinds and not events. Every other
// body key is application prose this boundary must not reinterpret.
var EventBodyFields = []string{
	"artifact",
	"authorizes_request",
	"authorizes_approval",
	"merge_approval",
	"merge_authorization",
	"merge_authorization_ratification",
}

// Body rewrites the recognized event-reference fields of one act's body, in
// place, against the same set every other reference of that act is answered
// from. A field the caller did not set is untouched, and a field outside the
// list is carried exactly as typed, because the fold reads it as prose and so
// must this.
//
// The fold is unchanged by any of it: a body reaching the log still carries
// the full canonical identifier, and a value naming no event, or more than
// one, refuses here rather than being signed.
func (r *Resolver) Body(body map[string]string) error {
	if len(body) == 0 {
		return nil
	}
	for _, field := range EventBodyFields {
		value, set := body[field]
		if !set || value == "" {
			continue
		}
		resolved, err := r.One(value)
		if err != nil {
			return fmt.Errorf("body.%s: %w", field, err)
		}
		body[field] = resolved
	}
	return nil
}

// Lines are the resolutions this act made, in the order they were made, one
// sentence each. A boundary shows them before it signs: the command line puts
// them on standard error so standard output keeps carrying one identifier and
// nothing else, and the MCP adapter returns the same sentences in its result.
func (r *Resolver) Lines() []string { return r.lines }

// BasisNotes says what an author needs to know about the citations of an act
// that is about to be signed. It describes; it refuses nothing and grants
// nothing, and the fold's own rules are unchanged by it.
//
// Three cases are worth a sentence, and they are deliberately distinct. A
// string that is no identifier at all connects the act to nothing. An
// identifier of this workroom naming no event here is a claim about a position
// in this log, which the sequencer refuses outright — saying so before signing
// turns a refusal a caller has to decode into one they were warned about. An
// identifier of another workroom claims nothing about this log, is admitted,
// and is worth naming precisely because it looks like the second case and is
// not.
//
// A basis that is live and present here earns no sentence: a disclosure that
// speaks on the ordinary path is one readers learn to skip. The workroom's own
// genesis is present in exactly that sense — the sequencer resolves it, though
// the fold projects no record for it — so it is quiet too.
func BasisNotes(set Set, restsOn []string) []string {
	var notes []string
	said := make(map[string]bool, len(restsOn))
	for _, basis := range restsOn {
		if basis == "" || said[basis] {
			continue
		}
		said[basis] = true
		switch set.Form(basis) {
		case FormOpaque:
			notes = append(notes, fmt.Sprintf("warning: rests-on %q is not an event identifier; nothing here resolves it, so this act will rest on nothing that can ever flare it", basis))
		case FormExternal:
			notes = append(notes, fmt.Sprintf("note: rests-on %s is another workroom's event; this room cannot verify it and admits it as an external citation", basis))
		case FormCanonical:
			if !set.Resolvable(basis) {
				notes = append(notes, fmt.Sprintf("warning: rests-on %s names no event in this workroom; a citation claiming a position in this log that does not name one is refused before it is sequenced", basis))
			}
		}
	}
	return notes
}
