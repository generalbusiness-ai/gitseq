package app

import (
	"context"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/intent"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

func admissionWorkspace(t *testing.T, ctx context.Context) (*Workspace, workroom.Record) {
	t.Helper()
	workspace, seed, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	return workspace, seed
}

// An undefined kind is refused before anything is signed, whatever surface
// asks, and the refusal points at the route that does exist.
func TestAdmissionRefusesUndefinedStateKindsWithoutOverride(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	workspace, seed := admissionWorkspace(t, ctx)
	for _, test := range []struct {
		kind string
		want []string
	}{
		{kind: "ratify", want: []string{"no override exists", "gs ratify"}},
		{kind: "supersede", want: []string{"gs supersede", "supersede tools"}},
		{kind: "review", want: []string{"gs review", "review tool"}},
		{kind: "whisper", want: []string{"no override exists", "kinds defined here:", "ratified kind-def"}},
	} {
		t.Run(test.kind, func(t *testing.T) {
			before := workspace.mustSnapshot(t, ctx)
			_, err := workspace.Act(ctx, "human", Act{
				Verb: VerbState, Kind: workroom.Kind(test.kind), Text: "a mistake shaped like a statement",
				RestsOn:        []string{seed.ID},
				IdempotencyKey: "undefined-" + test.kind,
			})
			if err == nil {
				t.Fatal("an undefined kind was signed")
			}
			for _, want := range test.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error %q does not name %q", err, want)
				}
			}
			after := workspace.mustSnapshot(t, ctx)
			if after.Head != before.Head || after.Depth != before.Depth {
				t.Fatalf("refused undefined kind changed workroom: before=%s/%d after=%s/%d", before.Head, before.Depth, after.Head, after.Depth)
			}
		})
	}
}

// A custom kind a ratified kind-def established stays exactly as writable as
// it was.
func TestAdmissionKeepsDeclaredCustomKindsValid(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	workspace, seed := admissionWorkspace(t, ctx)
	actRecord(t, ctx, workspace, "human", Act{
		Verb: VerbState, Kind: workroom.KindKindDef, Text: "declare whisper",
		Body: map[string]string{
			"name": "whisper", "fields": "[]", "basis": "[]", "satisfier": "none",
			"render": "note", "staleness": "propagates", "lifecycle": "none",
			"guidance": "Say something quietly.",
		},
		RestsOn:        []string{seed.ID},
		IdempotencyKey: "kind-def-whisper",
	})
	definition := ""
	for _, statement := range workspace.mustSnapshot(t, ctx).Projection.Statements {
		if statement.Kind == workroom.KindKindDef && statement.Body["name"] == "whisper" {
			definition = statement.Event
		}
	}
	if definition == "" {
		t.Fatal("the kind-def did not land")
	}
	actRecord(t, ctx, workspace, "human", Act{
		Verb: VerbRatify, Target: definition, IdempotencyKey: "ratify-whisper",
	})
	record := actRecord(t, ctx, workspace, "human", Act{
		Verb: VerbState, Kind: "whisper", Text: "quietly now",
		RestsOn:        []string{seed.ID},
		IdempotencyKey: "whisper-lands",
	})
	decision, ok := workspace.mustSnapshot(t, ctx).Projection.Decision(record.ID)
	if !ok || decision.Verdict != workroom.Effective {
		t.Fatalf("declared kind refused after ratification: %+v found=%v", decision, ok)
	}
}

// Both verdict-shaped fields route to the guarded path, on either spelling,
// while an ordinary verdict word on a report stays a plain report.
func TestAdmissionRefusesVerdictShapedReportsOnEveryField(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	workspace, _ := admissionWorkspace(t, ctx)
	promise := actRecord(t, ctx, workspace, "human", Act{
		Verb: VerbState, Kind: workroom.KindRequest, Text: "review something",
		Body:           map[string]string{"to": "human", "conditions": "exact head"},
		IdempotencyKey: "admission-request",
	})
	for _, test := range []struct {
		field string
		value string
	}{
		{field: "verdict", value: "approved"},
		{field: "verdict", value: "changes-requested"},
		{field: "status", value: "approved"},
		{field: "status", value: "changes-requested"},
	} {
		t.Run(test.field+"="+test.value, func(t *testing.T) {
			before := workspace.mustSnapshot(t, ctx)
			err := func() error {
				_, err := workspace.Act(ctx, "human", Act{
					Verb: VerbState, Kind: workroom.KindReport, Text: "hand-filed verdict",
					Body:    map[string]string{test.field: test.value, "head": strings.Repeat("0", 40)},
					RestsOn: []string{promise.ID}, IdempotencyKey: "verdict-shape-" + test.field + "-" + test.value,
				})
				return err
			}()
			if err == nil || !strings.Contains(err.Error(), "file it with gs review") {
				t.Fatalf("verdict-shaped report error = %v", err)
			}
			after := workspace.mustSnapshot(t, ctx)
			if after.Head != before.Head || after.Depth != before.Depth {
				t.Fatalf("refused verdict shape changed workroom: before=%s/%d after=%s/%d", before.Head, before.Depth, after.Head, after.Depth)
			}
		})
	}
	record := actRecord(t, ctx, workspace, "human", Act{
		Verb: VerbState, Kind: workroom.KindReport, Text: "an ordinary report",
		Body:    map[string]string{"verdict": "needs-work"},
		RestsOn: []string{promise.ID}, IdempotencyKey: "nonreview-verdict",
	})
	decision, ok := workspace.mustSnapshot(t, ctx).Projection.Decision(record.ID)
	if !ok || decision.Verdict != workroom.Effective {
		t.Fatalf("ordinary report refused: %+v found=%v", decision, ok)
	}
}

// Reserved admission fields are never caller input.
func TestAdmissionRejectsReservedFieldSpoofing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	workspace, seed := admissionWorkspace(t, ctx)
	for _, field := range ReservedBodyFields {
		t.Run(field, func(t *testing.T) {
			before := workspace.mustSnapshot(t, ctx)
			err := func() error {
				_, err := workspace.Act(ctx, "human", Act{
					Verb: VerbState, Kind: workroom.KindAssert, Text: "speaking for admission",
					Body:    map[string]string{field: "true"},
					RestsOn: []string{seed.ID}, IdempotencyKey: "spoof-" + field,
				})
				return err
			}()
			if err == nil || !strings.Contains(err.Error(), "reserved admission field") {
				t.Fatalf("reserved field %s error = %v", field, err)
			}
			after := workspace.mustSnapshot(t, ctx)
			if after.Head != before.Head {
				t.Fatalf("spoofed reserved field changed workroom: %s -> %s", before.Head, after.Head)
			}
		})
	}
}

func deadBasisFixture(t *testing.T, ctx context.Context) (*Workspace, string, string) {
	t.Helper()
	workspace, seed := admissionWorkspace(t, ctx)
	retired := actRecord(t, ctx, workspace, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "soon withdrawn",
		RestsOn: []string{seed.ID}, IdempotencyKey: "dead-retired",
	})
	stale := actRecord(t, ctx, workspace, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "soon stale",
		RestsOn: []string{retired.ID}, IdempotencyKey: "dead-stale",
	})
	actRecord(t, ctx, workspace, "human", Act{
		Verb: VerbSupersede, Target: retired.ID, Text: "withdrawn",
		IdempotencyKey: "dead-retire-it",
	})
	snapshot := workspace.mustSnapshot(t, ctx)
	for _, candidate := range snapshot.Projection.Statements {
		if candidate.Event == retired.ID && !candidate.Retired {
			t.Fatalf("%s was not retired by setup", retired.ID)
		}
		if candidate.Event == stale.ID && !candidate.Stale {
			t.Fatalf("%s did not go stale in setup", stale.ID)
		}
	}
	return workspace, retired.ID, stale.ID
}

// A state resting on an already-retired or already-stale basis refuses by
// default; asking for the escape signs the override; citing an effective
// supersession stays advisory.
func TestAdmissionRefusesDeadBasesUntilTheEscapeIsAskedFor(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	workspace, retired, stale := deadBasisFixture(t, ctx)

	refused := func(name, basis string) {
		t.Helper()
		before := workspace.mustSnapshot(t, ctx)
		err := func() error {
			_, err := workspace.Act(ctx, "human", Act{
				Verb: VerbState, Kind: workroom.KindAssert, Text: "standing on " + name,
				RestsOn: []string{basis}, IdempotencyKey: "dead-refused-" + name,
			})
			return err
		}()
		if err == nil || !strings.Contains(err.Error(), "already-dead basis") || !strings.Contains(err.Error(), "--allow-dead-basis") {
			t.Fatalf("%s basis error = %v", name, err)
		}
		after := workspace.mustSnapshot(t, ctx)
		if after.Head != before.Head {
			t.Fatalf("refused %s basis changed workroom", name)
		}
	}
	refused("retired", retired)
	refused("stale", stale)

	allowed := actRecord(t, ctx, workspace, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "I saw the dead bases",
		RestsOn: []string{stale, retired}, AllowDeadBasis: true, IdempotencyKey: "dead-override-signed",
	})
	snapshot := workspace.mustSnapshot(t, ctx)
	var body map[string]string
	for _, statement := range snapshot.Projection.Statements {
		if statement.Event == allowed.ID {
			body = statement.Body
		}
	}
	if body["dead_basis_override"] != "true" {
		t.Fatalf("allowed escape did not sign the override: %#v", body)
	}

	// An effective supersession remains advisory: citing the retirement act
	// itself refuses nothing and records no override.
	snapshot = workspace.mustSnapshot(t, ctx)
	retirement := ""
	for _, act := range snapshot.Projection.Acts {
		if act.Type == "supersede" && act.Target == retired {
			retirement = act.Event
		}
	}
	if retirement == "" {
		t.Fatal("setup lost the retirement act")
	}
	citing := actRecord(t, ctx, workspace, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "citing the retirement itself",
		RestsOn: []string{retirement}, IdempotencyKey: "dead-advisory",
	})
	for _, statement := range workspace.mustSnapshot(t, ctx).Projection.Statements {
		if statement.Event == citing.ID && statement.Body["dead_basis_override"] == "true" {
			t.Fatalf("advisory supersession citation recorded an override: %#v", statement.Body)
		}
	}
}

// An exact retry replays without re-judgement even after the world moved:
// idempotency-replay detection answers before admission ever looks.
func TestAdmissionReplaysExactRetriesWithoutRejudging(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	workspace, seed := admissionWorkspace(t, ctx)
	live := actRecord(t, ctx, workspace, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "ground that will move",
		RestsOn: []string{seed.ID}, IdempotencyKey: "replay-ground",
	})
	_, private, err := workspace.Actor("human")
	if err != nil {
		t.Fatal(err)
	}
	request, err := workspace.BuildActRequest(ctx, private, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "filed against a living world",
		RestsOn: []string{live.ID}, IdempotencyKey: "replay-vs-admission",
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := workspace.AcceptSubmission(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	// Retire the basis the act rested on. A genuinely new submission resting
	// there now refuses at admission; the exact retry of the landed one must
	// simply replay its record.
	actRecord(t, ctx, workspace, "human", Act{
		Verb: VerbSupersede, Target: live.ID, Text: "the ground moved",
		IdempotencyKey: "replay-move-world",
	})
	before := workspace.mustSnapshot(t, ctx)
	retried, err := workspace.AcceptSubmission(ctx, request)
	if err != nil {
		t.Fatalf("exact retry after movement = %v, want a replay", err)
	}
	if !retried.Result.Replay || retried.Result.Commit != first.Result.Commit {
		t.Fatalf("retry = %+v first = %+v, want the same record replayed", retried.Result, first.Result)
	}
	after := workspace.mustSnapshot(t, ctx)
	if after.Depth != before.Depth {
		t.Fatalf("a replay changed depth %d -> %d", before.Depth, after.Depth)
	}
	fresh := func() error {
		_, err := workspace.Act(ctx, "human", Act{
			Verb: VerbState, Kind: workroom.KindAssert, Text: "newly standing on the moved ground",
			RestsOn: []string{live.ID}, IdempotencyKey: "fresh-on-moved-ground",
		})
		return err
	}()
	if fresh == nil || !strings.Contains(fresh.Error(), "already-dead basis") {
		t.Fatalf("fresh submission onto the moved ground = %v, want the dead-basis refusal", fresh)
	}
}

// The sequencing-time guard judges against the head the kernel named and
// refuses when its own verified read of the log stands somewhere else.
func TestAdmissionRefusesWhenItsWorldAndTheFrontierDisagree(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	workspace, seed := admissionWorkspace(t, ctx)
	payload, err := workroom.Encode(workroom.State{Kind: workroom.KindAssert, Text: "judged against nowhere"})
	if err != nil {
		t.Fatal(err)
	}
	tree, err := workspace.Store.WritePayloadTree(ctx, payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	view := workspace.View()
	_, private, err := workspace.Actor("human")
	if err != nil {
		t.Fatal(err)
	}
	signed, err := intent.Sign(intent.Intent{
		Version: intent.Version,
		Target:  "git:" + view.ObjectFormat + ":" + view.Genesis,
		Schema:  workroom.SchemaState, PayloadTree: "git:" + view.ObjectFormat + ":" + tree,
		RestsOn:        []string{seed.ID},
		IdempotencyNS:  view.IdempotencyNamespace,
		IdempotencyKey: "frontier-mismatch",
	}, private)
	if err != nil {
		t.Fatal(err)
	}
	decodedIntent, err := intent.Verify(signed)
	if err != nil {
		t.Fatal(err)
	}
	fake := strings.Repeat("a", len(view.Genesis))
	err = workspace.admitApplication(ctx, kernel.Application{
		Intent: decodedIntent, ActorKey: signed.ActorKey, Payload: payload, Head: fake,
	})
	if err == nil || !strings.Contains(err.Error(), "moved under admission") {
		t.Fatalf("mismatched-frontier admission error = %v", err)
	}
}
