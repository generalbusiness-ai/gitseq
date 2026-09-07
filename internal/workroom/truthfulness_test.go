package workroom

import (
	"bytes"
	"os"
	"reflect"
	"strings"
	"testing"
)

// ineffectiveHopWorld is the retained #140 probe: a base artifact, a middle
// request resting on it, a leaf artifact resting on the middle, a twig resting
// on the leaf, and then the base retired. valid selects whether the middle
// request is admissible; an inadmissible one is the ineffective hop.
func ineffectiveHopWorld(t testing.TB, valid bool) Projection {
	t.Helper()
	body := map[string]string{"to": agent}
	if valid {
		body["conditions"] = "deliver the requested result"
	}
	return Fold(landingWorld(t,
		event(t, lid("base"), operator, SchemaState, State{Kind: KindArtifact, Text: "base", Body: map[string]string{"path": "base.go", "commit": approvedHead}}),
		event(t, lid("middle"), operator, SchemaState, State{Kind: KindRequest, Text: "middle", Body: body}, lid("base")),
		event(t, lid("leaf"), operator, SchemaState, State{Kind: KindArtifact, Text: "leaf", Body: map[string]string{"path": "leaf.go", "commit": approvedHead}}, lid("middle")),
		event(t, lid("twig"), operator, SchemaState, State{Kind: KindAssert, Text: "twig"}, lid("leaf")),
		event(t, lid("retire"), operator, SchemaSupersede, Supersede{Target: lid("base"), Text: "base no longer applies"}, lid("base")),
	))
}

func rowStatement(t testing.TB, p Projection, event string) Statement {
	t.Helper()
	for _, statement := range p.Statements {
		if statement.Event == event {
			return statement
		}
	}
	t.Fatalf("no statement row for %s", event)
	return Statement{}
}

func rowArtifact(t testing.TB, p Projection, event string) Artifact {
	t.Helper()
	for _, artifact := range p.Artifacts {
		if artifact.Event == event {
			return artifact
		}
	}
	t.Fatalf("no artifact row for %s", event)
	return Artifact{}
}

// The governed staleness policy is unchanged: staleness does not cross a
// refused record. What changes is that the citation of a refused record is
// disclosed — on the citing row, in the filing note, and on the human page —
// so a retirement hidden under an ineffective hop is no longer silent. The
// effective-hop control pins the other half: where the hop took force, the
// retirement propagates as before and nothing is disclosed as ineffective.
func TestIneffectiveSupportIsDisclosedNotPropagated(t *testing.T) {
	control := ineffectiveHopWorld(t, true)
	if decision, ok := control.Decision(lid("middle")); !ok || decision.Verdict != Effective {
		t.Fatalf("invalid control: middle = %+v", decision)
	}
	if !rowArtifact(t, control, lid("leaf")).Stale {
		t.Fatal("control: a retired base under an effective hop must stale the leaf")
	}
	if got := DeadBases(control, []string{lid("middle")})[lid("middle")]; got != DeadBasisStale {
		t.Fatalf("control: citing the effective hop = %q, want stale", got)
	}
	if bases := rowStatement(t, control, lid("leaf")).IneffectiveBases; len(bases) != 0 {
		t.Fatalf("control: leaf discloses ineffective bases %v under an effective hop", bases)
	}
	if strings.Contains(string(RenderStatus(control)), "ineffective support") {
		t.Fatal("control: the human page names ineffective support where there is none")
	}

	p := ineffectiveHopWorld(t, false)
	if decision, ok := p.Decision(lid("middle")); !ok || decision.Verdict != Ineffective {
		t.Fatalf("invalid probe: middle = %+v", decision)
	}
	if decision, ok := p.Decision(lid("retire")); !ok || decision.Verdict != Effective {
		t.Fatalf("invalid probe: retirement = %+v", decision)
	}
	leaf := rowArtifact(t, p, lid("leaf"))
	if leaf.Stale {
		t.Fatal("staleness crossed an ineffective record; that is a policy change nobody adopted")
	}
	if got := DeadBases(p, []string{lid("middle")})[lid("middle")]; got != DeadBasisIneffective {
		t.Fatalf("citing the refused hop = %q, want ineffective", got)
	}
	want := []string{lid("middle")}
	if !reflect.DeepEqual(leaf.IneffectiveBases, want) {
		t.Fatalf("leaf artifact ineffective_bases = %v, want %v", leaf.IneffectiveBases, want)
	}
	if got := rowStatement(t, p, lid("leaf")).IneffectiveBases; !reflect.DeepEqual(got, want) {
		t.Fatalf("leaf statement ineffective_bases = %v, want %v", got, want)
	}
	// The disclosure is direct and says so. The twig rests on a live, effective
	// leaf; it learns about the refused hop from the leaf's row, not its own.
	if twig := rowStatement(t, p, lid("twig")); len(twig.IneffectiveBases) != 0 || twig.Stale {
		t.Fatalf("twig = %+v, want no disclosure and no staleness one hop further", twig)
	}
	if got, dead := DeadBases(p, []string{lid("leaf")})[lid("leaf")]; dead {
		t.Fatalf("citing the leaf = %q, want nothing: the leaf itself is live", got)
	}
	rendered := string(RenderStatus(p))
	if note := "rests on ineffective support: " + name(lid("middle"), p.sequences()); !strings.Contains(rendered, note) {
		t.Fatalf("the human page omits %q:\n%s", note, rendered)
	}
	data, err := RenderJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"ineffective_bases": [`)) {
		t.Fatal("the JSON projection omits ineffective_bases")
	}
}

// truthfulnessWorld carries one of each record the complete page previously
// left out: a ratified proposal, a standing dissent against it, and a
// statement of a kind the vocabulary does not define.
func truthfulnessWorld(t testing.TB, extra ...Record) Projection {
	t.Helper()
	return Fold(landingWorld(t, append([]Record{
		event(t, lid("plan"), operator, SchemaState, State{Kind: KindPropose, Text: "adopt the plan"}),
		event(t, lid("adopt"), bystander, SchemaRatify, Ratify{Target: lid("plan")}, lid("plan")),
		event(t, lid("object"), agent, SchemaState, State{Kind: KindDissent, Text: "the plan skips review"}, lid("plan")),
		event(t, lid("ghost"), agent, SchemaState, State{Kind: "presence", Text: "busy"}),
	}, extra...)...))
}

func TestRenderStatusShowsDissentRatifiedAndUninterpretable(t *testing.T) {
	p := truthfulnessWorld(t)
	if plan := rowStatement(t, p, lid("plan")); !plan.Ratified || plan.RatifiedBy != lid("adopt") {
		t.Fatalf("invalid world: plan = %+v", plan)
	}
	if decision, ok := p.Decision(lid("object")); !ok || decision.Verdict != Effective {
		t.Fatalf("invalid world: dissent = %+v", decision)
	}
	if decision, ok := p.Decision(lid("ghost")); !ok || decision.Verdict != UndefinedKind {
		t.Fatalf("invalid world: ghost = %+v", decision)
	}
	sequences := p.sequences()
	rendered := string(RenderStatus(p))
	for _, want := range []string{
		"\n## Standing dissent\n\n- " + name(lid("object"), sequences) + " by " + short(agent) + " against " + name(lid("plan"), sequences) + " (current): the plan skips review\n",
		"\n## Ratified statements\n\n| kind | statement | ratified by | state |\n|---|---|---|---|\n",
		"\n| propose | " + name(lid("plan"), sequences) + " | " + name(lid("adopt"), sequences) + " | current |\n",
		"\n## Uninterpretable records\n\n- undefined kind `presence`: " + name(lid("ghost"), sequences) + ": busy\n",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("complete status omits %q:\n%s", want, rendered)
		}
	}

	// Omission-sensitive the other way: a withdrawn dissent leaves the section,
	// and a dissent whose target was withdrawn says so.
	withdrawn := truthfulnessWorld(t,
		event(t, lid("recant"), agent, SchemaSupersede, Supersede{Target: lid("object"), Text: "withdrawn"}, lid("object")),
	)
	if got := string(RenderStatus(withdrawn)); !strings.Contains(got, "\n## Standing dissent\n\nNone.\n") {
		t.Errorf("a withdrawn dissent still stands on the page:\n%s", got)
	}
	retiredTarget := truthfulnessWorld(t,
		event(t, lid("drop"), operator, SchemaSupersede, Supersede{Target: lid("plan"), Text: "plan withdrawn"}, lid("plan")),
	)
	if got := string(RenderStatus(retiredTarget)); !strings.Contains(got, " against "+name(lid("plan"), retiredTarget.sequences())+" (retired)") {
		t.Errorf("a dissent against a retired record does not say so:\n%s", got)
	}

	empty := string(RenderStatus(Projection{}))
	for _, section := range []string{"## Standing dissent", "## Ratified statements", "## Uninterpretable records"} {
		if !strings.Contains(empty, "\n"+section+"\n\nNone.\n") {
			t.Errorf("empty projection lacks %q with None.:\n%s", section, empty)
		}
	}
}

// The complete page is compared byte for byte against a small representative
// fixture, so a change to any row format, section order or wording is a
// deliberate edit of testdata/render_status.golden.md and not a drift.
func TestRenderStatusBytesArePinned(t *testing.T) {
	p := truthfulnessWorld(t,
		event(t, lid("base"), operator, SchemaState, State{Kind: KindArtifact, Text: "base", Body: map[string]string{"path": "base.go", "commit": approvedHead}}),
		event(t, lid("middle"), operator, SchemaState, State{Kind: KindRequest, Text: "middle", Body: map[string]string{"to": agent}}, lid("base")),
		event(t, lid("leaf"), operator, SchemaState, State{Kind: KindArtifact, Text: "leaf", Body: map[string]string{"path": "leaf.go", "commit": approvedHead}}, lid("middle")),
		event(t, lid("retire"), operator, SchemaSupersede, Supersede{Target: lid("base"), Text: "base no longer applies"}, lid("base")),
	)
	got := RenderStatus(p)
	if !bytes.Equal(got, RenderStatus(truthfulnessWorld(t,
		event(t, lid("base"), operator, SchemaState, State{Kind: KindArtifact, Text: "base", Body: map[string]string{"path": "base.go", "commit": approvedHead}}),
		event(t, lid("middle"), operator, SchemaState, State{Kind: KindRequest, Text: "middle", Body: map[string]string{"to": agent}}, lid("base")),
		event(t, lid("leaf"), operator, SchemaState, State{Kind: KindArtifact, Text: "leaf", Body: map[string]string{"path": "leaf.go", "commit": approvedHead}}, lid("middle")),
		event(t, lid("retire"), operator, SchemaSupersede, Supersede{Target: lid("base"), Text: "base no longer applies"}, lid("base")),
	))) {
		t.Fatal("complete status is not byte-stable across folds of the same log")
	}
	want, err := os.ReadFile("testdata/render_status.golden.md")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("complete status bytes changed; update testdata/render_status.golden.md only through review\n%s", got)
	}
}
