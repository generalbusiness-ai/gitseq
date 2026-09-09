package main

import (
	"context"
	"strings"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/eventref"
)

// eventReferenceInputs is the whole inventory of tool inputs that carry a
// durable event reference, in one place so a new tool cannot quietly acquire
// one without joining it.
//
// What is deliberately absent is as much of the contract as what is present.
// `merge_plan`'s candidate and an artifact's body commit name ordinary Git
// commits; `work`'s target_ref names a branch; `say` and `ack` carry ephemeral
// handles that grant nothing and are not durable; a request body addresses a
// performer by name or fingerprint. None of those is an event, and reading one
// as an event is exactly the reinterpretation this boundary must not make.
var eventReferenceInputs = map[string]struct {
	Single []string
	Lists  []string
	Bases  []string
	// Bodies names arguments carrying a body map whose recognized
	// event-reference fields, and only those, are resolved. Which fields
	// those are is `eventref.EventBodyFields`, derived from the consumers
	// that read them.
	Bodies []string
}{
	"inspect":               {Single: []string{"event"}},
	"merge_plan":            {Single: []string{"approval"}},
	"state":                 {Lists: []string{"rests_on"}, Bases: []string{"rests_on"}, Bodies: []string{"body"}},
	"review":                {Single: []string{"promise", "self_initiated"}, Lists: []string{"artifacts", "ack_head_news", "implementations"}},
	"ratify":                {Single: []string{"target"}},
	"supersede":             {Single: []string{"target"}, Lists: []string{"rests_on"}, Bases: []string{"rests_on"}},
	"reassign_if_unclaimed": {Single: []string{"old_request"}, Lists: []string{"rests_on"}, Bases: []string{"rests_on"}, Bodies: []string{"body"}},
}

// newResolver returns the one resolver every event reference of one tool call
// is answered from. It reads the verified event set at most once, and only
// when something actually needs it.
func newResolver(ctx context.Context, workspace *app.Workspace) *eventref.Resolver {
	view := workspace.View()
	room := eventref.Room{Genesis: view.Genesis, ObjectFormat: view.ObjectFormat}
	return eventref.New(room, func() (eventref.Set, error) {
		snapshot, err := workspace.Snapshot(ctx)
		if err != nil {
			return eventref.Set{}, err
		}
		return eventref.FromProjection(room, snapshot.Projection), nil
	})
}

// resolveToolReferences rewrites, in the call's own arguments, every event
// reference this tool carries, and returns what the caller needs to be told
// about the citations of an act that has yet to be signed. Both happen before
// the tool runs, so a refusal costs the caller a message and appends nothing,
// and a mis-resolution is visible in the same result as the act it produced.
func resolveToolReferences(resolver *eventref.Resolver, call *toolCall) ([]string, error) {
	inputs, carried := eventReferenceInputs[call.Name]
	if !carried {
		return nil, nil
	}
	for _, name := range inputs.Single {
		value := stringValue(call.Arguments[name])
		if value == "" {
			continue
		}
		resolved, err := resolver.One(value)
		if err != nil {
			return nil, err
		}
		call.Arguments[name] = resolved
	}
	for _, name := range inputs.Lists {
		values := stringSlice(call.Arguments[name])
		if len(values) == 0 {
			continue
		}
		resolved, err := resolver.Many(values)
		if err != nil {
			return nil, err
		}
		call.Arguments[name] = toAny(resolved)
	}
	for _, name := range inputs.Bodies {
		body := stringMap(call.Arguments[name])
		if len(body) == 0 {
			continue
		}
		if err := resolver.Body(body); err != nil {
			return nil, err
		}
		rewritten := make(map[string]any, len(body))
		for key, value := range body {
			rewritten[key] = value
		}
		call.Arguments[name] = rewritten
	}
	var notes []string
	for _, name := range inputs.Bases {
		bases := stringSlice(call.Arguments[name])
		if len(bases) == 0 {
			continue
		}
		set, err := resolver.Set()
		if err != nil {
			// A workroom this process cannot read right now is skipped rather
			// than refused, for the same reason admission skips it: the
			// judgement that gates the append is made at sequencing.
			continue
		}
		notes = append(notes, eventref.BasisNotes(set, bases)...)
	}
	return notes, nil
}

// annotateReferences puts the boundary's own two sentences into the tool
// result: what it resolved before signing, and what it has to say about this
// act's citations. They are the same sentences `gs` writes to standard error,
// so an agent reading a result and a person reading a terminal are told the
// same thing in the same words.
//
// Only a tool answering with a JSON object is annotated. The paged read
// answers are typed projections whose shape is a wire contract, and the
// resolution they made is already visible in the record they returned.
func annotateReferences(resolver *eventref.Resolver, notes []string, value any) any {
	result, ok := value.(map[string]any)
	if !ok {
		return value
	}
	if lines := resolver.Lines(); len(lines) > 0 {
		result["resolved"] = lines
	}
	if len(notes) > 0 {
		result["basis_notes"] = notes
	}
	return result
}

// referenceDisclosure carries, on a failed tool call, what the boundary had
// already worked out before the call failed: the references it resolved, and
// what it has to say about this act's citations.
//
// A refusal is exactly when an author most needs those sentences. The one that
// says a citation names no event in this workroom is the pre-signing half of
// the very refusal the sequencer then returns, and delivering the refusal
// without it leaves the author to decode a kernel error they were about to be
// warned about. The error itself is unchanged and still wraps: nothing landed,
// and callers matching on the underlying reason keep matching.
type referenceDisclosure struct {
	err   error
	lines []string
}

func (d *referenceDisclosure) Error() string {
	return strings.Join(append([]string{d.err.Error()}, d.lines...), "\n")
}

func (d *referenceDisclosure) Unwrap() error { return d.err }

// disclosed are the sentences to show beside a failure, so the transport can
// render them as their own content rather than as one run-on message.
func (d *referenceDisclosure) disclosed() []string { return d.lines }

// withDisclosure attaches the boundary's sentences to a failed call, and
// returns the error untouched when there are none to attach.
func withDisclosure(err error, resolved, notes []string) error {
	lines := append(append([]string(nil), resolved...), notes...)
	if err == nil || len(lines) == 0 {
		return err
	}
	return &referenceDisclosure{err: err, lines: lines}
}

// toAny restores the loosely typed argument shape the tool arguments carry, so
// a resolved list reads back through stringSlice exactly as the caller's did.
func toAny(values []string) []any {
	out := make([]any, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}
