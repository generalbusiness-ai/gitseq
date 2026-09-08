package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// referenceRoomDepth is above the sixteen hexadecimal digits, so two event
// hashes must share a first character and the ambiguity control is a fact
// about the room rather than a coincidence of one run's hashes.
const referenceRoomDepth = 20

// referenceRoom is one workroom with enough durable records in it to decide
// every property of short-reference resolution, driven through the real tool
// dispatcher.
type referenceRoom struct {
	t         *testing.T
	server    *mcpServer
	workspace *app.Workspace
}

func newReferenceRoom(t *testing.T, name string) *referenceRoom {
	t.Helper()
	workspace, _ := templateAtDepth(referenceRoomDepth).copy(t, name)
	server, _ := attachedServer(t, workspace, "human", "", nil)
	return &referenceRoom{t: t, server: server, workspace: workspace}
}

func (r *referenceRoom) call(arguments map[string]any, name string) (map[string]any, error) {
	r.t.Helper()
	value, _, err := r.server.call(context.Background(), toolCall{Name: name, Arguments: arguments})
	if err != nil {
		return nil, err
	}
	result, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("tool %s answered %T, not an object", name, value)
	}
	return result, nil
}

func (r *referenceRoom) snapshot() app.Snapshot {
	r.t.Helper()
	snapshot, err := r.workspace.Snapshot(context.Background())
	if err != nil {
		r.t.Fatal(err)
	}
	return snapshot
}

func (r *referenceRoom) events() (ids []string, hashes []string) {
	r.t.Helper()
	view := r.workspace.View()
	prefix := "git:" + view.ObjectFormat + ":" + view.Genesis + "#git:" + view.ObjectFormat + ":"
	for _, decision := range r.snapshot().Projection.Decisions {
		ids = append(ids, decision.Event)
		hashes = append(hashes, strings.TrimPrefix(decision.Event, prefix))
	}
	return ids, hashes
}

func (r *referenceRoom) depth() int { return r.snapshot().Depth }

// signedBody reads one landed statement's body out of the projection, which
// decodes it from the signed record.
func (r *referenceRoom) signedBody(event string) map[string]string {
	r.t.Helper()
	for _, statement := range r.snapshot().Projection.Statements {
		if statement.Event == event {
			return statement.Body
		}
	}
	r.t.Fatalf("no signed statement for %s", event)
	return nil
}

func (r *referenceRoom) signedBases(event string) []string {
	r.t.Helper()
	bases, held := r.snapshot().Projection.Provenance[event]
	if !held {
		r.t.Fatalf("no signed record for %s", event)
	}
	return bases
}

// recordOf digs the new event's identifier out of a tool result the same way
// the adapter's own disclosure does.
func recordOf(t *testing.T, result map[string]any) string {
	t.Helper()
	record, ok := result["record"].(workroom.Record)
	if !ok {
		t.Fatalf("result carries no durable record: %#v", result)
	}
	return record.ID
}

func lines(t *testing.T, result map[string]any, key string) []string {
	t.Helper()
	value, present := result[key]
	if !present {
		return nil
	}
	got, ok := value.([]string)
	if !ok {
		t.Fatalf("%s is %T, not a list of sentences", key, value)
	}
	return got
}

// shortestUniquePrefix is the shortest prefix of one event hash that no other
// event hash answers to, so the happy path is exercised with a genuinely short
// reference.
func shortestUniquePrefix(hashes []string, index int) string {
	for width := 1; width <= len(hashes[index]); width++ {
		candidate := hashes[index][:width]
		matches := 0
		for _, hash := range hashes {
			if strings.HasPrefix(hash, candidate) || strings.HasSuffix(hash, candidate) {
				matches++
			}
		}
		if matches == 1 {
			return candidate
		}
	}
	return hashes[index]
}

func firstSharedPrefix(t *testing.T, hashes []string) string {
	t.Helper()
	counts := make(map[string]int)
	for _, hash := range hashes {
		counts[hash[:1]]++
	}
	for character, count := range counts {
		if count > 1 {
			return character
		}
	}
	t.Fatal("no two event hashes share a first character")
	return ""
}

// Every tool input that carries an event reference takes what a display shows,
// and what lands is the full canonical identifier read out of the signed
// record. The result says what it resolved, so an agent can see the identifier
// its act is about before it acts again.
func TestToolsResolveWhatTheDisplaysShow(t *testing.T) {
	parallelTest(t)
	room := newReferenceRoom(t, "resolve")
	ids, hashes := room.events()

	result, err := room.call(map[string]any{
		"kind": "assert", "text": "cites by number and by hash",
		"rests_on":        []any{"#2", shortestUniquePrefix(hashes, 4), hashes[6][len(hashes[6])-8:]},
		"idempotency_key": "mcp-short-references",
	}, "state")
	if err != nil {
		t.Fatal(err)
	}
	event := recordOf(t, result)
	want := []string{ids[1], ids[4], ids[6]}
	if got := room.signedBases(event); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("signed rests_on = %v, want %v", got, want)
	}
	resolved := lines(t, result, "resolved")
	if len(resolved) != 3 || resolved[0] != "resolved #2 -> "+ids[1] {
		t.Fatalf("resolved = %v", resolved)
	}

	// A target is an event reference too, and the ratification signs the
	// canonical identifier its own payload then carries.
	proposal, err := room.call(map[string]any{
		"kind": "propose", "text": "a proposal to ratify",
		"rests_on": []any{ids[0]}, "idempotency_key": "mcp-proposal",
	}, "state")
	if err != nil {
		t.Fatal(err)
	}
	proposed := recordOf(t, proposal)
	number := 0
	for _, decision := range room.snapshot().Projection.Decisions {
		if decision.Event == proposed {
			number = decision.Sequence
		}
	}
	ratified, err := room.call(map[string]any{
		"target": fmt.Sprintf("#%d", number), "idempotency_key": "mcp-ratify-by-number",
	}, "ratify")
	if err != nil {
		t.Fatal(err)
	}
	target := ""
	for _, act := range room.snapshot().Projection.Acts {
		if act.Event == recordOf(t, ratified) {
			target = act.Target
		}
	}
	if target != proposed {
		t.Fatalf("signed target = %q, want %q", target, proposed)
	}

	// A read selector resolves the same way, and the paged answer keeps the
	// typed shape its wire contract promises.
	inspection, _, err := room.server.call(context.Background(), toolCall{Name: "inspect", Arguments: map[string]any{"event": "#3"}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(inspection)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), ids[2]) {
		t.Fatalf("inspect by number did not answer about %s: %s", ids[2], encoded)
	}
}

// The adapter resolves the same recognized body fields the command line does,
// and what lands is decoded from the signed statement's own body.
func TestStateToolResolvesRecognizedBodyFields(t *testing.T) {
	parallelTest(t)
	room := newReferenceRoom(t, "body")
	ids, hashes := room.events()

	result, err := room.call(map[string]any{
		"kind": "assert", "text": "a body naming events",
		"rests_on": []any{ids[0]},
		"body": map[string]any{
			"authorizes_request":   "#3",
			"authorizes_approval":  shortestUniquePrefix(hashes, 5),
			"artifact":             ids[7],
			"authorizes_candidate": strings.Repeat("c", 40),
			"note":                 "#3 is where this began",
		},
		"idempotency_key": "mcp-body-fields",
	}, "state")
	if err != nil {
		t.Fatal(err)
	}
	body := room.signedBody(recordOf(t, result))
	for field, want := range map[string]string{
		"authorizes_request":   ids[2],
		"authorizes_approval":  ids[5],
		"artifact":             ids[7],
		"authorizes_candidate": strings.Repeat("c", 40),
		"note":                 "#3 is where this began",
	} {
		if body[field] != want {
			t.Errorf("signed body.%s = %q, want %q", field, body[field], want)
		}
	}

	before := room.depth()
	if _, err := room.call(map[string]any{
		"kind": "assert", "text": "refused body", "rests_on": []any{ids[0]},
		"body":            map[string]any{"authorizes_request": "#0"},
		"idempotency_key": "mcp-body-refused",
	}, "state"); err == nil {
		t.Fatal("a body field naming no record was admitted")
	} else if !strings.Contains(err.Error(), "body.authorizes_request") {
		t.Fatalf("the refusal does not name the field: %v", err)
	}
	if after := room.depth(); after != before {
		t.Fatalf("depth %d -> %d", before, after)
	}
}

// Every refusal costs the caller a message and appends nothing.
func TestReferenceRefusalsAppendNothing(t *testing.T) {
	parallelTest(t)
	room := newReferenceRoom(t, "refuse")
	_, hashes := room.events()
	elsewhere := newReferenceRoom(t, "elsewhere")
	foreignIDs, _ := elsewhere.events()

	for name, selector := range map[string]string{
		"zero":           "#0",
		"beyond the end": fmt.Sprintf("#%d", room.depth()+1),
		"non numeric":    "#x",
		"ambiguous":      firstSharedPrefix(t, hashes),
		"no match":       "beefbeefbeef",
	} {
		t.Run(name, func(t *testing.T) {
			before := room.depth()
			_, err := room.call(map[string]any{
				"kind": "assert", "text": "must not land", "rests_on": []any{selector},
				"idempotency_key": "mcp-refused-" + strings.ReplaceAll(name, " ", "-"),
			}, "state")
			if err == nil {
				t.Fatal("the act was admitted")
			}
			if !strings.Contains(err.Error(), fmt.Sprintf("event reference %q", selector)) {
				t.Fatalf("refusal does not name what was typed: %v", err)
			}
			if after := room.depth(); after != before {
				t.Fatalf("depth %d -> %d", before, after)
			}
		})
	}

	// Another workroom's event is reachable only by its canonical identifier,
	// which is preserved exactly as typed in the signed payload.
	result, err := room.call(map[string]any{
		"kind": "assert", "text": "explicit cross-workroom citation",
		"rests_on": []any{foreignIDs[3]}, "idempotency_key": "mcp-foreign",
	}, "state")
	if err != nil {
		t.Fatal(err)
	}
	if got := room.signedBases(recordOf(t, result)); len(got) != 1 || got[0] != foreignIDs[3] {
		t.Fatalf("signed rests_on = %v, want [%s]", got, foreignIDs[3])
	}
	if got := lines(t, result, "resolved"); len(got) != 0 {
		t.Fatalf("a canonical identifier was reported as resolved: %v", got)
	}
}

// The three sentences an author is owed before their act is signed, the
// silence they are owed on the ordinary path, and the post-submit disclosure
// this adapter has always made, unchanged.
func TestStateToolDisclosesItsBases(t *testing.T) {
	parallelTest(t)
	room := newReferenceRoom(t, "disclose")
	ids, _ := room.events()
	view := room.workspace.View()
	absent := "git:" + view.ObjectFormat + ":" + view.Genesis + "#git:" + view.ObjectFormat + ":" + strings.Repeat("f", len(view.Genesis))
	foreign := "git:" + view.ObjectFormat + ":" + strings.Repeat("a", len(view.Genesis)) + "#git:" + view.ObjectFormat + ":" + strings.Repeat("b", len(view.Genesis))

	quiet, err := room.call(map[string]any{
		"kind": "assert", "text": "ordinary", "rests_on": []any{ids[1]}, "idempotency_key": "mcp-quiet",
	}, "state")
	if err != nil {
		t.Fatal(err)
	}
	if got := lines(t, quiet, "basis_notes"); len(got) != 0 {
		t.Fatalf("an ordinary filing said something: %v", got)
	}

	malformed, err := room.call(map[string]any{
		"kind": "assert", "text": "malformed", "rests_on": []any{"not-an-event"}, "idempotency_key": "mcp-malformed-basis",
	}, "state")
	if err != nil {
		t.Fatal(err)
	}
	notes := lines(t, malformed, "basis_notes")
	if len(notes) != 1 || !strings.HasPrefix(notes[0], `warning: rests-on "not-an-event" is not an event identifier`) {
		t.Fatalf("basis_notes = %v", notes)
	}
	// The disclosure this adapter has always made is unchanged beside it.
	projected, ok := malformed["projected"].(map[string]any)
	if !ok {
		t.Fatalf("no projection notes: %#v", malformed)
	}
	if unresolved, held := projected["unresolved_rests_on"].([]string); !held || len(unresolved) != 1 || unresolved[0] != "not-an-event" {
		t.Fatalf("unresolved_rests_on = %#v", projected["unresolved_rests_on"])
	}

	external, err := room.call(map[string]any{
		"kind": "assert", "text": "external", "rests_on": []any{foreign}, "idempotency_key": "mcp-external-basis",
	}, "state")
	if err != nil {
		t.Fatal(err)
	}
	notes = lines(t, external, "basis_notes")
	if len(notes) != 1 || !strings.HasPrefix(notes[0], "note: rests-on "+foreign+" is another workroom's event") {
		t.Fatalf("basis_notes = %v", notes)
	}
	if strings.Contains(notes[0], "names no event in this workroom") {
		t.Fatalf("an external citation was confused with an absent local one: %v", notes)
	}

	// A canonical identifier of this workroom naming no event is warned about
	// here and refused by the sequencer. The warning has to arrive *with* the
	// refusal: a refused call carries no structured content, so asserting only
	// the kernel error would let the disclosure vanish on exactly the path
	// that needs it most.
	before := room.depth()
	_, err = room.call(map[string]any{
		"kind": "assert", "text": "absent", "rests_on": []any{absent}, "idempotency_key": "mcp-absent-basis",
	}, "state")
	if err == nil {
		t.Fatal("an identifier of this workroom naming no event was admitted")
	}
	if !strings.Contains(err.Error(), "causal reference does not resolve in this log") {
		t.Fatalf("the sequencer's refusal was replaced: %v", err)
	}
	want := "warning: rests-on " + absent + " names no event in this workroom"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("the refusal dropped the disclosure:\n  got  %v\n  want it to carry %q", err, want)
	}
	// And the caller reads it on the actual failure response, as its own
	// content block beside the reason.
	failure := failedToolResponse(t, room.server, "state", map[string]any{
		"kind": "assert", "text": "absent again", "rests_on": []any{absent}, "idempotency_key": "mcp-absent-basis-response",
	})
	if failure["isError"] != true {
		t.Fatalf("the call did not fail: %#v", failure)
	}
	texts := blockTexts(t, failure)
	if len(texts) < 2 || !strings.Contains(texts[0], "causal reference does not resolve in this log") {
		t.Fatalf("the first block is not the refusal: %q", texts)
	}
	disclosed := false
	for _, text := range texts[1:] {
		if strings.Contains(text, want) {
			disclosed = true
		}
	}
	if !disclosed {
		t.Fatalf("the failure response carries no disclosure block: %q", texts)
	}
	if after := room.depth(); after != before {
		t.Fatalf("depth %d -> %d", before, after)
	}
}

// failedToolResponse drives the real JSON-RPC loop, so what a client actually
// receives on a refusal is asserted rather than reconstructed here. A helper
// that rebuilt the envelope itself would pass while the transport dropped
// every sentence.
func failedToolResponse(t *testing.T, server *mcpServer, name string, arguments map[string]any) map[string]any {
	t.Helper()
	params, err := json.Marshal(map[string]any{
		"_meta":     map[string]any{"io.modelcontextprotocol/protocolVersion": "2026-07-28", "io.modelcontextprotocol/clientCapabilities": map[string]any{}},
		"name":      name,
		"arguments": arguments,
	})
	if err != nil {
		t.Fatal(err)
	}
	frame, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": json.RawMessage(params)})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := server.run(context.Background(), strings.NewReader(string(frame)+"\n"), &output); err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatalf("decode %s: %v", output.String(), err)
	}
	result, ok := response["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result in %s", output.String())
	}
	return result
}

// blockTexts is every text block of a tool result, in order.
func blockTexts(t *testing.T, result map[string]any) []string {
	t.Helper()
	blocks, ok := result["content"].([]any)
	if !ok {
		t.Fatalf("no content blocks: %#v", result["content"])
	}
	var texts []string
	for _, block := range blocks {
		entry, ok := block.(map[string]any)
		if !ok {
			t.Fatalf("content block is not an object: %#v", block)
		}
		text, _ := entry["text"].(string)
		texts = append(texts, text)
	}
	return texts
}

// The command line and the adapter are one resolver over one event set. Two
// copies of the same signed history hold the same identifiers, so a selector
// that resolves differently in the two surfaces is visible as an inequality of
// what each one signed.
func TestCommandLineAndAdapterResolveTheSameReferences(t *testing.T) {
	parallelTest(t)
	command := newReferenceRoom(t, "cli")
	adapter := newReferenceRoom(t, "mcp")
	ids, hashes := command.events()
	if other, _ := adapter.events(); strings.Join(other, ",") != strings.Join(ids, ",") {
		t.Fatal("the two fixture rooms do not hold the same signed history")
	}

	binary := filepath.Join(t.TempDir(), "gs")
	if output, err := exec.Command("go", "build", "-o", binary, "../gs").CombinedOutput(); err != nil {
		t.Fatalf("building gs: %v: %s", err, output)
	}

	selectors := []string{"#5", shortestUniquePrefix(hashes, 7), hashes[9][len(hashes[9])-6:], ids[11]}
	arguments := []string{"state", "--repo", command.workspace.Repo, "--as", "human",
		"--kind", "assert", "--text", "parity", "--idempotency-key", "parity"}
	for _, selector := range selectors {
		arguments = append(arguments, "--rests-on", selector)
	}
	output, err := exec.Command(binary, arguments...).Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			t.Fatalf("gs state: %v: %s", err, exit.Stderr)
		}
		t.Fatal(err)
	}
	fromCommandLine := command.signedBases(strings.TrimSpace(string(output)))

	bases := make([]any, 0, len(selectors))
	for _, selector := range selectors {
		bases = append(bases, selector)
	}
	result, err := adapter.call(map[string]any{
		"kind": "assert", "text": "parity", "rests_on": bases, "idempotency_key": "parity",
	}, "state")
	if err != nil {
		t.Fatal(err)
	}
	fromAdapter := adapter.signedBases(recordOf(t, result))

	if strings.Join(fromCommandLine, ",") != strings.Join(fromAdapter, ",") {
		t.Fatalf("the two surfaces signed different citations:\n  gs  %v\n  mcp %v", fromCommandLine, fromAdapter)
	}
	want := []string{ids[4], ids[7], ids[9], ids[11]}
	if strings.Join(fromCommandLine, ",") != strings.Join(want, ",") {
		t.Fatalf("signed %v, want %v", fromCommandLine, want)
	}

	// The two surfaces also say the same thing about a basis, in the same
	// words: `gs` on standard error, the adapter in its result. Both rooms
	// hold the same history, so the sentences must match exactly.
	commandLine := exec.Command(binary, "state", "--repo", command.workspace.Repo, "--as", "human",
		"--kind", "assert", "--text", "agreement", "--idempotency-key", "agreement",
		"--rests-on", "not-an-event")
	var warned strings.Builder
	commandLine.Stderr = &warned
	if err := commandLine.Run(); err != nil {
		t.Fatalf("gs state: %v: %s", err, warned.String())
	}
	noted, err := adapter.call(map[string]any{
		"kind": "assert", "text": "agreement", "rests_on": []any{"not-an-event"},
		"idempotency_key": "agreement",
	}, "state")
	if err != nil {
		t.Fatal(err)
	}
	notes := lines(t, noted, "basis_notes")
	if len(notes) != 1 {
		t.Fatalf("basis_notes = %v", notes)
	}
	// Compared line by line, not stream against sentence. Standard error also
	// carries the resolutions this act made and, when a cold fold runs long,
	// the progress line that says so; none of that is the disclosure, and a
	// test that demanded the whole stream be one sentence would go red for a
	// slow machine rather than for a disagreement.
	if got := basisNoteLines(warned.String()); strings.Join(got, "\n") != strings.Join(notes, "\n") {
		t.Fatalf("the two surfaces disagree:\n  gs  %q\n  mcp %q\n  whole stream %q", got, notes, warned.String())
	}
}

// basisNoteLines picks the citation disclosure out of what `gs` wrote to
// standard error: the lines the command prefixes with its own name, less that
// prefix, and only the two shapes the disclosure emits.
func basisNoteLines(stderr string) []string {
	var notes []string
	for _, line := range strings.Split(stderr, "\n") {
		sentence, named := strings.CutPrefix(strings.TrimSpace(line), "gs: ")
		if !named {
			continue
		}
		if strings.HasPrefix(sentence, "warning: rests-on ") || strings.HasPrefix(sentence, "note: rests-on ") {
			notes = append(notes, sentence)
		}
	}
	return notes
}
