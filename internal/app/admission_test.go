package app

import (
	"context"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/intent"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/reviewguard"
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

// bodyOf reads back the body the fold projects for one landed event, which is
// the only place a stamp admission made is observable.
func bodyOf(t *testing.T, ctx context.Context, workspace *Workspace, event string) map[string]string {
	t.Helper()
	for _, statement := range workspace.mustSnapshot(t, ctx).Projection.Statements {
		if statement.Event == event {
			return statement.Body
		}
	}
	t.Fatalf("no projected statement for %s", event)
	return nil
}

// A state resting on an already-retired basis refuses by default: the record
// was withdrawn and nothing stands there to rest on.
func TestAdmissionRefusesARetiredBasisUntilTheEscapeIsAskedFor(t *testing.T) {
	ctx := context.Background()
	workspace, retired, stale := deadBasisFixture(t, ctx)

	before := workspace.mustSnapshot(t, ctx)
	_, err := workspace.Act(ctx, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "standing on withdrawn ground",
		RestsOn: []string{retired}, IdempotencyKey: "dead-refused-retired",
	})
	if err == nil || !strings.Contains(err.Error(), "already-retired basis") || !strings.Contains(err.Error(), "--allow-dead-basis") {
		t.Fatalf("retired basis error = %v, want the retired-basis refusal", err)
	}
	if !strings.Contains(err.Error(), retired) {
		t.Fatalf("refusal %q does not name the retired basis %s", err, retired)
	}
	after := workspace.mustSnapshot(t, ctx)
	if after.Head != before.Head {
		t.Fatalf("refused retired basis changed workroom: %s -> %s", before.Head, after.Head)
	}

	allowed := actRecord(t, ctx, workspace, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "I saw the dead bases",
		RestsOn: []string{stale, retired}, AllowDeadBasis: true, IdempotencyKey: "dead-override-signed",
	})
	if body := bodyOf(t, ctx, workspace, allowed.ID); body["dead_basis_override"] != "true" {
		t.Fatalf("allowed escape did not sign the override: %#v", body)
	}

	// An effective supersession remains advisory: citing the retirement act
	// itself refuses nothing and records no override.
	snapshot := workspace.mustSnapshot(t, ctx)
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

// A merely stale basis is admitted, and the row that lands carries the merge
// receipt's record of it: body.stale=true and a note naming the stale basis
// and the retired act underneath it. Nobody has to ask for an escape, because
// nothing was withdrawn — the record still stands and something under it
// moved.
func TestAdmissionAdmitsAStaleBasisAndRecordsTheStaleness(t *testing.T) {
	ctx := context.Background()
	workspace, retired, stale := deadBasisFixture(t, ctx)

	landed := actRecord(t, ctx, workspace, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "standing on ground that still stands",
		RestsOn: []string{stale}, IdempotencyKey: "stale-admitted",
	})
	body := bodyOf(t, ctx, workspace, landed.ID)
	if body["stale"] != "true" {
		t.Fatalf("admitted row did not record staleness: %#v", body)
	}
	// Not merely non-empty. The note has to name the stale basis it was
	// admitted over and walk down to the retirement that caused it, which is
	// exactly what a merge receipt's note does.
	if !strings.Contains(body["staleness"], stale) || !strings.Contains(body["staleness"], "stale") {
		t.Fatalf("staleness note %q does not name the stale basis %s", body["staleness"], stale)
	}
	if !strings.Contains(body["staleness"], "retired bases: "+retired) {
		t.Fatalf("staleness note %q does not name the retirement %s underneath it", body["staleness"], retired)
	}
	if body["dead_basis_override"] == "true" {
		t.Fatalf("a stale basis demanded the retired-basis escape: %#v", body)
	}

	// A live basis records nothing, so the mark stays a signal rather than
	// decoration on every row.
	live := actRecord(t, ctx, workspace, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "ground nothing has moved under",
		IdempotencyKey: "stale-live-ground",
	})
	clean := actRecord(t, ctx, workspace, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "standing on ground nothing has moved under",
		RestsOn: []string{live.ID}, IdempotencyKey: "stale-not-contagious",
	})
	if body := bodyOf(t, ctx, workspace, clean.ID); body["stale"] != "" || body["staleness"] != "" {
		t.Fatalf("a live basis recorded staleness: %#v", body)
	}
}

// stale and staleness are admission's words, not the caller's. An ordinary
// write may neither suppress the real note by sending one of its own, nor
// forge staleness onto living ground. Both halves matter: the first is what a
// row uses to lie about the ground it was signed over, the second is what it
// would use to claim ground had moved when it had not.
func TestAdmissionOwnsTheStalenessFieldsAgainstTheCaller(t *testing.T) {
	ctx := context.Background()
	workspace, retired, stale := deadBasisFixture(t, ctx)

	// (a) Suppression. The caller sends a note of its own and denies the flag.
	// Admission overwrites both from the bases it actually admitted.
	spoofed := actRecord(t, ctx, workspace, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "hiding the ground that moved",
		Body:    map[string]string{"stale": "false", "staleness": "bogus"},
		RestsOn: []string{stale}, IdempotencyKey: "staleness-suppression",
	})
	body := bodyOf(t, ctx, workspace, spoofed.ID)
	if body["stale"] != "true" {
		t.Fatalf("a caller suppressed the stale flag: %#v", body)
	}
	if strings.Contains(body["staleness"], "bogus") {
		t.Fatalf("the caller's note survived admission: %#v", body)
	}
	// The canonical note, not merely some other string: it names the stale
	// basis and walks down to the retirement underneath it.
	if !strings.Contains(body["staleness"], stale) || !strings.Contains(body["staleness"], "retired bases: "+retired) {
		t.Fatalf("staleness note %q is not the canonical note over %s", body["staleness"], stale)
	}

	// (b) Forgery. Nothing under this act has moved, so neither field may
	// survive the write.
	live := actRecord(t, ctx, workspace, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "ground nothing has moved under",
		IdempotencyKey: "staleness-forge-ground",
	})
	forged := actRecord(t, ctx, workspace, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "claiming ground moved when it did not",
		Body:    map[string]string{"stale": "true", "staleness": "invented"},
		RestsOn: []string{live.ID}, IdempotencyKey: "staleness-forgery",
	})
	if body := bodyOf(t, ctx, workspace, forged.ID); body["stale"] != "" || body["staleness"] != "" {
		t.Fatalf("a caller forged staleness onto living ground: %#v", body)
	}
}

// (c) and (d): the two in-process paths that build a note covering more than
// their own bases keep it exactly as built. A guarded review's note names the
// artifact, promise and request it stands on; a merge receipt's names the
// reviewed artifact, which is not a basis of the receipt at all. Recomputing
// either from bases would replace an accurate account with a narrower one, so
// admission must leave both alone.
func TestAdmissionKeepsAnInternallyBuiltStalenessNote(t *testing.T) {
	ctx := context.Background()
	// Deliberately unlike anything admission could derive from these bases, so
	// keeping it cannot be mistaken for recomputing it. This is the shape both
	// real builders produce: parts named in words, not by event id.
	const internal = "approval, artifact stale; describes a superseded world"
	for _, test := range []struct {
		name      string
		admission stateAdmission
	}{
		{
			name:      "guarded review",
			admission: stateAdmission{Kind: workroom.KindReport, Body: map[string]string{"review_path": reviewguard.ReviewPath}},
		},
		{
			name:      "merge receipt",
			admission: stateAdmission{Kind: workroom.KindAssert, Body: map[string]string{}, InternalStaleness: true},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			workspace, _, stale := deadBasisFixture(t, ctx)
			admission := test.admission
			admission.Body["stale"] = "true"
			admission.Body["staleness"] = internal
			admission.RestsOn = []string{stale}
			admission.AllowDeadBasis = true

			body, err := workspace.AdmitState(ctx, admission)
			if err != nil {
				t.Fatal(err)
			}
			if body["staleness"] != internal {
				t.Fatalf("admission replaced an internally built note: %q, want %q", body["staleness"], internal)
			}
			if body["stale"] != "true" {
				t.Fatalf("admission dropped an internally built stale flag: %#v", body)
			}
		})
	}
}

// The exception to admission's ownership is a field a ratified kind schema
// declares for itself, and it covers either name. kind-def already proves the
// `staleness` spelling, because its own schema declares that field to say how
// staleness propagates through the kind it defines. This proves the other one
// directly: a ratified custom kind that declares `stale` keeps what its author
// wrote there, even filing over a basis that really had gone stale, where an
// ordinary write would have both fields replaced.
//
// Both proofs are needed and neither substitutes for the other. Recognising
// only one spelling leaves the other silently stripped, and a kind whose
// required field admission deletes cannot be filed at all.
func TestAdmissionKeepsAFieldARatifiedKindDeclaresForItself(t *testing.T) {
	ctx := context.Background()
	workspace, _, stale := deadBasisFixture(t, ctx)

	// A sensor reading whose own vocabulary uses the word `stale` for
	// something of its own: whether the reading itself was old when taken.
	// That has nothing to do with provenance, and admission may not speak
	// over it.
	actRecord(t, ctx, workspace, "human", Act{
		Verb: VerbState, Kind: workroom.KindKindDef, Text: "declare reading",
		Body: map[string]string{
			"name": "reading", "fields": `[{"op":"present","name":"stale"}]`, "basis": "[]",
			"satisfier": "none", "render": "note", "staleness": "propagates", "lifecycle": "none",
			"guidance": "Report one sensor reading; stale says the reading was old when taken.",
		},
		IdempotencyKey: "kind-def-reading",
	})
	definition := ""
	for _, statement := range workspace.mustSnapshot(t, ctx).Projection.Statements {
		if statement.Kind == workroom.KindKindDef && statement.Body["name"] == "reading" {
			definition = statement.Event
		}
	}
	if definition == "" {
		t.Fatal("the kind-def did not land")
	}
	actRecord(t, ctx, workspace, "human", Act{
		Verb: VerbRatify, Target: definition, IdempotencyKey: "ratify-reading",
	})

	// Filed over a genuinely stale basis, so an ordinary write here would have
	// stale set to "true" and staleness replaced by the canonical note. The
	// declared field is what stops that.
	const wasOld = "the reading was already an hour old when taken"
	const drift = "sensor drift, not provenance"
	record := actRecord(t, ctx, workspace, "human", Act{
		Verb: VerbState, Kind: "reading", Text: "14 degrees",
		Body:           map[string]string{"stale": wasOld, "staleness": drift},
		RestsOn:        []string{stale},
		IdempotencyKey: "reading-lands",
	})
	snapshot := workspace.mustSnapshot(t, ctx)
	// Effective as well as intact: `stale` is a required field of this kind,
	// so admission deleting it would leave the fold refusing the act outright.
	decision, ok := snapshot.Projection.Decision(record.ID)
	if !ok || decision.Verdict != workroom.Effective {
		t.Fatalf("declared kind refused after ratification: %+v found=%v", decision, ok)
	}
	body := bodyOf(t, ctx, workspace, record.ID)
	if body["stale"] != wasOld {
		t.Fatalf("admission spoke over the kind's own stale field: %q, want %q", body["stale"], wasOld)
	}
	if body["staleness"] != drift {
		t.Fatalf("admission replaced the kind's own staleness field: %q, want %q", body["staleness"], drift)
	}
}

// An exact retry replays without re-judgement even after the world moved:
// idempotency-replay detection answers before admission ever looks.
func TestAdmissionReplaysExactRetriesWithoutRejudging(t *testing.T) {
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
	if fresh == nil || !strings.Contains(fresh.Error(), "already-retired basis") {
		t.Fatalf("fresh submission onto the moved ground = %v, want the dead-basis refusal", fresh)
	}
}

// The sequencing-time guard judges against the head the kernel named and
// refuses when its own verified read of the log stands somewhere else.
func TestAdmissionRefusesWhenItsWorldAndTheFrontierDisagree(t *testing.T) {
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
