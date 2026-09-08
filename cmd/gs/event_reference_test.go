package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// refRoom is a workroom with enough durable records in it to make every
// property of short-reference resolution decidable rather than lucky, driven
// through the installed command rather than in process: which stream carried
// which half of the answer, and what exit status a refusal has, are part of
// what these tests are about, and neither survives calling the command
// function directly.
type refRoom struct {
	t         *testing.T
	binary    string
	repo      string
	workspace *app.Workspace
	seed      string
	format    string
}

// refRoomEvents is above the sixteen hexadecimal digits, so two event hashes
// must share a first character. That makes the ambiguity control a fact about
// the room rather than a coincidence of the hashes a run happened to produce.
const refRoomEvents = 20

func newRefRoom(t *testing.T, objectFormat string) *refRoom {
	t.Helper()
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	testGit(t, "", "init", "-q", "-b", "main", "--object-format="+objectFormat, repo)
	testGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", "ordinary seed")
	workspace, seed, err := app.Init(ctx, repo, "alice", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	room := &refRoom{t: t, binary: buildGS(t), repo: repo, workspace: workspace, seed: seed.ID, format: objectFormat}
	for index := 0; index < refRoomEvents; index++ {
		if _, err := workspace.Act(ctx, "alice", app.Act{
			Verb: app.VerbState, Kind: workroom.KindAssert, Text: fmt.Sprintf("orientation %d", index),
			RestsOn: []string{seed.ID}, IdempotencyKey: fmt.Sprintf("orientation-%d", index),
		}); err != nil {
			t.Fatal(err)
		}
	}
	return room
}

// run types one command at the installed binary and returns both streams and
// the exit status. The repository flag goes in front of everything else,
// because Go's flag parsing stops at the first positional argument and these
// commands take one.
func (r *refRoom) run(arguments ...string) (stdout, stderr string, code int) {
	r.t.Helper()
	full := append([]string{arguments[0], "--repo", r.repo}, arguments[1:]...)
	command := exec.Command(r.binary, full...)
	var out, stderrBuffer strings.Builder
	command.Stdout, command.Stderr = &out, &stderrBuffer
	if err := command.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			r.t.Fatalf("running gs %v: %v", arguments, err)
		}
		code = exit.ExitCode()
	}
	return out.String(), stderrBuffer.String(), code
}

func (r *refRoom) snapshot() app.Snapshot {
	r.t.Helper()
	snapshot, err := r.workspace.Snapshot(context.Background())
	if err != nil {
		r.t.Fatal(err)
	}
	return snapshot
}

// events lists every durable record as the displays name it: the number the
// fold assigned, the canonical identifier, and the event half of that
// identifier, which is what a hash reference is matched against.
func (r *refRoom) events() (ids []string, hashes []string) {
	r.t.Helper()
	prefix := "git:" + r.format + ":" + r.workspace.View().Genesis + "#git:" + r.format + ":"
	for _, decision := range r.snapshot().Projection.Decisions {
		ids = append(ids, decision.Event)
		hashes = append(hashes, strings.TrimPrefix(decision.Event, prefix))
	}
	return ids, hashes
}

func (r *refRoom) depth() int { return r.snapshot().Depth }

// signedBases reads what one landed event actually rests on, out of the signed
// record rather than out of anything the command printed.
func (r *refRoom) signedBases(event string) []string {
	r.t.Helper()
	bases, held := r.snapshot().Projection.Provenance[event]
	if !held {
		r.t.Fatalf("no signed record for %s", event)
	}
	return bases
}

// signedBody reads the body of one landed statement out of the projection,
// which decodes it from the signed record rather than from anything the
// command printed.
func (r *refRoom) signedBody(event string) map[string]string {
	r.t.Helper()
	for _, statement := range r.snapshot().Projection.Statements {
		if statement.Event == event {
			return statement.Body
		}
	}
	r.t.Fatalf("no signed statement for %s", event)
	return nil
}

// signedTarget reads the target out of the signed payload of a ratify or
// supersede record.
func (r *refRoom) signedTarget(event string) string {
	r.t.Helper()
	for _, act := range r.snapshot().Projection.Acts {
		if act.Event == event {
			return act.Target
		}
	}
	r.t.Fatalf("no signed act for %s", event)
	return ""
}

// uniquePrefix is the shortest prefix of one event hash that no other event
// hash shares, so the happy path is exercised with a genuinely short
// reference rather than a whole hash.
func uniquePrefix(hashes []string, index int) string {
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

// sharedPrefix is a one-character reference at least two event hashes answer
// to. With more events than hexadecimal digits, one always exists.
func sharedPrefix(t *testing.T, hashes []string) (string, int) {
	t.Helper()
	counts := make(map[string]int)
	for _, hash := range hashes {
		counts[hash[:1]]++
	}
	for character, count := range counts {
		if count > 1 {
			total := 0
			for _, hash := range hashes {
				if strings.HasPrefix(hash, character) || strings.HasSuffix(hash, character) {
					total++
				}
			}
			return character, total
		}
	}
	t.Fatal("no two event hashes share a first character")
	return "", 0
}

// The two things a display shows — the record number and the hash — are both
// accepted, in both object formats, and what lands is always the full
// canonical identifier read out of the signed record.
func TestShortEventReferencesResolveInBothObjectFormats(t *testing.T) {
	t.Parallel()
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			room := newRefRoom(t, format)
			// The target is named by its record number, which is stable, and
			// every selector is derived from the event set as it stands when
			// its own case runs. Each case appends, so a prefix computed once
			// for the whole table could be unique when the table was built and
			// ambiguous by the time a later case used it.
			const target = 3
			for _, testCase := range []struct {
				name string
				pick func(ids, hashes []string) string
			}{
				{"number", func([]string, []string) string { return fmt.Sprintf("#%d", target+1) }},
				{"short prefix", func(_, hashes []string) string { return uniquePrefix(hashes, target) }},
				{"suffix", func(_, hashes []string) string { return hashes[target][len(hashes[target])-8:] }},
				{"whole hash", func(_, hashes []string) string { return hashes[target] }},
				{"canonical", func(ids, _ []string) string { return ids[target] }},
			} {
				name := testCase.name
				t.Run(name, func(t *testing.T) {
					ids, hashes := room.events()
					selector := testCase.pick(ids, hashes)
					stdout, stderr, code := room.run("state", "--as", "alice", "--kind", "assert",
						"--text", "cites "+name, "--rests-on", selector, "--idempotency-key", "cite-"+format+"-"+name)
					if code != 0 {
						t.Fatalf("exit %d\n%s", code, stderr)
					}
					event := strings.TrimSpace(stdout)
					// Standard output carries exactly one identifier and
					// nothing else: every resolution and note is on standard
					// error, so a shell capturing the identifier is unchanged.
					if strings.Count(stdout, "\n") != 1 || !strings.HasPrefix(event, "git:"+format+":") {
						t.Fatalf("standard output is not one identifier: %q", stdout)
					}
					if got := room.signedBases(event); len(got) != 1 || got[0] != ids[target] {
						t.Fatalf("signed rests_on = %v, want [%s]", got, ids[target])
					}
					if name != "canonical" && !strings.Contains(stderr, "resolved "+selector+" -> "+ids[target]) {
						t.Fatalf("stderr does not name the resolution: %q", stderr)
					}
					if name == "canonical" && strings.Contains(stderr, "resolved") {
						t.Fatalf("a canonical identifier was reported as resolved: %q", stderr)
					}
				})
			}
		})
	}
}

// Both ends of the record numbering, and the two spellings that look like a
// number and are not. Each refusal leaves the log exactly where it was.
func TestRecordNumberBoundariesRefuseWithoutAppending(t *testing.T) {
	t.Parallel()
	room := newRefRoom(t, "sha1")
	ids, _ := room.events()
	depth := room.depth()

	for name, selector := range map[string]string{
		"first": "#1",
		"last":  fmt.Sprintf("#%d", depth),
	} {
		stdout, stderr, code := room.run("state", "--as", "alice", "--kind", "assert",
			"--text", "boundary "+name, "--rests-on", selector, "--idempotency-key", "boundary-"+name)
		if code != 0 {
			t.Fatalf("%s (%s) was refused: %s", name, selector, stderr)
		}
		want := ids[0]
		if name == "last" {
			want = ids[depth-1]
		}
		if got := room.signedBases(strings.TrimSpace(stdout)); len(got) != 1 || got[0] != want {
			t.Fatalf("%s signed %v, want [%s]", name, got, want)
		}
	}

	for name, selector := range map[string]string{
		"zero":        "#0",
		"beyond":      fmt.Sprintf("#%d", room.depth()+1),
		"non numeric": "#x",
		"bare":        "#",
		"padded":      "#01",
	} {
		before := room.depth()
		stdout, stderr, code := room.run("state", "--as", "alice", "--kind", "assert",
			"--text", "boundary "+name, "--rests-on", selector, "--idempotency-key", "refused-"+name)
		if code == 0 {
			t.Fatalf("%s (%s) was admitted: %s", name, selector, stdout)
		}
		if !strings.Contains(stderr, "event reference "+fmt.Sprintf("%q", selector)) {
			t.Fatalf("%s refusal does not name what was typed: %q", name, stderr)
		}
		if stdout != "" {
			t.Fatalf("%s wrote to standard output: %q", name, stdout)
		}
		if after := room.depth(); after != before {
			t.Fatalf("%s changed depth from %d to %d", name, before, after)
		}
	}
}

// A fragment more than one event answers to refuses, names the candidates the
// way a display names them, and appends nothing. Guessing here would put a
// citation nobody chose into an append-only log.
func TestAmbiguousHashReferenceRefusesAndNamesCandidates(t *testing.T) {
	t.Parallel()
	room := newRefRoom(t, "sha1")
	_, hashes := room.events()
	selector, matches := sharedPrefix(t, hashes)
	before := room.depth()
	stdout, stderr, code := room.run("state", "--as", "alice", "--kind", "assert",
		"--text", "ambiguous", "--rests-on", selector, "--idempotency-key", "ambiguous")
	if code == 0 {
		t.Fatalf("an ambiguous reference was admitted: %s", stdout)
	}
	if !strings.Contains(stderr, fmt.Sprintf("matches %d events here", matches)) {
		t.Fatalf("refusal does not say how many matched: %q", stderr)
	}
	named := 0
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			named++
		}
	}
	if named < 2 {
		t.Fatalf("refusal named %d candidates: %q", named, stderr)
	}
	if after := room.depth(); after != before {
		t.Fatalf("an ambiguous reference changed depth from %d to %d", before, after)
	}
}

// The searched population is the durable event set, never Git's object
// database. A hexadecimal fragment that names a blob in this very repository
// and no event resolves to nothing, and says so.
func TestHashOfAnUnrelatedGitObjectNamesNoEvent(t *testing.T) {
	t.Parallel()
	room := newRefRoom(t, "sha1")
	_, hashes := room.events()
	var blob, selector string
	for attempt := 0; attempt < 64 && selector == ""; attempt++ {
		candidate := strings.TrimSpace(testGitStdin(t, room.repo, fmt.Sprintf("unrelated content %d", attempt), "hash-object", "-w", "--stdin"))
		fragment := candidate[:8]
		matched := false
		for _, hash := range hashes {
			if strings.HasPrefix(hash, fragment) || strings.HasSuffix(hash, fragment) {
				matched = true
			}
		}
		if !matched {
			blob, selector = candidate, fragment
		}
	}
	if selector == "" {
		t.Fatal("could not write a blob whose prefix names no event")
	}
	if kind := strings.TrimSpace(testGit(t, room.repo, "cat-file", "-t", blob)); kind != "blob" {
		t.Fatalf("the control object is a %s, not a blob", kind)
	}
	before := room.depth()
	stdout, stderr, code := room.run("state", "--as", "alice", "--kind", "assert",
		"--text", "blob prefix", "--rests-on", selector, "--idempotency-key", "blob-prefix")
	if code == 0 {
		t.Fatalf("a blob prefix was admitted as an event: %s", stdout)
	}
	if !strings.Contains(stderr, "matches no event hash in this workroom") {
		t.Fatalf("refusal = %q", stderr)
	}
	if after := room.depth(); after != before {
		t.Fatalf("a refused blob prefix changed depth from %d to %d", before, after)
	}
}

// Numbers and hashes never cross a workroom boundary. Another room's event is
// reachable only by its canonical identifier, which is preserved exactly as it
// was typed, in the signed payload and not merely in the echo.
func TestAnotherWorkroomsEventIsNeverResolvedAndIsPreservedAsTyped(t *testing.T) {
	t.Parallel()
	here := newRefRoom(t, "sha1")
	elsewhere := newRefRoom(t, "sha1")
	localIDs, _ := here.events()
	foreignIDs, foreignHashes := elsewhere.events()
	foreign := foreignIDs[2]

	// A record number is in range in both rooms and means a different event in
	// each. It must name this room's record, not the other room's, and the
	// proof is the identifier that lands in the signed act. A number chosen
	// past the end of this room would prove only that it was out of range.
	stdout, stderr, code := here.run("state", "--as", "alice", "--kind", "assert",
		"--text", "number is read here", "--rests-on", "#3", "--idempotency-key", "cross-room-number")
	if code != 0 {
		t.Fatalf("#3 was refused in this room: %s", stderr)
	}
	if got := here.signedBases(strings.TrimSpace(stdout)); len(got) != 1 || got[0] != localIDs[2] {
		t.Fatalf("#3 signed %v, want this room's own [%s]", got, localIDs[2])
	}
	if localIDs[2] == foreignIDs[2] {
		t.Fatal("the two fixture rooms share an identifier, so this test proves nothing")
	}
	if strings.Contains(stderr, elsewhere.workspace.View().Genesis) {
		t.Fatalf("#3 named the other room: %q", stderr)
	}

	// A hash is the other short form, and it does not cross either: the other
	// room's event hash names nothing here.
	before := here.depth()
	stdout, stderr, code = here.run("state", "--as", "alice", "--kind", "assert",
		"--text", "foreign hash", "--rests-on", foreignHashes[2], "--idempotency-key", "cross-room-hash")
	if code == 0 {
		t.Fatalf("another room's event hash was admitted: %s", stdout)
	}
	if strings.Contains(stderr, foreign) {
		t.Fatalf("a foreign hash resolved into the other room: %q", stderr)
	}
	if after := here.depth(); after != before {
		t.Fatalf("a refused cross-room reference changed depth from %d to %d", before, after)
	}

	stdout, stderr, code = here.run("state", "--as", "alice", "--kind", "assert",
		"--text", "explicit cross-workroom citation", "--rests-on", foreign, "--idempotency-key", "foreign-canonical")
	if code != 0 {
		t.Fatalf("an explicit cross-workroom citation was refused: %s", stderr)
	}
	if got := here.signedBases(strings.TrimSpace(stdout)); len(got) != 1 || got[0] != foreign {
		t.Fatalf("signed rests_on = %v, want [%s]", got, foreign)
	}
	if !strings.Contains(stderr, "is another workroom's event") {
		t.Fatalf("the external citation was not distinguished: %q", stderr)
	}
	if strings.Contains(stderr, "names no event in this workroom") {
		t.Fatalf("an external citation was reported as a local one: %q", stderr)
	}
}

// A target is an event reference too, and what a ratification signs is read
// out of its own payload.
func TestTargetsAcceptShortReferencesAndSignCanonicalIdentifiers(t *testing.T) {
	t.Parallel()
	room := newRefRoom(t, "sha1")
	ids, _ := room.events()
	proposal, _, code := room.run("state", "--as", "alice", "--kind", "propose",
		"--text", "a proposal to ratify", "--rests-on", ids[0], "--idempotency-key", "proposal")
	if code != 0 {
		t.Fatal("could not file the proposal")
	}
	event := strings.TrimSpace(proposal)
	number := 0
	for _, decision := range room.snapshot().Projection.Decisions {
		if decision.Event == event {
			number = decision.Sequence
		}
	}
	stdout, stderr, code := room.run("ratify", "--as", "alice", fmt.Sprintf("#%d", number))
	if code != 0 {
		t.Fatalf("ratify by number was refused: %s", stderr)
	}
	if got := room.signedTarget(strings.TrimSpace(stdout)); got != event {
		t.Fatalf("signed target = %q, want %q", got, event)
	}
	if !strings.Contains(stderr, fmt.Sprintf("resolved #%d -> %s", number, event)) {
		t.Fatalf("ratify did not name what it resolved: %q", stderr)
	}
}

// A batch resolves the whole chain against one event set before its first
// append, and the labels that name acts the batch has yet to mint are not
// event references and travel through untouched.
func TestBatchResolvesShortReferencesAndKeepsItsLabels(t *testing.T) {
	t.Parallel()
	room := newRefRoom(t, "sha1")
	ids, hashes := room.events()
	file := filepath.Join(t.TempDir(), "batch.json")
	acts := []batchAct{
		{Label: "claim", Verb: app.VerbState, Kind: workroom.KindAssert, Text: "by number",
			RestsOn: []string{"#2"}, IdempotencyKey: "batch-number"},
		{Verb: app.VerbState, Kind: workroom.KindAssert, Text: "by hash and label",
			RestsOn: []string{uniquePrefix(hashes, 4), "$claim"}, IdempotencyKey: "batch-hash"},
	}
	encoded, err := json.Marshal(acts)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := room.run("batch", "--as", "alice", file)
	if code != 0 {
		t.Fatalf("batch was refused: %s\n%s", stderr, stdout)
	}
	var report batchReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("decode %s: %v", stdout, err)
	}
	if report.Landed != 2 {
		t.Fatalf("report = %+v", report)
	}
	if got := room.signedBases(report.Acts[0].Event); len(got) != 1 || got[0] != ids[1] {
		t.Fatalf("first act signed %v, want [%s]", got, ids[1])
	}
	second := room.signedBases(report.Acts[1].Event)
	if len(second) != 2 || second[0] != ids[4] || second[1] != report.Acts[0].Event {
		t.Fatalf("second act signed %v, want [%s %s]", second, ids[4], report.Acts[0].Event)
	}
	if strings.Contains(stdout, "resolved") {
		t.Fatalf("a resolution reached the batch report on standard output: %q", stdout)
	}

	// A chain whose last act cannot resolve lands nothing at all.
	before := room.depth()
	acts[0].IdempotencyKey, acts[1].IdempotencyKey = "batch-refused-1", "batch-refused-2"
	acts[1].RestsOn = []string{"#0", "$claim"}
	encoded, err = json.Marshal(acts)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, stderr, code = room.run("batch", "--as", "alice", file); code == 0 {
		t.Fatal("a batch with an unresolvable reference was admitted")
	}
	if !strings.Contains(stderr, "act 1:") {
		t.Fatalf("the refusal does not say which act: %q", stderr)
	}
	if after := room.depth(); after != before {
		t.Fatalf("a refused batch changed depth from %d to %d", before, after)
	}
}

// The three things an author is told before their act is signed, and the
// silence they are owed on the ordinary path. A canonical identifier of this
// workroom that names no event is refused by the sequencer, and the warning
// arrives before that refusal rather than instead of it.
func TestFilingDisclosesItsBasesBeforeSigning(t *testing.T) {
	t.Parallel()
	room := newRefRoom(t, "sha1")
	ids, _ := room.events()
	genesis := room.workspace.View().Genesis
	absent := "git:sha1:" + genesis + "#git:sha1:" + strings.Repeat("f", 40)
	foreign := "git:sha1:" + strings.Repeat("a", 40) + "#git:sha1:" + strings.Repeat("b", 40)

	t.Run("live basis is quiet", func(t *testing.T) {
		_, stderr, code := room.run("state", "--as", "alice", "--kind", "assert",
			"--text", "ordinary", "--rests-on", ids[1], "--idempotency-key", "quiet")
		if code != 0 {
			t.Fatalf("exit %d: %s", code, stderr)
		}
		if stderr != "" {
			t.Fatalf("an ordinary filing said something: %q", stderr)
		}
	})

	// The workroom's own genesis is in the log the sequencer resolves against
	// and is not an application record, so it is admitted and earns no warning.
	// It is the one basis a new workroom starts from, and warning about it
	// would put a false alarm on the ordinary path.
	t.Run("the genesis basis is quiet", func(t *testing.T) {
		genesis := "git:sha1:" + room.workspace.View().Genesis + "#git:sha1:" + room.workspace.View().Genesis
		_, stderr, code := room.run("state", "--as", "alice", "--kind", "assert",
			"--text", "on the founding", "--rests-on", genesis, "--idempotency-key", "genesis-basis")
		if code != 0 {
			t.Fatalf("the genesis basis was refused: %s", stderr)
		}
		if stderr != "" {
			t.Fatalf("the genesis basis was warned about: %q", stderr)
		}
	})

	t.Run("malformed basis warns and lands", func(t *testing.T) {
		before := room.depth()
		stdout, stderr, code := room.run("state", "--as", "alice", "--kind", "assert",
			"--text", "malformed", "--rests-on", "not-an-event", "--idempotency-key", "malformed")
		if code != 0 {
			t.Fatalf("a malformed basis was refused rather than warned: %s", stderr)
		}
		if !strings.Contains(stderr, `warning: rests-on "not-an-event" is not an event identifier`) {
			t.Fatalf("stderr = %q", stderr)
		}
		if got := room.signedBases(strings.TrimSpace(stdout)); len(got) != 1 || got[0] != "not-an-event" {
			t.Fatalf("signed rests_on = %v", got)
		}
		if after := room.depth(); after != before+1 {
			t.Fatalf("depth %d -> %d", before, after)
		}
	})

	t.Run("local identifier naming no event is warned then refused", func(t *testing.T) {
		before := room.depth()
		stdout, stderr, code := room.run("state", "--as", "alice", "--kind", "assert",
			"--text", "absent", "--rests-on", absent, "--idempotency-key", "absent")
		if code == 0 {
			t.Fatalf("an identifier of this workroom naming no event was admitted: %s", stdout)
		}
		if !strings.Contains(stderr, "warning: rests-on "+absent+" names no event in this workroom") {
			t.Fatalf("the author was not warned before signing: %q", stderr)
		}
		if !strings.Contains(stderr, "causal reference does not resolve in this log") {
			t.Fatalf("the sequencer's own refusal was lost: %q", stderr)
		}
		if after := room.depth(); after != before {
			t.Fatalf("a refused act changed depth from %d to %d", before, after)
		}
	})

	t.Run("external identifier is named as external and admitted", func(t *testing.T) {
		stdout, stderr, code := room.run("state", "--as", "alice", "--kind", "assert",
			"--text", "external", "--rests-on", foreign, "--idempotency-key", "external")
		if code != 0 {
			t.Fatalf("an external citation was refused: %s", stderr)
		}
		if !strings.Contains(stderr, "note: rests-on "+foreign+" is another workroom's event") {
			t.Fatalf("stderr = %q", stderr)
		}
		if strings.Contains(stderr, "is not an event identifier") || strings.Contains(stderr, "names no event in this workroom") {
			t.Fatalf("an external citation was confused with a malformed or absent local one: %q", stderr)
		}
		if got := room.signedBases(strings.TrimSpace(stdout)); len(got) != 1 || got[0] != foreign {
			t.Fatalf("signed rests_on = %v", got)
		}
	})

	t.Run("a retired basis still earns its own note after the act lands", func(t *testing.T) {
		retired, _, code := room.run("state", "--as", "alice", "--kind", "assert",
			"--text", "to be retired", "--rests-on", ids[1], "--idempotency-key", "to-retire")
		if code != 0 {
			t.Fatal("could not file the basis")
		}
		basis := strings.TrimSpace(retired)
		if _, stderr, code := room.run("supersede", "--as", "alice", "--text", "retired", basis); code != 0 {
			t.Fatalf("could not retire the basis: %s", stderr)
		}
		_, stderr, code := room.run("state", "--as", "alice", "--kind", "assert",
			"--text", "on dead ground", "--rests-on", basis, "--allow-dead-basis", "--idempotency-key", "dead")
		if code != 0 {
			t.Fatalf("exit %d: %s", code, stderr)
		}
		if !strings.Contains(stderr, "note: rests-on "+basis+" is already dead (retired)") {
			t.Fatalf("the dead-basis disclosure was lost: %q", stderr)
		}
	})
}

// testGitStdin runs one Git command with content on standard input.
func testGitStdin(t *testing.T, repo, input string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repo}, arguments...)...)
	command.Stdin = strings.NewReader(input)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %v: %v", arguments, err)
	}
	return string(output)
}

// `gs publish` signs the governing basis into the rests_on of every
// publication fact it derives, so it is an event reference like any other: the
// short forms resolve, the signed act carries the canonical identifier, and
// one that names no event refuses before the remote frontier is read or
// anything is appended.
func TestPublishResolvesItsGoverningBasis(t *testing.T) {
	ctx := context.Background()
	fixture := newPublicationFixture(t)
	fixture.write(".gitseq", "watch notes/**.md\n")
	fixture.write("notes/one.md", "first\n")
	head := fixture.commit("watched note")
	fixture.push("main")

	// A record with a number to type. The genesis the fixture normally cites
	// is not an application record and has none.
	governing := fixture.act(app.Act{
		Verb: app.VerbState, Kind: workroom.KindAssert, Text: "publication is governed here",
		RestsOn: []string{fixture.basis}, IdempotencyKey: "publication-governance",
	})
	number := 0
	for _, decision := range fixture.snapshot().Projection.Decisions {
		if decision.Event == governing {
			number = decision.Sequence
		}
	}
	if number == 0 {
		t.Fatal("the governing act has no record number")
	}

	if err := publishCommand(ctx, []string{"--repo", fixture.repo, "--as", "operator",
		"--basis", fmt.Sprintf("#%d", number)}); err != nil {
		t.Fatalf("publish by record number: %v", err)
	}
	fact := ""
	snapshot := fixture.snapshot()
	for _, statement := range snapshot.Projection.Statements {
		if statement.Body[publicationBodyPath] == "notes/one.md" && statement.Body[publicationBodyHead] == head {
			fact = statement.Event
		}
	}
	if fact == "" {
		t.Fatal("publish recorded no fact for the watched path")
	}
	bases := snapshot.Projection.Provenance[fact]
	if len(bases) != 1 || bases[0] != governing {
		t.Fatalf("the published fact signed %v, want the canonical [%s]", bases, governing)
	}

	// A basis naming no event refuses, and nothing is appended: not a durable
	// act, and not the remote frontier this command would otherwise advance.
	before := fixture.snapshot().Depth
	frontier := fixture.frontier()
	fixture.write("notes/two.md", "second\n")
	fixture.commit("second watched note")
	fixture.push("main")
	err := publishCommand(ctx, []string{"--repo", fixture.repo, "--as", "operator", "--basis", "#0"})
	if err == nil {
		t.Fatal("a basis naming no record was admitted")
	}
	if !strings.Contains(err.Error(), `event reference "#0"`) {
		t.Fatalf("the refusal does not name what was typed: %v", err)
	}
	if after := fixture.snapshot().Depth; after != before {
		t.Fatalf("a refused publication changed depth from %d to %d", before, after)
	}
	if after := fixture.frontier(); after != frontier {
		t.Fatalf("a refused publication advanced the remote frontier from %q to %q", frontier, after)
	}
}

// A "$label" names an act the same chain has yet to mint, so it is the one
// reference in a batch file that is always correct. Warning about it would put
// a false alarm on the ordinary path.
func TestBatchLabelsEarnNoBasisWarning(t *testing.T) {
	t.Parallel()
	room := newRefRoom(t, "sha1")
	file := filepath.Join(t.TempDir(), "batch.json")
	acts := []batchAct{
		{Label: "claim", Verb: app.VerbState, Kind: workroom.KindAssert, Text: "the claim",
			RestsOn: []string{"#2"}, IdempotencyKey: "label-quiet-claim"},
		{Verb: app.VerbState, Kind: workroom.KindAssert, Text: "rests on the claim",
			RestsOn: []string{"$claim"}, IdempotencyKey: "label-quiet-note"},
	}
	encoded, err := json.Marshal(acts)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := room.run("batch", "--as", "alice", file)
	if code != 0 {
		t.Fatalf("batch was refused: %s", stderr)
	}
	if strings.Contains(stderr, "$claim") || strings.Contains(stderr, "is not an event identifier") {
		t.Fatalf("a batch label was warned about: %q", stderr)
	}

	// The filter is about labels and nothing else: a genuinely unresolvable
	// basis in the same chain still earns its warning.
	acts[1].RestsOn = []string{"$claim", "not-an-event"}
	acts[0].IdempotencyKey, acts[1].IdempotencyKey = "label-warn-claim", "label-warn-note"
	encoded, err = json.Marshal(acts)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, stderr, code = room.run("batch", "--as", "alice", file); code != 0 {
		t.Fatalf("batch was refused: %s", stderr)
	}
	if !strings.Contains(stderr, `warning: rests-on "not-an-event" is not an event identifier`) {
		t.Fatalf("an unresolvable basis went unwarned beside a label: %q", stderr)
	}
	if strings.Contains(stderr, "$claim") {
		t.Fatalf("a batch label was warned about: %q", stderr)
	}
}

// chainCitations is the filter itself, at unit level: labels out, everything
// else kept in order and unchanged.
func TestChainCitationsKeepsEverythingButLabels(t *testing.T) {
	t.Parallel()
	got := chainCitations([]batchAct{
		{RestsOn: []string{"git:sha1:g#git:sha1:a", "$one"}},
		{RestsOn: []string{"$two", "not-an-event", "git:sha1:g#git:sha1:b"}},
		{},
	})
	want := []string{"git:sha1:g#git:sha1:a", "not-an-event", "git:sha1:g#git:sha1:b"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("chainCitations = %q, want %q", got, want)
	}
}

// A few body fields are read by a consumer as one durable event identifier —
// a request's `artifact`, a release's `authorizes_request` and
// `authorizes_approval`, a receipt's `merge_approval` — and they are resolved
// with the rest of the act. What is signed is decoded from the statement's own
// body, not from anything the command printed.
func TestRecognizedBodyFieldsResolveAndSignCanonicalIdentifiers(t *testing.T) {
	t.Parallel()
	room := newRefRoom(t, "sha1")
	ids, hashes := room.events()

	stdout, stderr, code := room.run("state", "--as", "alice", "--kind", "assert",
		"--text", "a body naming events", "--rests-on", ids[0],
		"--body", "authorizes_request=#3",
		"--body", "authorizes_approval="+uniquePrefix(hashes, 5),
		"--body", "artifact="+hashes[7],
		"--body", "merge_approval="+ids[8],
		// A commit and free prose in the same body, to prove the rule is the
		// field and not the shape of the value.
		"--body", "authorizes_candidate="+strings.Repeat("c", 40),
		"--body", "note=#3 is where this began",
		"--idempotency-key", "body-fields")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	body := room.signedBody(strings.TrimSpace(stdout))
	for field, want := range map[string]string{
		"authorizes_request":   ids[2],
		"authorizes_approval":  ids[5],
		"artifact":             ids[7],
		"merge_approval":       ids[8],
		"authorizes_candidate": strings.Repeat("c", 40),
		"note":                 "#3 is where this began",
	} {
		if body[field] != want {
			t.Errorf("signed body.%s = %q, want %q", field, body[field], want)
		}
	}

	// A recognized field naming no event refuses, and nothing is appended.
	for name, pair := range map[string]string{
		"no match":  "authorizes_request=#0",
		"ambiguous": "artifact=" + firstSharedRefPrefix(t, hashes),
	} {
		before := room.depth()
		stdout, stderr, code := room.run("state", "--as", "alice", "--kind", "assert",
			"--text", "refused body", "--rests-on", ids[0], "--body", pair,
			"--idempotency-key", "body-refused-"+strings.ReplaceAll(name, " ", "-"))
		if code == 0 {
			t.Fatalf("%s body field was admitted: %s", name, stdout)
		}
		if !strings.Contains(stderr, "body.") || !strings.Contains(stderr, "event reference") {
			t.Fatalf("%s refusal does not name the field and what was typed: %q", name, stderr)
		}
		if after := room.depth(); after != before {
			t.Fatalf("%s changed depth from %d to %d", name, before, after)
		}
	}
}

// The same rule reaches a batch entry's body, which is a third way to write
// one and was the third way to sign a short form as typed.
func TestBatchEntryBodiesResolveTheirRecognizedFields(t *testing.T) {
	t.Parallel()
	room := newRefRoom(t, "sha1")
	ids, _ := room.events()
	file := filepath.Join(t.TempDir(), "batch.json")
	write := func(acts []batchAct) {
		encoded, err := json.Marshal(acts)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, encoded, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write([]batchAct{{
		Verb: app.VerbState, Kind: workroom.KindAssert, Text: "a batch body naming an event",
		Body:    map[string]string{"authorizes_request": "#4", "note": "#4 in prose"},
		RestsOn: []string{ids[0]}, IdempotencyKey: "batch-body",
	}})
	stdout, stderr, code := room.run("batch", "--as", "alice", file)
	if code != 0 {
		t.Fatalf("batch was refused: %s", stderr)
	}
	var report batchReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("decode %s: %v", stdout, err)
	}
	body := room.signedBody(report.Acts[0].Event)
	if body["authorizes_request"] != ids[3] {
		t.Fatalf("signed body.authorizes_request = %q, want %q", body["authorizes_request"], ids[3])
	}
	if body["note"] != "#4 in prose" {
		t.Fatalf("prose was rewritten: %q", body["note"])
	}

	before := room.depth()
	write([]batchAct{{
		Verb: app.VerbState, Kind: workroom.KindAssert, Text: "unresolvable batch body",
		Body:    map[string]string{"authorizes_approval": "#0"},
		RestsOn: []string{ids[0]}, IdempotencyKey: "batch-body-refused",
	}})
	if _, stderr, code = room.run("batch", "--as", "alice", file); code == 0 {
		t.Fatal("a batch body naming no event was admitted")
	}
	if !strings.Contains(stderr, "act 0:") || !strings.Contains(stderr, "body.authorizes_approval") {
		t.Fatalf("the refusal does not say which act and which field: %q", stderr)
	}
	if after := room.depth(); after != before {
		t.Fatalf("a refused batch changed depth from %d to %d", before, after)
	}
}

// firstSharedRefPrefix is a one-character reference at least two event hashes
// answer to. With more events than hexadecimal digits, one always exists.
func firstSharedRefPrefix(t *testing.T, hashes []string) string {
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
