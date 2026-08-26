package main

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/statusview"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// ---------------------------------------------------------------------------
// Condition 1: publication mints no artifacts, and every fact carries the
// exact path, the accepted head, and the remote.
// ---------------------------------------------------------------------------

func TestPublicationMintsNoArtifactAndCarriesPathHeadAndRemote(t *testing.T) {
	f := newPublicationFixture(t)
	f.write(".gitseq", "watch notes/**.md\n")
	f.write("notes/one.md", "one\n")
	head := f.commit("first note")
	f.push("HEAD:main")

	report := f.publish()
	if len(report.Published) != 1 || report.Published[0].Outcome != "landed" || report.Head != head {
		t.Fatalf("publication report = %+v", report)
	}
	projection := f.snapshot().Projection
	for _, artifact := range projection.Artifacts {
		if artifact.Path == "notes/one.md" {
			t.Fatalf("publication minted an artifact: %+v", artifact)
		}
	}
	fact := statementByEvent(t, projection, report.Published[0].Event)
	if fact.Kind != workroom.KindAssert {
		t.Fatalf("publication fact kind = %q, want assert", fact.Kind)
	}
	want := map[string]string{
		publicationBodyPath:   "notes/one.md",
		publicationBodyHead:   head,
		publicationBodyRemote: "origin",
		publicationBodyRef:    "refs/heads/main",
	}
	for field, value := range want {
		if fact.Body[field] != value {
			t.Fatalf("publication fact body[%q] = %q, want %q (body %#v)", field, fact.Body[field], value, fact.Body)
		}
	}
	// The artifact schema's own field names must not appear, or a reader that
	// keys on them treats this fact as an artifact.
	if fact.Body["path"] != "" || fact.Body["commit"] != "" {
		t.Fatalf("publication fact borrowed the artifact schema fields: %#v", fact.Body)
	}
}

// ---------------------------------------------------------------------------
// Condition 2: prefer an artifact at the exact path AND the exact accepted
// head, unsettled candidate included; otherwise the governing basis, and say
// that no artifact was available.
// ---------------------------------------------------------------------------

func TestPublicationPrefersAnArtifactAtTheExactPathAndHead(t *testing.T) {
	f := newPublicationFixture(t)
	f.write(".gitseq", "watch notes/**.md\n")
	f.write("notes/one.md", "one\n")
	f.write("notes/two.md", "two\n")
	head := f.commit("two notes")
	f.push("HEAD:main")

	// An unsettled candidate: a live artifact nobody has merged or ratified.
	// It stands at the exact path and the exact accepted head, so it is the
	// preferred basis.
	candidate := f.act(app.Act{
		Verb: app.VerbState, Kind: workroom.KindArtifact, Text: "candidate at the accepted head",
		Body: map[string]string{"path": "notes/one.md", "commit": head}, RestsOn: []string{f.basis},
	})
	// A decoy at the right path and the wrong head, and another at the right
	// head and the wrong path. Neither may be chosen.
	f.act(app.Act{
		Verb: app.VerbState, Kind: workroom.KindArtifact, Text: "wrong head",
		Body: map[string]string{"path": "notes/one.md", "commit": strings.Repeat("a", 40)}, RestsOn: []string{f.basis},
	})
	f.act(app.Act{
		Verb: app.VerbState, Kind: workroom.KindArtifact, Text: "wrong path",
		Body: map[string]string{"path": "notes/elsewhere.md", "commit": head}, RestsOn: []string{f.basis},
	})

	report := f.publish()
	byPath := outcomesByPath(report)
	if byPath["notes/one.md"].Basis != candidate {
		t.Fatalf("basis for the path with an exact artifact = %q, want the unsettled candidate %q", byPath["notes/one.md"].Basis, candidate)
	}
	if byPath["notes/two.md"].Basis != "" {
		t.Fatalf("basis for the path with no artifact = %q, want none", byPath["notes/two.md"].Basis)
	}
	projection := f.snapshot().Projection
	withArtifact := statementByEvent(t, projection, byPath["notes/one.md"].Event)
	if withArtifact.Body[publicationBodyArtifact] != candidate {
		t.Fatalf("fact did not name the artifact it rested on: %#v", withArtifact.Body)
	}
	if !containsEvent(projection.Provenance[withArtifact.Event], candidate) {
		t.Fatalf("fact bases = %#v, want the exact-head artifact", projection.Provenance[withArtifact.Event])
	}
	withoutArtifact := statementByEvent(t, projection, byPath["notes/two.md"].Event)
	if !containsEvent(projection.Provenance[withoutArtifact.Event], f.basis) {
		t.Fatalf("fact bases = %#v, want the governing publication basis %q", projection.Provenance[withoutArtifact.Event], f.basis)
	}
	if !strings.Contains(withoutArtifact.Text, "No artifact stood at this path and head") {
		t.Fatalf("fact did not state that no artifact was available: %q", withoutArtifact.Text)
	}
	if withoutArtifact.Body[publicationBodyArtifact] != "" {
		t.Fatalf("fact claimed an artifact it did not have: %#v", withoutArtifact.Body)
	}
}

// A retired artifact at the exact path and head is not a basis: nothing may
// rest on a withdrawn pointer, so the fact falls back to the governing basis.
func TestPublicationWillNotRestOnARetiredArtifact(t *testing.T) {
	f := newPublicationFixture(t)
	f.write(".gitseq", "watch notes/**.md\n")
	f.write("notes/one.md", "one\n")
	head := f.commit("one note")
	f.push("HEAD:main")
	artifact := f.act(app.Act{
		Verb: app.VerbState, Kind: workroom.KindArtifact, Text: "withdrawn pointer",
		Body: map[string]string{"path": "notes/one.md", "commit": head}, RestsOn: []string{f.basis},
	})
	f.act(app.Act{Verb: app.VerbSupersede, Target: artifact, Text: "withdrawn"})

	report := f.publish()
	if len(report.Published) != 1 || report.Published[0].Basis != "" {
		t.Fatalf("publication rested on a retired artifact: %+v", report)
	}
}

// ---------------------------------------------------------------------------
// Conditions 3 and 4: a subsequent fact rests on and supersedes its same-path
// predecessor; an identical accepted head replays; a force push succeeds.
// ---------------------------------------------------------------------------

func TestPublicationSuccessorRestsOnAndSupersedesItsPredecessor(t *testing.T) {
	f := newPublicationFixture(t)
	f.write(".gitseq", "watch notes/**.md\n")
	f.write("notes/one.md", "one\n")
	first := f.commit("first note")
	f.push("HEAD:main")
	firstReport := f.publish()
	if len(firstReport.Published) != 1 {
		t.Fatalf("first publication = %+v", firstReport)
	}
	predecessor := firstReport.Published[0].Event

	// Condition 4, first clause: an identical accepted head publishes nothing.
	if replay := f.publish(); len(replay.Published) != 0 || replay.Head != first {
		t.Fatalf("identical accepted head was not idempotent: %+v", replay)
	}

	// Condition 4, second clause: a force push creates a successor.
	f.write("notes/one.md", "rewritten\n")
	f.git("add", "-A")
	f.git("commit", "--amend", "-q", "-m", "rewritten note")
	second := strings.TrimSpace(f.git("rev-parse", "HEAD"))
	if _, err := exec.Command("git", "-C", f.repo, "merge-base", "--is-ancestor", first, second).CombinedOutput(); err == nil {
		t.Fatal("force-push fixture stayed fast-forward")
	}
	f.git("push", "--force", "origin", "HEAD:main")

	report := f.publish()
	if len(report.Published) != 1 || report.Head != second {
		t.Fatalf("force-push publication = %+v", report)
	}
	successor := report.Published[0].Event
	if report.Published[0].Retire == "" {
		t.Fatal("successor did not retire its predecessor")
	}
	// The whole succession, not half of it: an entry reaches "landed" only
	// when both durable phases are verified effective.
	if report.Published[0].Outcome != "landed" {
		t.Fatalf("own-author succession outcome = %q, want landed", report.Published[0].Outcome)
	}
	projection := f.snapshot().Projection
	if decision, judged := projection.Decision(report.Published[0].Retire); !judged || decision.Verdict != workroom.Effective {
		t.Fatalf("retirement decision = %+v, judged %v, want effective", decision, judged)
	}
	if !containsEvent(projection.Provenance[successor], predecessor) {
		t.Fatalf("successor bases = %#v, want the same-path predecessor %q", projection.Provenance[successor], predecessor)
	}
	if !statementByEvent(t, projection, predecessor).Retired {
		t.Fatal("predecessor publication fact was not retired")
	}
	if statementByEvent(t, projection, successor).Retired {
		t.Fatal("successor publication fact was retired")
	}
	if !containsEvent(projection.Provenance[report.Published[0].Retire], successor) {
		t.Fatalf("retirement bases = %#v, want the successor it names", projection.Provenance[report.Published[0].Retire])
	}
	live := 0
	for path, fact := range livePublicationFacts(projection, "origin") {
		if path == "notes/one.md" && fact.Event == successor {
			live++
		}
	}
	if live != 1 {
		t.Fatalf("live facts at notes/one.md = %d, want exactly the successor", live)
	}
}

// Condition 4, third clause: dropping a path from the watch globs bare-retires
// its final fact — no successor is named, because there is none.
func TestWatchRemovalBareRetiresTheFinalFact(t *testing.T) {
	f := newPublicationFixture(t)
	f.write(".gitseq", "watch notes/**.md\n")
	f.write("notes/one.md", "one\n")
	f.commit("watched note")
	f.push("HEAD:main")
	first := f.publish()
	if len(first.Published) != 1 {
		t.Fatalf("first publication = %+v", first)
	}
	fact := first.Published[0].Event

	f.write(".gitseq", "watch docs/**.md\n")
	f.write("docs/x.md", "moved on\n")
	f.commit("stop watching notes")
	f.push("HEAD:main")

	report := f.publish()
	byPath := outcomesByPath(report)
	withdrawal, found := byPath["notes/one.md"]
	if !found || withdrawal.Outcome != "withdrawn" || withdrawal.Retire == "" || withdrawal.Event != "" {
		t.Fatalf("watch removal outcome = %+v (report %+v)", withdrawal, report)
	}
	if byPath["docs/x.md"].Outcome != "landed" {
		t.Fatalf("newly watched path did not publish: %+v", report)
	}
	projection := f.snapshot().Projection
	if !statementByEvent(t, projection, fact).Retired {
		t.Fatal("the unwatched path's final fact was not retired")
	}
	// A bare retirement names nothing beyond the thing it retires. A
	// supersession's provenance always carries its own target; anything else
	// in there would be a successor, and the fold would read the withdrawal as
	// a succession to somewhere.
	if bases := projection.Provenance[withdrawal.Retire]; !reflect.DeepEqual(bases, []string{fact}) {
		t.Fatalf("bare retirement cited %#v, want only its target %q", bases, fact)
	}
	// It happens once. A later run has no live fact left to withdraw.
	if again := f.publish(); len(again.Published) != 0 {
		t.Fatalf("watch removal repeated itself: %+v", again)
	}
}

// ---------------------------------------------------------------------------
// One live publication wire per remote and path, succeeded by one author from
// end to end. A live same-path predecessor belonging to another actor refuses
// the whole derived batch before a byte is queued, an act appended, or the
// shared frontier moved.
// ---------------------------------------------------------------------------

func TestPublicationRefusesTheWholeBatchOverAForeignPredecessor(t *testing.T) {
	f := newPublicationFixture(t)
	f.write(".gitseq", "watch notes/**.md\n")
	f.write("notes/one.md", "one\n")
	first := f.commit("first note")
	f.push("HEAD:main")

	firstReport := f.publish()
	if len(firstReport.Published) != 1 {
		t.Fatalf("operator publication = %+v", firstReport)
	}
	predecessor := firstReport.Published[0].Event

	// A plain participant, holding no authority of any kind. The ratifier case
	// is a separate test, because a ratifier is refused for a different reason
	// than "you may not retire that".
	worker, workerPrivate := f.addActor("worker")
	if roles := f.snapshot().Projection.Actors[worker].Roles; containsEvent(roles, "ratifier") {
		t.Fatalf("worker roles = %#v, want no ratifier in this case", roles)
	}

	// The worker's push touches the operator's path and one nobody has ever
	// published. Refusing the whole batch means the untouched path stays
	// unpublished too.
	f.write("notes/one.md", "rewritten by a second hand\n")
	f.write("notes/two.md", "a path with no wire at all\n")
	second := f.commit("second note")
	f.push("HEAD:main")

	report, err := f.publishAs("worker", worker, workerPrivate)
	if err == nil {
		t.Fatalf("a second author continued another actor's publication wire: %+v", report)
	}
	for _, want := range []string{
		"notes/one.md",
		"same publisher must continue the chain",
		"ratifier must terminally retire an orphan",
		"one live publication wire per remote and path",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("handover refusal %q does not say %q", err.Error(), want)
		}
	}
	if len(report.Published) != 0 {
		t.Fatalf("the refused batch reported publications: %+v", report)
	}

	// No assert, no outbox, no frontier mutation.
	if _, statErr := os.Stat(publicationOutboxPath(f.workspace.MetaDir, worker)); !os.IsNotExist(statErr) {
		t.Fatalf("the refused batch was queued: %v", statErr)
	}
	if got := f.frontier(); got != first {
		t.Fatalf("the refused run moved the frontier to %q, want the operator's head %q", got, first)
	}
	projection := f.snapshot().Projection
	for _, statement := range projection.Statements {
		if statement.Body[publicationBodyHead] == second {
			t.Fatalf("the refused batch signed a fact: %+v", statement)
		}
	}

	// The predecessor is left sole live, and untouched.
	facts := livePublicationFacts(projection, "origin")
	if len(facts) != 1 || facts["notes/one.md"].Event != predecessor {
		t.Fatalf("live facts after the refusal = %#v, want only the predecessor %q", facts, predecessor)
	}
	if statementByEvent(t, projection, predecessor).Retired {
		t.Fatal("the refused run retired another actor's fact")
	}
}

// Holding `ratifier` is not an exception. A ratifier may lawfully retire
// another actor's fact, but doing it inside publication would end one author's
// wire and begin another's in a single unreviewed step.
func TestAPublisherHoldingRatifierIsAlsoRefused(t *testing.T) {
	f := newPublicationFixture(t)
	f.write(".gitseq", "watch notes/**.md\n")
	f.write("notes/one.md", "one\n")
	first := f.commit("first note")
	f.push("HEAD:main")
	if report := f.publish(); len(report.Published) != 1 {
		t.Fatalf("operator publication = %+v", report)
	}

	worker, workerPrivate := f.addActor("worker")
	if _, err := f.workspace.GrantRole(context.Background(), "operator", "worker", "ratifier"); err != nil {
		t.Fatal(err)
	}
	// The precondition is part of the claim: this publisher really does hold
	// the role the old code made an exception for.
	roles := f.snapshot().Projection.Actors[worker].Roles
	if !containsEvent(roles, "ratifier") {
		t.Fatalf("worker roles = %#v, want ratifier", roles)
	}

	f.write("notes/one.md", "rewritten by the ratifier\n")
	f.commit("second note")
	f.push("HEAD:main")

	report, err := f.publishAs("worker", worker, workerPrivate)
	if err == nil {
		t.Fatalf("a ratifier continued another actor's publication wire: %+v", report)
	}
	if !strings.Contains(err.Error(), "ratifier must terminally retire an orphan") {
		t.Fatalf("ratifier refusal = %v", err)
	}
	if _, statErr := os.Stat(publicationOutboxPath(f.workspace.MetaDir, worker)); !os.IsNotExist(statErr) {
		t.Fatalf("the refused ratifier batch was queued: %v", statErr)
	}
	if got := f.frontier(); got != first {
		t.Fatalf("the refused ratifier run moved the frontier to %q, want %q", got, first)
	}
}

// A path nobody has published carries no wire, so any actor may begin one.
func TestADifferentActorMayPublishANeverPublishedPath(t *testing.T) {
	f := newPublicationFixture(t)
	f.write(".gitseq", "watch notes/**.md\n")
	f.write("notes/one.md", "one\n")
	f.commit("first note")
	f.push("HEAD:main")
	if report := f.publish(); len(report.Published) != 1 {
		t.Fatalf("operator publication = %+v", report)
	}

	worker, workerPrivate := f.addActor("worker")
	f.write("notes/two.md", "a path the operator never published\n")
	head := f.commit("a second path")
	f.push("HEAD:main")

	report, err := f.publishAs("worker", worker, workerPrivate)
	if err != nil || len(report.Published) != 1 || report.Published[0].Path != "notes/two.md" || report.Published[0].Outcome != "landed" {
		t.Fatalf("fresh-path publication by a second actor = %+v, %v", report, err)
	}
	// A fresh chain succeeds nothing, so it retires nothing.
	if report.Published[0].Retire != "" {
		t.Fatalf("a fresh chain retired something: %+v", report.Published[0])
	}
	projection := f.snapshot().Projection
	fact := statementByEvent(t, projection, report.Published[0].Event)
	if fact.Actor != worker || fact.Body[publicationBodyHead] != head {
		t.Fatalf("fresh fact = %+v", fact)
	}
	facts := livePublicationFacts(projection, "origin")
	if len(facts) != 2 || facts["notes/one.md"].Actor != f.fingerprint || facts["notes/two.md"].Actor != worker {
		t.Fatalf("live facts = %#v, want one wire per author per path", facts)
	}
}

// Terminally retiring the orphan is the lawful way a path changes publisher.
// Once no live fact stands there, the next actor begins a fresh chain rather
// than continuing someone else's.
func TestANewActorStartsFreshAfterATerminalOrphanRetirement(t *testing.T) {
	f := newPublicationFixture(t)
	f.write(".gitseq", "watch notes/**.md\n")
	f.write("notes/one.md", "one\n")
	f.commit("first note")
	f.push("HEAD:main")
	first := f.publish()
	if len(first.Published) != 1 {
		t.Fatalf("operator publication = %+v", first)
	}
	orphan := first.Published[0].Event

	worker, workerPrivate := f.addActor("worker")
	f.write("notes/one.md", "rewritten by another hand\n")
	second := f.commit("second note")
	f.push("HEAD:main")
	if _, err := f.publishAs("worker", worker, workerPrivate); err == nil {
		t.Fatal("a second author continued a live wire")
	}

	// The fact's own author — who here also holds ratifier — terminally
	// retires it, naming no successor.
	f.act(app.Act{Verb: app.VerbSupersede, Target: orphan,
		Text: "Terminally retire the publication wire at notes/one.md so another actor may begin one."})
	if !statementByEvent(t, f.snapshot().Projection, orphan).Retired {
		t.Fatal("the orphan was not retired")
	}

	report, err := f.publishAs("worker", worker, workerPrivate)
	if err != nil || len(report.Published) != 1 || report.Published[0].Outcome != "landed" {
		t.Fatalf("fresh chain after a terminal retirement = %+v, %v", report, err)
	}
	if report.Published[0].Retire != "" {
		t.Fatalf("the fresh chain retired something: %+v", report.Published[0])
	}
	projection := f.snapshot().Projection
	successor := report.Published[0].Event
	if containsEvent(projection.Provenance[successor], orphan) {
		t.Fatalf("the fresh chain rested on the retired orphan: %#v", projection.Provenance[successor])
	}
	fact := statementByEvent(t, projection, successor)
	if fact.Actor != worker || fact.Body[publicationBodyHead] != second {
		t.Fatalf("fresh fact = %+v", fact)
	}
	facts := livePublicationFacts(projection, "origin")
	if len(facts) != 1 || facts["notes/one.md"].Event != successor {
		t.Fatalf("live facts = %#v, want only the worker's fresh fact", facts)
	}
}

// ---------------------------------------------------------------------------
// Two durable phases: the successor assert, and the retirement of the
// predecessor it succeeds. Each is separately durable and verified on the
// exact event the sequencer accepted.
// ---------------------------------------------------------------------------

// A crash between the phases leaves a successor that is already visible and a
// predecessor that is still live. Visibility is not completion: the recovering
// run owes the retirement, and the shared frontier waits for it.
func TestARecoveredVisibleSuccessorStillRetiresItsPredecessor(t *testing.T) {
	f := newPublicationFixture(t)
	f.write(".gitseq", "watch notes/**.md\n")
	f.write("notes/one.md", "one\n")
	first := f.commit("first note")
	f.push("HEAD:main")
	firstReport := f.publish()
	if len(firstReport.Published) != 1 {
		t.Fatalf("first publication = %+v", firstReport)
	}
	predecessor := firstReport.Published[0].Event

	f.write("notes/one.md", "two\n")
	second := f.commit("second note")
	f.push("HEAD:main")

	// Exactly what phase one leaves behind: the successor is effective in the
	// log, under the very idempotency key this command would compute.
	successor := f.act(app.Act{
		Verb: app.VerbState, Kind: workroom.KindAssert,
		Text: "origin accepted " + second + " and notes/one.md changed within it.",
		Body: map[string]string{
			publicationBodyPath: "notes/one.md", publicationBodyHead: second,
			publicationBodyRemote: "origin", publicationBodyRef: "refs/heads/main",
		},
		RestsOn:        []string{f.basis, predecessor},
		IdempotencyKey: publicationAssertKey("origin", "refs/heads/main", "notes/one.md", second),
	})
	outboxPath := publicationOutboxPath(f.workspace.MetaDir, f.fingerprint)
	if err := savePublicationOutbox(outboxPath, publicationOutbox{
		Version: publicationOutboxV1, Actor: f.fingerprint,
		Batches: []publicationBatch{{
			Remote: "origin", Ref: "refs/heads/main", Before: first, Head: second, Basis: f.basis,
			Entries: []publicationEntry{{Path: "notes/one.md", Prior: predecessor, Event: successor, State: "pending"}},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if statementByEvent(t, f.snapshot().Projection, predecessor).Retired {
		t.Fatal("the fixture retired the predecessor before the run under test")
	}
	if f.frontier() != first {
		t.Fatalf("fixture frontier = %q, want %q", f.frontier(), first)
	}

	report := f.publish()
	if len(report.Published) != 1 || report.Published[0].Event != successor {
		t.Fatalf("recovered publication = %+v, want the recorded successor", report)
	}
	if report.Published[0].Outcome != "replayed" || report.Published[0].Retire == "" {
		t.Fatalf("recovered outcome = %+v, want a replayed entry carrying its retirement", report.Published[0])
	}
	projection := f.snapshot().Projection
	if !statementByEvent(t, projection, predecessor).Retired {
		t.Fatal("the recovered run left the predecessor live: visibility was treated as completion")
	}
	retirement := report.Published[0].Retire
	if decision, judged := projection.Decision(retirement); !judged || decision.Verdict != workroom.Effective {
		t.Fatalf("retirement decision = %+v, judged %v, want effective", decision, judged)
	}
	if !containsEvent(projection.Provenance[retirement], successor) {
		t.Fatalf("retirement bases = %#v, want the successor it names", projection.Provenance[retirement])
	}
	facts := livePublicationFacts(projection, "origin")
	if len(facts) != 1 || facts["notes/one.md"].Event != successor {
		t.Fatalf("live facts = %#v, want exactly the successor", facts)
	}
	if got := f.frontier(); got != second {
		t.Fatalf("frontier after the completed succession = %q, want %q", got, second)
	}
}

// The other half of the same rule: while the retirement is not yet effective,
// the batch is kept and the frontier stays where it was. Nothing is presented
// as published.
func TestAnUnfinishedRetirementHoldsTheBatchAndTheFrontier(t *testing.T) {
	f := newPublicationFixture(t)
	report, outbox, state, submissions := f.reconcileAgainstRetirementVerdict(t, nil)
	if report.err == nil {
		t.Fatal("an unfinished retirement was reported as complete")
	}
	if len(report.report.Published) != 1 || report.report.Published[0].Outcome != "pending" {
		t.Fatalf("unfinished retirement report = %+v", report.report)
	}
	if state.Observed[publicationFrontierKey("origin", "refs/heads/main")] != "" {
		t.Fatalf("an unfinished retirement advanced the shared frontier: %+v", state)
	}
	if len(outbox.Batches) != 1 || outbox.Batches[0].Entries[0].State != "pending" {
		t.Fatalf("an unfinished retirement settled its batch: %+v", outbox)
	}
	if outbox.Batches[0].Entries[0].Retire != publicationFixtureRetirement {
		t.Fatalf("the submitted retirement was not recorded durably: %+v", outbox.Batches[0].Entries[0])
	}
	// Phase one was already effective, so only the retirement was signed.
	if submissions != 1 {
		t.Fatalf("submissions = %d, want only the retirement", submissions)
	}
}

// An ineffective retirement is terminal — its idempotency key is spent — and
// it refuses. The successor stands, the succession does not, and the path is
// never presented as published.
func TestAnIneffectiveRetirementRefuses(t *testing.T) {
	f := newPublicationFixture(t)
	report, outbox, _, _ := f.reconcileAgainstRetirementVerdict(t, &workroom.Decision{
		Event: publicationFixtureRetirement, Verdict: workroom.Ineffective, Reason: "the fold refused this retirement"})
	if report.err != nil {
		t.Fatalf("abandoning an ineffective retirement returned %v, want the report to carry it", report.err)
	}
	outcome := report.report.Published
	if len(outcome) != 1 || outcome[0].Outcome != "abandoned" || !strings.Contains(outcome[0].Error, "the fold refused this retirement") {
		t.Fatalf("ineffective retirement report = %+v", report.report)
	}
	if outcome[0].Event != publicationFixtureSuccessor || outcome[0].Retire != publicationFixtureRetirement {
		t.Fatalf("the abandoned entry did not name both halves: %+v", outcome[0])
	}
	if !publicationUnfinished(report.report) {
		t.Fatal("an ineffective retirement exited zero")
	}
	if len(outbox.Batches) != 0 {
		t.Fatalf("an abandoned entry kept its batch alive: %+v", outbox)
	}
}

const (
	publicationFixtureSuccessor  = "git:sha1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa#git:sha1:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	publicationFixtureRetirement = "git:sha1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa#git:sha1:cccccccccccccccccccccccccccccccccccccccc"
)

type publicationRunResult struct {
	report publicationReport
	err    error
}

// reconcileAgainstRetirementVerdict drives one entry whose successor is
// already effective and whose retirement is not yet made, against a sequencer
// that returns the given decision for that retirement. A nil decision means
// the retirement never becomes visible.
func (f *publicationFixture) reconcileAgainstRetirementVerdict(t *testing.T, retirement *workroom.Decision) (publicationRunResult, publicationOutbox, publicationState, int32) {
	t.Helper()
	var submissions atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/v0/submit":
			submissions.Add(1)
			_ = json.NewEncoder(writer).Encode(app.Submission{
				Result: kernel.Result{Commit: strings.Repeat("b", 40)},
				Record: workroom.Record{ID: publicationFixtureRetirement}})
		case "/v0/inspect":
			var query statusview.InspectRequest
			if err := json.NewDecoder(request.Body).Decode(&query); err != nil {
				http.Error(writer, err.Error(), http.StatusBadRequest)
				return
			}
			answer := statusview.ItemInspection{Event: query.Event}
			switch query.Event {
			case publicationFixtureSuccessor:
				answer.Decision = &workroom.Decision{Event: query.Event, Verdict: workroom.Effective, Reason: "recorded"}
			case publicationFixtureRetirement:
				answer.Decision = retirement
			}
			_ = json.NewEncoder(writer).Encode(answer)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	outboxPath := publicationOutboxPath(f.workspace.MetaDir, f.fingerprint)
	statePath := publicationStatePath(f.workspace.MetaDir)
	outbox := publicationOutbox{Version: publicationOutboxV1, Actor: f.fingerprint, Batches: []publicationBatch{{
		Remote: "origin", Ref: "refs/heads/main", Head: strings.Repeat("d", 40), Basis: f.basis,
		Entries: []publicationEntry{{
			Path: "notes/one.md", Prior: "predecessor-event",
			Event: publicationFixtureSuccessor, State: "pending"}},
	}}}
	if err := savePublicationOutbox(outboxPath, outbox); err != nil {
		t.Fatal(err)
	}
	state := publicationState{Version: publicationStateV1, Observed: map[string]string{}}
	var report publicationReport
	err := reconcilePublicationOutbox(context.Background(), f.workspace, f.private, "operator", server.URL,
		outboxPath, &outbox, statePath, &state, &report)
	return publicationRunResult{report: report, err: err}, outbox, state, submissions.Load()
}

// ---------------------------------------------------------------------------
// Gate: literal idempotency keys, and Result.Replay reported as a replay.
// ---------------------------------------------------------------------------

func TestPublicationIdempotencyKeysAreLiteral(t *testing.T) {
	assertKey := publicationAssertKey("origin", "refs/heads/main", "notes/one.md", strings.Repeat("a", 40))
	wantAssert := "publication:" + publicationDigest("origin", "refs/heads/main", "notes/one.md", strings.Repeat("a", 40))
	if assertKey != wantAssert {
		t.Fatalf("assert idempotency key = %q, want %q", assertKey, wantAssert)
	}
	if !strings.HasPrefix(assertKey, "publication:") || len(assertKey) != len("publication:")+64 {
		t.Fatalf("assert idempotency key has an unexpected shape: %q", assertKey)
	}
	// The literal namespaces are distinct, so a retirement can never replay
	// against the fact it retires.
	retire := publicationRetireKey("event-a")
	withdraw := publicationWithdrawKey("event-a")
	if !strings.HasPrefix(retire, "publication-retire:") || !strings.HasPrefix(withdraw, "publication-withdraw:") || retire == withdraw {
		t.Fatalf("retirement keys = %q and %q", retire, withdraw)
	}
	// Every component of the assert key is load-bearing: changing any one of
	// the four must change the key, or two different facts share a replay
	// identity.
	base := []string{"origin", "refs/heads/main", "notes/one.md", strings.Repeat("a", 40)}
	for index := range base {
		changed := append([]string(nil), base...)
		changed[index] += "x"
		if publicationAssertKey(changed[0], changed[1], changed[2], changed[3]) == assertKey {
			t.Fatalf("component %d does not enter the idempotency key", index)
		}
	}
}

// A process death between a successful submission and the durable write of its
// event id is the one window that leaves the outbox not knowing what it did.
// The next run resubmits under the same key, the kernel replays rather than
// duplicating, and the report says "replayed" rather than "landed".
func TestPublicationReplaysARecoveredSubmissionRatherThanDuplicatingIt(t *testing.T) {
	f := newPublicationFixture(t)
	f.write(".gitseq", "watch notes/**.md\n")
	f.write("notes/one.md", "one\n")
	f.commit("one note")
	f.push("HEAD:main")

	interrupted := errors.New("simulated death between submit and durable write")
	publicationAfterSubmit = func() error { return interrupted }
	t.Cleanup(func() { publicationAfterSubmit = nil })
	if _, err := f.tryPublish(); !errors.Is(err, interrupted) {
		t.Fatalf("crash seam error = %v", err)
	}
	publicationAfterSubmit = nil

	outbox := f.outbox()
	if len(outbox.Batches) != 1 || outbox.Batches[0].Entries[0].State != "pending" || outbox.Batches[0].Entries[0].Event != "" {
		t.Fatalf("interrupted entry = %+v", outbox)
	}
	report := f.publish()
	if len(report.Published) != 1 || report.Published[0].Outcome != "replayed" {
		t.Fatalf("recovered publication = %+v, want one replayed outcome", report)
	}
	facts := 0
	for _, statement := range f.snapshot().Projection.Statements {
		if statement.Body[publicationBodyPath] == "notes/one.md" {
			facts++
		}
	}
	if facts != 1 {
		t.Fatalf("recovered publication left %d facts at one path, want one", facts)
	}
}

// ---------------------------------------------------------------------------
// Gate: published-path suppression on a NON-EMPTY diff. A lost frontier write
// leaves exactly this state, and a suppression that only ever ran on an empty
// diff was never executed at all.
// ---------------------------------------------------------------------------

func TestPublicationSuppressesAlreadyPublishedPathsOnANonEmptyDiff(t *testing.T) {
	f := newPublicationFixture(t)
	f.write(".gitseq", "watch notes/**.md\n")
	f.write("notes/one.md", "one\n")
	first := f.commit("first note")
	f.push("HEAD:main")
	if report := f.publish(); len(report.Published) != 1 {
		t.Fatalf("first publication = %+v", report)
	}
	f.write("notes/two.md", "two\n")
	second := f.commit("second note")
	f.push("HEAD:main")
	if report := f.publish(); len(report.Published) != 1 || report.Published[0].Path != "notes/two.md" {
		t.Fatalf("second publication = %+v", report)
	}

	// Rewind the shared frontier the way a crash between the durable acts and
	// the frontier write would.
	statePath := publicationStatePath(f.workspace.MetaDir)
	state, err := loadPublicationState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	state.Observed[publicationFrontierKey("origin", "refs/heads/main")] = first
	if err := savePublicationState(statePath, state); err != nil {
		t.Fatal(err)
	}

	// The diff really is non-empty: this is what the suppression has to run on.
	changes, err := changedPublicationPaths(context.Background(), f.repo, first, second)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) == 0 {
		t.Fatal("fixture produced an empty diff, so suppression would never execute")
	}

	report := f.publish()
	if len(report.Published) != 0 {
		t.Fatalf("already-published path was republished on a non-empty diff: %+v", report)
	}
	if report.Before != first {
		t.Fatalf("run did not start from the rewound frontier: %+v", report)
	}
	facts := 0
	for _, statement := range f.snapshot().Projection.Statements {
		if statement.Body[publicationBodyPath] == "notes/two.md" && !statement.Retired {
			facts++
		}
	}
	if facts != 1 {
		t.Fatalf("live facts at notes/two.md = %d, want one", facts)
	}
	// The frontier is restored, so the next run has nothing to re-derive.
	reloaded, err := loadPublicationState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Observed[publicationFrontierKey("origin", "refs/heads/main")] != second {
		t.Fatalf("frontier did not advance past a fully suppressed diff: %+v", reloaded)
	}
}

// ---------------------------------------------------------------------------
// Gate: exact ls-remote ref equality.
// ---------------------------------------------------------------------------

// `git ls-remote <remote> refs/heads/main` matches whole path components from
// the right, so a remote carrying only `refs/heads/foo/refs/heads/main`
// answers that query with one well-formed line naming a different branch.
// Only the exact ref equality separates the two.
func TestRemoteBranchHeadRequiresExactRefEquality(t *testing.T) {
	f := newPublicationFixture(t)
	f.write("a.txt", "a\n")
	head := f.commit("only commit")
	f.git("push", "-q", "origin", "HEAD:refs/heads/foo/refs/heads/main")

	if _, err := remoteBranchHead(context.Background(), f.repo, "origin", "refs/heads/main"); err == nil {
		t.Fatal("a suffix-matching branch was accepted as the exact ref")
	} else if !strings.Contains(err.Error(), "invalid exact head") {
		t.Fatalf("suffix-match refusal = %v", err)
	}

	// The real branch, alone, still resolves.
	f.git("push", "-q", "origin", "+HEAD:refs/heads/main")
	f.git("push", "-q", "origin", "--delete", "refs/heads/foo/refs/heads/main")
	got, err := remoteBranchHead(context.Background(), f.repo, "origin", "refs/heads/main")
	if err != nil || got != head {
		t.Fatalf("exact branch head = %q, err %v, want %q", got, err, head)
	}
}

// ---------------------------------------------------------------------------
// Gate: more than 256 watched paths, driven through the wired command, refused
// before anything is queued.
// ---------------------------------------------------------------------------

func TestPublishRefusesMoreThanTheBatchCeilingBeforeQueueing(t *testing.T) {
	f := newPublicationFixture(t)
	f.write(".gitseq", "watch notes/**.md\n")
	for index := 0; index <= publicationBatchLimit; index++ {
		f.write(fmt.Sprintf("notes/%04d.md", index), "note\n")
	}
	f.commit("one path over the ceiling")
	f.push("HEAD:main")

	err := publishCommand(context.Background(), []string{
		"--repo", f.repo, "--as", "operator", "--basis", f.basis, "--server", localFold,
	})
	if err == nil {
		t.Fatal("publication over the batch ceiling succeeded")
	}
	var failure *publicationError
	if !errors.As(err, &failure) || len(failure.report.Refused) != 1 || !strings.Contains(failure.report.Refused[0], "batch ceiling") {
		t.Fatalf("over-ceiling refusal = %v (%T)", err, err)
	}
	// Refused before queueing: no outbox file, and no durable act.
	if _, statErr := os.Stat(publicationOutboxPath(f.workspace.MetaDir, f.fingerprint)); !os.IsNotExist(statErr) {
		t.Fatalf("an over-ceiling batch was queued: %v", statErr)
	}
	for _, statement := range f.snapshot().Projection.Statements {
		if statement.Body[publicationBodyPath] != "" {
			t.Fatalf("an over-ceiling batch signed a fact: %+v", statement)
		}
	}
	// Exactly at the ceiling still publishes, so the test pins the boundary
	// rather than "many paths fail".
	if err := os.Remove(filepath.Join(f.repo, "notes", fmt.Sprintf("%04d.md", publicationBatchLimit))); err != nil {
		t.Fatal(err)
	}
	f.commit("back to the ceiling")
	f.push("HEAD:main")
	if report := f.publish(); len(report.Published) != publicationBatchLimit {
		t.Fatalf("at-ceiling publication published %d paths, want %d", len(report.Published), publicationBatchLimit)
	}
}

// ---------------------------------------------------------------------------
// Gate: bounded before the read, not after it.
// ---------------------------------------------------------------------------

// countingReader yields endless content and counts what was taken from it. A
// bound applied before reading takes exactly limit+1 bytes; a bound applied
// after reading takes everything there is.
type countingReader struct {
	remaining int64
	read      int64
}

func (r *countingReader) Read(buffer []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, io.EOF
	}
	size := int64(len(buffer))
	if size > r.remaining {
		size = r.remaining
	}
	for index := int64(0); index < size; index++ {
		buffer[index] = 'x'
	}
	r.remaining -= size
	r.read += size
	return int(size), nil
}

func TestReadBoundedAppliesTheBoundBeforeReading(t *testing.T) {
	const limit = 1 << 10
	source := &countingReader{remaining: limit * 64}
	if _, err := readBounded(source, limit, "publication outbox"); err == nil {
		t.Fatal("oversized content was accepted")
	} else if !strings.Contains(err.Error(), "larger than the 1024 bytes") {
		t.Fatalf("bound refusal = %v", err)
	}
	if source.read != limit+1 {
		t.Fatalf("read %d bytes before refusing, want exactly the bound plus one; the bound is being applied after the read", source.read)
	}

	// Content exactly at the limit still parses, which is why the reader takes
	// one byte past it rather than stopping at it.
	exact := &countingReader{remaining: limit}
	content, err := readBounded(exact, limit, "publication outbox")
	if err != nil || len(content) != limit {
		t.Fatalf("content exactly at the bound = %d bytes, err %v", len(content), err)
	}
}

// The bound is the one the file loaders actually use, end to end.
func TestPublicationOutboxRefusesAnOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox.json")
	if err := os.WriteFile(path, make([]byte, publicationOutboxLimit+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPublicationOutbox(path, "fingerprint"); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("oversized outbox refusal = %v", err)
	}
	if _, err := loadPublicationState(path); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("oversized state refusal = %v", err)
	}
}

// ---------------------------------------------------------------------------
// Gate: the resident's answer must be about the event that was asked for, and
// a durable write never falls back to the local projection.
// ---------------------------------------------------------------------------

func TestPublicationDecisionRequiresTheResidentToAnswerTheQuestion(t *testing.T) {
	f := newPublicationFixture(t)
	asked := "git:sha1:" + strings.Repeat("a", 40) + "#git:sha1:" + strings.Repeat("b", 40)
	other := "git:sha1:" + strings.Repeat("a", 40) + "#git:sha1:" + strings.Repeat("c", 40)

	var answer statusview.ItemInspection
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(answer)
	}))
	defer server.Close()

	// An envelope naming another event, carrying a verdict that does name the
	// queried one. Only the envelope check separates this from a good answer:
	// the decision check below is satisfied, so this case pins that guard on
	// its own.
	answer = statusview.ItemInspection{Event: other, Decision: &workroom.Decision{Event: asked, Verdict: workroom.Effective, Reason: "recorded"}}
	if _, judged, err := publicationDecision(context.Background(), f.workspace, server.URL, asked); judged || err == nil {
		t.Fatalf("a resident answer about another event was accepted: judged=%v err=%v", judged, err)
	} else if !strings.Contains(err.Error(), "resident answered for event") {
		t.Fatalf("mismatched-envelope refusal = %v", err)
	}

	// Both halves naming another event is refused too.
	answer = statusview.ItemInspection{Event: other, Decision: &workroom.Decision{Event: other, Verdict: workroom.Effective, Reason: "recorded"}}
	if _, judged, err := publicationDecision(context.Background(), f.workspace, server.URL, asked); judged || err == nil {
		t.Fatalf("a wholly mismatched answer was accepted: judged=%v err=%v", judged, err)
	}

	// The right envelope carrying a verdict about a different event. Only the
	// decision check separates this one.
	answer = statusview.ItemInspection{Event: asked, Decision: &workroom.Decision{Event: other, Verdict: workroom.Effective, Reason: "recorded"}}
	if _, judged, err := publicationDecision(context.Background(), f.workspace, server.URL, asked); judged || err == nil {
		t.Fatalf("a verdict about another event was accepted: judged=%v err=%v", judged, err)
	} else if !strings.Contains(err.Error(), "resident returned a decision about") {
		t.Fatalf("mismatched-decision refusal = %v", err)
	}

	// The right answer is accepted.
	answer = statusview.ItemInspection{Event: asked, Decision: &workroom.Decision{Event: asked, Verdict: workroom.Effective, Reason: "recorded"}}
	decision, judged, err := publicationDecision(context.Background(), f.workspace, server.URL, asked)
	if err != nil || !judged || decision.Verdict != workroom.Effective {
		t.Fatalf("matching answer = %+v, judged %v, err %v", decision, judged, err)
	}
}

// A durable write must not answer itself out of the local fold. The event
// below is genuinely in this repository's own projection, and a resident that
// refuses to answer for it still leaves the entry pending.
func TestPublicationDecisionNeverFallsBackToTheLocalProjection(t *testing.T) {
	f := newPublicationFixture(t)
	event := f.act(app.Act{Verb: app.VerbState, Kind: workroom.KindAssert, Text: "locally visible", RestsOn: []string{f.basis}})
	if _, judged, err := localPublicationDecision(context.Background(), f.workspace, event); err != nil || !judged {
		t.Fatalf("fixture event is not in the local projection: judged=%v err=%v", judged, err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Error(writer, "not observed", http.StatusNotFound)
	}))
	defer server.Close()
	if _, judged, err := publicationDecision(context.Background(), f.workspace, server.URL, event); judged || err == nil {
		t.Fatalf("a durable write fell back to the local projection: judged=%v err=%v", judged, err)
	}
}

// A refusing resident leaves the entry pending and reports a failure. It never
// reports success, and it never marks the entry done.
func TestPublicationRefusalIsNeverReportedAsSuccess(t *testing.T) {
	f := newPublicationFixture(t)
	event := "git:sha1:" + strings.Repeat("a", 40) + "#git:sha1:" + strings.Repeat("b", 40)
	var submissions atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/v0/submit":
			submissions.Add(1)
			_ = json.NewEncoder(writer).Encode(app.Submission{Result: kernel.Result{Commit: strings.Repeat("b", 40)}, Record: workroom.Record{ID: event}})
		default:
			http.Error(writer, "not observed", http.StatusNotFound)
		}
	}))
	defer server.Close()

	outboxPath, statePath, outbox, state := f.handQueue("notes/pending.md", strings.Repeat("c", 40), false)
	var report publicationReport
	if err := reconcilePublicationOutbox(context.Background(), f.workspace, f.private, "operator", server.URL, outboxPath, &outbox, statePath, &state, &report); err == nil {
		t.Fatal("an unobserved submission was reported as complete")
	}
	if len(report.Published) != 1 || report.Published[0].Outcome != "pending" {
		t.Fatalf("unobserved submission report = %+v", report)
	}
	reloaded, err := loadPublicationOutbox(outboxPath, f.fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	entry := reloaded.Batches[0].Entries[0]
	if entry.State != "pending" || entry.Event != event {
		t.Fatalf("unobserved submission was not retained as pending: %+v", entry)
	}
	if state.Observed[publicationFrontierKey("origin", "refs/heads/main")] != "" {
		t.Fatalf("an unfinished batch advanced the shared frontier: %+v", state)
	}
	if submissions.Load() != 1 {
		t.Fatalf("submissions = %d, want one", submissions.Load())
	}
}

// ---------------------------------------------------------------------------
// Condition 9: a permanently failed entry, and a retired actor's queue, both
// have a bounded exit instead of wedging the repository.
// ---------------------------------------------------------------------------

// An act the fold rules ineffective is terminal. Its idempotency key is spent,
// so no replay could reach a different verdict; it is abandoned, reported, and
// stops holding the shared frontier.
func TestPublicationAbandonsAnIneffectiveActAndReleasesTheFrontier(t *testing.T) {
	f := newPublicationFixture(t)
	event := "git:sha1:" + strings.Repeat("a", 40) + "#git:sha1:" + strings.Repeat("b", 40)
	head := strings.Repeat("c", 40)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/v0/submit":
			_ = json.NewEncoder(writer).Encode(app.Submission{Result: kernel.Result{Commit: strings.Repeat("b", 40)}, Record: workroom.Record{ID: event}})
		case "/v0/inspect":
			_ = json.NewEncoder(writer).Encode(statusview.ItemInspection{
				Event: event, Decision: &workroom.Decision{Event: event, Verdict: workroom.Ineffective, Reason: "the fold refused this act"}})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	outboxPath, statePath, outbox, state := f.handQueue("notes/refused.md", head, false)
	var report publicationReport
	if err := reconcilePublicationOutbox(context.Background(), f.workspace, f.private, "operator", server.URL, outboxPath, &outbox, statePath, &state, &report); err != nil {
		t.Fatalf("abandoning an ineffective act returned %v, want the report to carry it", err)
	}
	if len(report.Published) != 1 || report.Published[0].Outcome != "abandoned" || !strings.Contains(report.Published[0].Error, "the fold refused") {
		t.Fatalf("ineffective act report = %+v", report)
	}
	if !publicationUnfinished(report) {
		t.Fatal("an abandoned act was not counted as unfinished, so the command would exit zero")
	}
	if len(outbox.Batches) != 0 {
		t.Fatalf("an abandoned entry kept its batch alive: %+v", outbox)
	}
	if state.Observed[publicationFrontierKey("origin", "refs/heads/main")] != head {
		t.Fatalf("an abandoned entry held the shared frontier: %+v", state)
	}
}

// Transport failures retry, but not forever. After the attempt ceiling the
// entry is abandoned with the reason it kept failing for.
func TestPublicationRetriesAreBoundedByTheAttemptCeiling(t *testing.T) {
	f := newPublicationFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Error(writer, "resident is down", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	outboxPath, statePath, outbox, state := f.handQueue("notes/unreachable.md", strings.Repeat("c", 40), false)
	for attempt := 1; attempt <= publicationAttemptLimit; attempt++ {
		var report publicationReport
		if err := reconcilePublicationOutbox(context.Background(), f.workspace, f.private, "operator", server.URL, outboxPath, &outbox, statePath, &state, &report); err == nil {
			t.Fatalf("attempt %d did not report the transport failure", attempt)
		}
		if len(report.Published) != 1 || report.Published[0].Outcome != "pending" {
			t.Fatalf("attempt %d report = %+v", attempt, report)
		}
		if outbox.Batches[0].Entries[0].Attempts != attempt {
			t.Fatalf("attempt %d recorded %d attempts", attempt, outbox.Batches[0].Entries[0].Attempts)
		}
	}
	var final publicationReport
	if err := reconcilePublicationOutbox(context.Background(), f.workspace, f.private, "operator", server.URL, outboxPath, &outbox, statePath, &state, &final); err != nil {
		t.Fatalf("the run past the ceiling returned %v, want the report to carry it", err)
	}
	if len(final.Published) != 1 || final.Published[0].Outcome != "abandoned" || !strings.Contains(final.Published[0].Error, "gave up after") {
		t.Fatalf("past-ceiling report = %+v", final)
	}
	if len(outbox.Batches) != 0 {
		t.Fatalf("the abandoned batch was retained: %+v", outbox)
	}
}

// An entry abandoned at the attempt ceiling because every submission failed
// names no event: none was ever recorded. The outbox is fsynced in that state
// the moment the entry runs out of attempts, and the batch-settling loop that
// would clear it only runs after every remaining entry has been driven. A
// second entry still failing transport returns before that loop is reached, so
// the fsynced file is exactly what the next run loads — no process death
// required. That file must reload and settle, or publication is wedged by its
// own bounded-abandonment path.
func TestATerminalAbandonmentWithNoEventReloadsAndSettles(t *testing.T) {
	f := newPublicationFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Error(writer, "resident is down", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	head := strings.Repeat("c", 40)
	frontier := publicationFrontierKey("origin", "refs/heads/main")
	outboxPath, statePath, outbox, state := f.handQueueEntries(head,
		publicationEntry{Path: "notes/exhausted.md", State: "pending"},
		publicationEntry{Path: "notes/still-trying.md", State: "pending"})

	// Every pass returns on the first entry's transport failure, so only that
	// entry spends an attempt until it reaches the ceiling and is abandoned.
	for pass := 1; pass <= publicationAttemptLimit+1; pass++ {
		var report publicationReport
		if err := reconcilePublicationOutbox(context.Background(), f.workspace, f.private, "operator", server.URL,
			outboxPath, &outbox, statePath, &state, &report); err == nil {
			t.Fatalf("pass %d did not report the transport failure", pass)
		}
	}

	// The precondition is asserted here rather than assumed, and from the bytes
	// on disk rather than from memory: the file left behind carries a
	// terminally abandoned entry naming neither an event nor a retirement,
	// beside a pending entry that keeps the batch from being cleared.
	written := publicationOutbox{}
	content, err := os.ReadFile(outboxPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := decodePublicationJSON(content, &written); err != nil {
		t.Fatal(err)
	}
	if len(written.Batches) != 1 || len(written.Batches[0].Entries) != 2 {
		t.Fatalf("the outbox on disk = %+v", written)
	}
	exhausted, holding := written.Batches[0].Entries[0], written.Batches[0].Entries[1]
	if exhausted.State != "abandoned" || exhausted.Event != "" || exhausted.Retire != "" ||
		exhausted.Attempts != publicationAttemptLimit {
		t.Fatalf("the first entry is not a terminal abandonment carrying no event: %+v", exhausted)
	}
	if holding.State != "pending" {
		t.Fatalf("the second entry was expected to hold the batch open: %+v", holding)
	}

	// The defect: this file used to be refused for ever, so publication could
	// never resume.
	reloaded, err := loadPublicationOutbox(outboxPath, f.fingerprint)
	if err != nil {
		t.Fatalf("the outbox that terminal abandonment fsynced no longer loads: %v", err)
	}
	if len(reloaded.Batches) != 1 || len(reloaded.Batches[0].Entries) != 2 {
		t.Fatalf("the reloaded outbox lost work: %+v", reloaded)
	}

	// Reloadable is not enough; it has to settle. The second entry spends its
	// own attempts and is abandoned in turn, and only then does the batch clear.
	final := publicationReport{}
	for pass := 1; ; pass++ {
		if pass > publicationAttemptLimit+1 {
			t.Fatalf("the reloaded outbox never settled: %+v", reloaded)
		}
		final = publicationReport{}
		if err := reconcilePublicationOutbox(context.Background(), f.workspace, f.private, "operator", server.URL,
			outboxPath, &reloaded, statePath, &state, &final); err == nil {
			break
		}
	}
	if len(reloaded.Batches) != 0 {
		t.Fatalf("the settled batch was retained: %+v", reloaded)
	}
	if state.Observed[frontier] != head {
		t.Fatalf("a settled batch did not release the shared frontier: %+v", state)
	}
	// Nothing partial is ever presented as published, and the run still fails.
	if !publicationUnfinished(final) {
		t.Fatal("an abandoned batch was counted as finished, so the command would exit zero")
	}
	outcomes := outcomesByPath(final)
	for _, path := range []string{"notes/exhausted.md", "notes/still-trying.md"} {
		if outcomes[path].Outcome != "abandoned" {
			t.Fatalf("%s reported %+v, want abandoned", path, outcomes[path])
		}
	}
	// And the cleared file is loadable too, so the next run starts clean.
	if after, err := loadPublicationOutbox(outboxPath, f.fingerprint); err != nil || len(after.Batches) != 0 {
		t.Fatalf("the settled outbox on disk = %+v, %v", after, err)
	}
}

// The refusal narrows to terminal abandonment and to nothing else. An entry
// claiming completion still has to name what it recorded, and so does an
// abandonment that has attempts left, because the only way to abandon with no
// event at all is to exhaust them.
func TestAnEntryWithoutAnEventIsRefusedUnlessItExhaustedItsAttempts(t *testing.T) {
	cases := []struct {
		name    string
		entry   publicationEntry
		refused string
	}{
		{
			name:    "a done entry names no event",
			entry:   publicationEntry{Path: "notes/one.md", State: "done", Attempts: publicationAttemptLimit},
			refused: "completed entry without an event",
		},
		{
			name:    "an abandoned entry names no event and has attempts left",
			entry:   publicationEntry{Path: "notes/one.md", State: "abandoned", Attempts: publicationAttemptLimit - 1},
			refused: "did not exhaust its attempts",
		},
		{
			name:  "an abandoned entry names no event and exhausted its attempts",
			entry: publicationEntry{Path: "notes/one.md", State: "abandoned", Attempts: publicationAttemptLimit},
		},
		{
			name:  "a done entry names its event",
			entry: publicationEntry{Path: "notes/one.md", State: "done", Attempts: publicationAttemptLimit, Event: "event-id"},
		},
	}

	// Each case must be separated from the admitted one by exactly the field it
	// claims to pin, so no single mutation can break two cases for one reason.
	// The distinction is enforced here, not asserted in a comment.
	differOnlyIn := func(name string, first, second publicationEntry, clear func(*publicationEntry)) {
		t.Helper()
		if reflect.DeepEqual(first, second) {
			t.Fatalf("the cases meant to differ in %s are identical: %+v", name, first)
		}
		clear(&first)
		clear(&second)
		if !reflect.DeepEqual(first, second) {
			t.Fatalf("the cases meant to differ only in %s differ elsewhere: %+v and %+v", name, first, second)
		}
	}
	admitted := cases[2].entry
	differOnlyIn("State", cases[0].entry, admitted, func(entry *publicationEntry) { entry.State = "" })
	differOnlyIn("Attempts", cases[1].entry, admitted, func(entry *publicationEntry) { entry.Attempts = 0 })
	differOnlyIn("Event", cases[3].entry, cases[0].entry, func(entry *publicationEntry) { entry.Event = "" })

	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			outbox := publicationOutbox{Version: publicationOutboxV1, Actor: "fingerprint", Batches: []publicationBatch{{
				Remote: "origin", Ref: "refs/heads/main", Head: strings.Repeat("c", 40), Basis: "basis-event",
				Entries: []publicationEntry{item.entry},
			}}}
			err := validatePublicationOutbox(outbox)
			if item.refused == "" {
				if err != nil {
					t.Fatalf("%+v was refused: %v", item.entry, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), item.refused) {
				t.Fatalf("%+v gave %v, want a refusal naming %q", item.entry, err, item.refused)
			}
		})
	}
}

// A foreign queue blocks only when it shares this run's remote and ref, and
// only when its actor is still able to drain it.
func TestForeignPublicationBatchesBlockOnlyLiveActorsOnTheSameFrontier(t *testing.T) {
	directory := t.TempDir()
	current := filepath.Join(directory, "current.json")
	foreign := filepath.Join(directory, "other.json")
	batch := func(ref string) publicationBatch {
		return publicationBatch{Remote: "origin", Ref: ref, Head: strings.Repeat("d", 40), Basis: "basis-event",
			Entries: []publicationEntry{{Path: "notes/pending.md", State: "pending"}}}
	}
	live := workroom.Projection{Actors: map[string]workroom.ActorState{"other-fingerprint": {Name: "other"}}}
	retired := workroom.Projection{Actors: map[string]workroom.ActorState{"other-fingerprint": {Name: "other", Retired: true}}}

	write := func(batches ...publicationBatch) {
		t.Helper()
		if err := savePublicationOutbox(foreign, publicationOutbox{Version: publicationOutboxV1, Actor: "other-fingerprint", Batches: batches}); err != nil {
			t.Fatal(err)
		}
	}

	write(batch("refs/heads/main"))
	var report publicationReport
	if err := checkForeignPublicationBatches(directory, current, "origin", "refs/heads/main", live, &report); err == nil || !strings.Contains(err.Error(), "unresolved publication work") {
		t.Fatalf("a live actor's overlapping queue was not refused: %v", err)
	}

	// A different branch is a different frontier. Blocking it was a
	// repository-wide wedge with no relation to what this run touches.
	report = publicationReport{}
	if err := checkForeignPublicationBatches(directory, current, "origin", "refs/heads/other", live, &report); err != nil {
		t.Fatalf("a queue on another branch blocked publication: %v", err)
	}
	if len(report.Warnings) != 0 {
		t.Fatalf("a non-overlapping queue produced warnings: %#v", report.Warnings)
	}

	// A retired actor can no longer sign, so nobody could ever drain the
	// queue. It is reported as orphaned and stepped over, not refused.
	report = publicationReport{}
	if err := checkForeignPublicationBatches(directory, current, "origin", "refs/heads/main", retired, &report); err != nil {
		t.Fatalf("a retired actor's queue wedged publication with no exit: %v", err)
	}
	if len(report.Warnings) != 1 || !strings.Contains(report.Warnings[0], "ratifier must clear") {
		t.Fatalf("orphan warning = %#v", report.Warnings)
	}

	// An actor the roster has never heard of is orphaned for the same reason.
	report = publicationReport{}
	if err := checkForeignPublicationBatches(directory, current, "origin", "refs/heads/main", workroom.Projection{}, &report); err != nil {
		t.Fatalf("an unknown actor's queue wedged publication: %v", err)
	}
	if len(report.Warnings) != 1 {
		t.Fatalf("unknown-actor warning = %#v", report.Warnings)
	}

	// An empty queue never blocks anything.
	write()
	report = publicationReport{}
	if err := checkForeignPublicationBatches(directory, current, "origin", "refs/heads/main", live, &report); err != nil {
		t.Fatalf("an empty foreign queue blocked publication: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Condition 6: the command routes through resolveServerURL, so a repository
// that advertises a resident it cannot trust refuses before anything is read
// or signed.
// ---------------------------------------------------------------------------

func TestPublishRefusesAnUntrustworthyAdvertisement(t *testing.T) {
	f := newPublicationFixture(t)
	f.write(".gitseq", "watch notes/**.md\n")
	f.write("notes/one.md", "one\n")
	f.commit("one note")
	f.push("HEAD:main")

	// A record naming another workroom is present and cannot be trusted.
	foreign, err := json.Marshal(app.Resident{URL: "http://127.0.0.1:7788", Genesis: strings.Repeat("f", 40), PID: os.Getpid()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.workspace.MetaDir, "resident.json"), foreign, 0o600); err != nil {
		t.Fatal(err)
	}
	err = publishCommand(context.Background(), []string{"--repo", f.repo, "--as", "operator", "--basis", f.basis})
	if err == nil || !strings.Contains(err.Error(), "cannot be trusted") {
		t.Fatalf("publish over an untrustworthy advertisement = %v", err)
	}
	if _, statErr := os.Stat(publicationOutboxPath(f.workspace.MetaDir, f.fingerprint)); !os.IsNotExist(statErr) {
		t.Fatalf("the refused run still queued work: %v", statErr)
	}

	// Removing the record leaves an ordinary repository with no resident, and
	// the same command publishes locally.
	if err := os.Remove(filepath.Join(f.workspace.MetaDir, "resident.json")); err != nil {
		t.Fatal(err)
	}
	if err := publishCommand(context.Background(), []string{"--repo", f.repo, "--as", "operator", "--basis", f.basis}); err != nil {
		t.Fatalf("publish with no advertisement = %v", err)
	}
}

// ---------------------------------------------------------------------------
// Condition 10: `..` and its neighbours, decided and tested.
// ---------------------------------------------------------------------------

// Path matching here is pure string comparison with no normalisation, so
// `notes/../one.md` neither covers nor is covered by `one.md`. Admitting one
// would put two unrelatable live facts on one file. The decision is to refuse
// the shape, on both the paths and the globs that select them.
func TestPathTraversalIsRefusedSoNoShadowChainCanForm(t *testing.T) {
	for _, path := range []string{
		"notes/../one.md", "../one.md", "./one.md", "notes/./one.md",
		"notes//one.md", "/notes/one.md", "notes/one.md/", "..", ".",
	} {
		if err := validatePublicationPath(path); err == nil {
			t.Fatalf("accepted a traversable path %q", path)
		}
	}
	for _, path := range []string{"notes/one.md", "one.md", "a/b/c/..d.md", "notes/...md"} {
		if err := validatePublicationPath(path); err != nil {
			t.Fatalf("refused an ordinary path %q: %v", path, err)
		}
	}
	// The glob comes out of the head a remote accepted, so it is the other
	// half of the same door.
	for _, glob := range []string{"notes/../**", "../**", "./**", "notes//**", "/notes/**"} {
		if _, err := parsePublicationConfig([]byte("watch " + glob + "\n")); err == nil {
			t.Fatalf("accepted a traversable watch glob %q", glob)
		}
	}

	// The shape is unreachable through selection too, not only through the
	// validator, and it never reaches the queue.
	patterns, err := parsePublicationConfig([]byte("watch **\n"))
	if err != nil {
		t.Fatal(err)
	}
	paths, refused := selectWatchedPaths([]publicationChange{
		{Path: "notes/../one.md", Kind: 'A'}, {Path: "notes/one.md", Kind: 'A'},
	}, patterns)
	if !reflect.DeepEqual(paths, []string{"notes/one.md"}) {
		t.Fatalf("selected paths = %#v", paths)
	}
	if len(refused) != 1 || !strings.Contains(refused[0], "string-prefix path matching") {
		t.Fatalf("traversal refusal = %#v", refused)
	}
	outbox := publicationOutbox{Version: publicationOutboxV1, Actor: "fingerprint", Batches: []publicationBatch{{
		Remote: "origin", Ref: "refs/heads/main", Head: strings.Repeat("a", 40), Basis: "basis-event",
		Entries: []publicationEntry{{Path: "notes/../one.md", State: "pending"}},
	}}}
	if err := validatePublicationOutbox(outbox); err == nil {
		t.Fatal("a traversable path was queueable")
	}
}

// ---------------------------------------------------------------------------
// Condition 12: hostile inputs refuse only the bad names, errors stay bounded,
// and a refusal is never a success.
// ---------------------------------------------------------------------------

func TestPublicationHostilePathsRefuseOnlyTheBadNames(t *testing.T) {
	patterns, err := parsePublicationConfig([]byte("watch notes/**\n"))
	if err != nil {
		t.Fatal(err)
	}
	overlong := "notes/" + strings.Repeat("x", publicationPathLimit)
	paths, refused := selectWatchedPaths([]publicationChange{
		{Path: "notes/good.md", Kind: 'A'},
		{Path: "notes/new\nline.md", Kind: 'A'},
		{Path: "notes/two,names.md", Kind: 'A'},
		{Path: overlong, Kind: 'A'},
		{Path: "source/unwatched.go", Kind: 'M'},
	}, patterns)
	if !reflect.DeepEqual(paths, []string{"notes/good.md"}) {
		t.Fatalf("selected paths = %#v", paths)
	}
	joined := strings.Join(refused, "\n")
	if len(refused) != 3 || !strings.Contains(joined, "control byte") || !strings.Contains(joined, "exceeds") || !strings.Contains(joined, "comma") {
		t.Fatalf("refusals = %#v", refused)
	}
	// Nothing hostile reaches an unbounded error string.
	for _, message := range refused {
		if len([]rune(message)) > publicationReasonLimit+len("path exceeds 4096 bytes: \"\"…") {
			t.Fatalf("refusal is not bounded: %d runes", len([]rune(message)))
		}
	}
	if truncateForError(strings.Repeat("y", publicationReasonLimit*4)) == strings.Repeat("y", publicationReasonLimit*4) {
		t.Fatal("truncateForError left an oversized reason unbounded")
	}
	if err := validatePublicationRef("refs/heads/bad\nref"); err == nil {
		t.Fatal("accepted a ref containing a newline")
	}
	if err := validatePublicationRef("refs/heads/" + strings.Repeat("x", publicationRefLimit)); err == nil {
		t.Fatal("accepted a ref over the length ceiling")
	}
	if err := validatePublicationBatchSize(publicationBatchLimit + 1); err == nil {
		t.Fatal("accepted a publication over the batch ceiling")
	}
	if err := validatePublicationBatchSize(publicationBatchLimit); err != nil {
		t.Fatalf("refused a publication at the batch ceiling: %v", err)
	}
}

func TestPublicationConfigContainsWatchGlobsAndNothingElse(t *testing.T) {
	patterns, err := parsePublicationConfig([]byte("# decisions\nwatch notes/**.md\nwatch docs/decisions/**.md\n"))
	if err != nil {
		t.Fatal(err)
	}
	for candidate, want := range map[string]bool{
		"notes/one.md":                   true,
		"notes/nested/two.md":            true,
		"docs/decisions/accepted/a.md":   true,
		"notes/one.txt":                  false,
		"docs/reference/architecture.md": false,
	} {
		if matchesAnyPattern(patterns, candidate) != want {
			t.Fatalf("watch match for %q = %v, want %v", candidate, !want, want)
		}
	}
	for _, malformed := range []string{
		"status open\n", "watch\n", "watch /notes/*.md\n", "watch notes/[ab].md\n",
		"watch notes/line\nbreak.md\n", "watch notes/\xff.md\n", "",
	} {
		if _, err := parsePublicationConfig([]byte(malformed)); err == nil {
			t.Fatalf("accepted non-watch or malformed config %q", malformed)
		}
	}
	over := strings.Repeat("watch notes/**.md\n", publicationGlobLimit+1)
	if _, err := parsePublicationConfig([]byte(over)); err == nil {
		t.Fatal("accepted more watch globs than the ceiling")
	}
}

func TestPublicationRejectsInvalidUTF8BeforeQueueOrSigning(t *testing.T) {
	invalid := string([]byte{'r', 'e', 'f', 0xff})
	if err := validatePublicationRef("refs/heads/" + invalid); err == nil {
		t.Fatal("accepted an invalid UTF-8 ref")
	}
	if err := validatePublicationBasis("basis" + invalid); err == nil {
		t.Fatal("accepted an invalid UTF-8 basis")
	}
	if _, err := parsePublicationConfig([]byte("watch notes/\xff.md\n")); err == nil {
		t.Fatal("accepted invalid UTF-8 in tracked configuration")
	}
	patterns, err := parsePublicationConfig([]byte("watch notes/**\n"))
	if err != nil {
		t.Fatal(err)
	}
	paths, refused := selectWatchedPaths([]publicationChange{{Path: "notes/" + invalid, Kind: 'A'}}, patterns)
	if len(paths) != 0 || len(refused) != 1 || !strings.Contains(refused[0], "valid UTF-8") {
		t.Fatalf("invalid path selection = paths %#v, refused %#v", paths, refused)
	}

	f := newPublicationFixture(t)
	outboxPath, _, outbox, _ := f.handQueue("notes/pending.md", strings.Repeat("a", 40), false)
	if _, err := runPublication(context.Background(), f.workspace, f.private, "operator", f.fingerprint, "", "origin", "refs/heads/"+invalid, f.basis); err == nil {
		t.Fatal("an invalid ref reached publication")
	}
	reloaded, err := loadPublicationOutbox(outboxPath, f.fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Batches) != len(outbox.Batches) || reloaded.Batches[0].Entries[0].Event != "" {
		t.Fatalf("an invalid ref reconciled or changed a queued act: %+v", reloaded)
	}
}

func TestPublicationBadPathStillPublishesGoodPathAndReportsRefusal(t *testing.T) {
	f := newPublicationFixture(t)
	f.write(".gitseq", "watch notes/**\n")
	f.write("notes/good.md", "good\n")
	f.write("notes/bad\nname.md", "bad\n")
	f.commit("hostile path")
	f.push("HEAD:main")

	report, err := f.tryPublish()
	if err == nil {
		t.Fatal("a refused path exited zero")
	}
	if len(report.Refused) != 1 || len(report.Published) != 1 || report.Published[0].Path != "notes/good.md" {
		t.Fatalf("mixed hostile publication = %+v, err %v", report, err)
	}
	for _, statement := range f.snapshot().Projection.Statements {
		if strings.ContainsRune(statement.Body[publicationBodyPath], '\n') {
			t.Fatalf("a hostile path was published: %+v", statement)
		}
	}
}

func TestChangedPublicationPathsPublishesRenameDestinationAndNotDeletion(t *testing.T) {
	f := newPublicationFixture(t)
	f.write("old.md", "old\n")
	f.write("deleted.md", "delete me\n")
	first := f.commit("first")
	f.git("mv", "old.md", "new.md")
	f.git("rm", "-q", "deleted.md")
	head := f.commit("rename and delete")
	changes, err := changedPublicationPaths(context.Background(), f.repo, first, head)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, change := range changes {
		paths = append(paths, change.Path)
	}
	if !reflect.DeepEqual(paths, []string{"new.md"}) {
		t.Fatalf("changed publication paths = %#v, want the rename destination only", paths)
	}
	if _, err := changedPublicationPaths(context.Background(), f.repo, "not-an-oid", head); err == nil {
		t.Fatal("accepted a malformed previous head")
	}
}

// ---------------------------------------------------------------------------
// Condition 5: post-push proof, process identity, durable queue, per-path
// isolation, and the repository-wide watch-glob scope.
// ---------------------------------------------------------------------------

func TestPublicationReadsTheRemoteAndNotTheLocalCheckout(t *testing.T) {
	f := newPublicationFixture(t)
	f.write(".gitseq", "watch notes/**.md\n")
	f.write("notes/one.md", "one\n")
	first := f.commit("pushed note")
	f.push("HEAD:main")
	if report := f.publish(); len(report.Published) != 1 || report.Head != first {
		t.Fatalf("first publication = %+v", report)
	}
	// A local commit nobody pushed must not escape.
	f.write("notes/two.md", "unpushed\n")
	f.commit("unpushed note")
	report := f.publish()
	if report.Head != first || len(report.Published) != 0 {
		t.Fatalf("an unpushed local commit escaped: %+v", report)
	}
}

func TestPublishIgnoresTagsAndRefusesWithoutProcessIdentity(t *testing.T) {
	t.Setenv(actorEnvironment, "")
	if err := publishCommand(context.Background(), []string{"--repo", "/does/not/matter"}); err == nil || !strings.Contains(err.Error(), actorEnvironment) {
		t.Fatalf("publish without identity error = %v", err)
	}
	f := newPublicationFixture(t)
	report, err := runPublication(context.Background(), f.workspace, f.private, "operator", f.fingerprint, "", "origin", "refs/tags/v1", f.basis)
	if err != nil || report.Ignored != "tag ref" || len(report.Published) != 0 {
		t.Fatalf("tag publication = %+v, err %v", report, err)
	}
	if _, err := runPublication(context.Background(), f.workspace, f.private, "operator", f.fingerprint, "", "origin", "refs/notes/commits", f.basis); err == nil {
		t.Fatal("a ref outside refs/heads was published")
	}
	if _, err := runPublication(context.Background(), f.workspace, f.private, "operator", f.fingerprint, "", "origin", "refs/heads/main", ""); err == nil {
		t.Fatal("publication proceeded with no governing basis")
	}
}

func TestPublicationQueuesDurablyBeforeTheFirstAct(t *testing.T) {
	f := newPublicationFixture(t)
	f.write(".gitseq", "watch notes/**.md\n")
	f.write("notes/one.md", "queued\n")
	head := f.commit("queue before submit")
	f.push("HEAD:main")

	interrupted := errors.New("simulated process interruption")
	publicationAfterQueue = func() error { return interrupted }
	t.Cleanup(func() { publicationAfterQueue = nil })
	if _, err := f.tryPublish(); !errors.Is(err, interrupted) {
		t.Fatalf("queue interruption error = %v", err)
	}
	publicationAfterQueue = nil

	outbox := f.outbox()
	if len(outbox.Batches) != 1 || outbox.Batches[0].Head != head || outbox.Batches[0].Basis != f.basis || outbox.Batches[0].Entries[0].State != "pending" {
		t.Fatalf("interrupted publication was not durable: %+v", outbox)
	}
	for _, statement := range f.snapshot().Projection.Statements {
		if statement.Body[publicationBodyPath] != "" {
			t.Fatalf("an act was signed before the queue was durable: %+v", statement)
		}
	}
	if report := f.publish(); len(report.Published) != 1 {
		t.Fatalf("the next run did not reconcile the durable queue: %+v", report)
	}
}

// The shared frontier belongs to the repository, not to an actor: handing the
// next push to a different actor must not republish unchanged paths.
func TestPublicationBaselineSurvivesActorHandoff(t *testing.T) {
	f := newPublicationFixture(t)
	f.write(".gitseq", "watch notes/**.md\n")
	f.write("notes/one.md", "one\n")
	first := f.commit("first note")
	f.push("HEAD:main")
	if _, _, err := f.workspace.AddActor(context.Background(), "operator", "worker", "agent"); err != nil {
		t.Fatal(err)
	}
	worker, workerPrivate, err := f.workspace.Actor("worker")
	if err != nil {
		t.Fatal(err)
	}
	if report := f.publish(); len(report.Published) != 1 {
		t.Fatalf("operator publication = %+v", report)
	}
	report, err := runPublication(context.Background(), f.workspace, workerPrivate, "worker", worker.Fingerprint, "", "origin", "refs/heads/main", f.basis)
	if err != nil || len(report.Published) != 0 || report.Head != first {
		t.Fatalf("actor handoff republished unchanged paths: %+v, %v", report, err)
	}

	f.write("notes/two.md", "two\n")
	second := f.commit("second note")
	f.push("HEAD:main")
	report, err = runPublication(context.Background(), f.workspace, workerPrivate, "worker", worker.Fingerprint, "", "origin", "refs/heads/main", f.basis)
	if err != nil || len(report.Published) != 1 || report.Published[0].Path != "notes/two.md" || report.Head != second {
		t.Fatalf("actor handoff changed-path publication = %+v, %v", report, err)
	}
	counts := map[string]int{}
	for path, fact := range livePublicationFacts(f.snapshot().Projection, "origin") {
		counts[path+"@"+fact.Body[publicationBodyHead]]++
	}
	if counts["notes/one.md@"+first] != 1 || counts["notes/two.md@"+second] != 1 || len(counts) != 2 {
		t.Fatalf("actor handoff facts = %#v", counts)
	}
}

// One operating-system lock, taken through the host layer's own primitive,
// serialises publishers across linked worktrees of one repository.
func TestPublicationLockSerializesAcrossLinkedWorktrees(t *testing.T) {
	f := newPublicationFixture(t)
	f.write(".gitseq", "watch notes/**.md\n")
	f.write("notes/one.md", "one\n")
	head := f.commit("concurrent publication")
	f.push("HEAD:main")
	linked := filepath.Join(t.TempDir(), "linked")
	f.git("worktree", "add", "-q", "-b", "concurrent-publication", linked, head)
	linkedWorkspace, err := app.Open(context.Background(), linked)
	if err != nil {
		t.Fatal(err)
	}
	if f.workspace.MetaDir != linkedWorkspace.MetaDir {
		t.Fatalf("linked worktree state differs: %s != %s", f.workspace.MetaDir, linkedWorkspace.MetaDir)
	}
	_, linkedPrivate, err := linkedWorkspace.Actor("operator")
	if err != nil {
		t.Fatal(err)
	}

	firstLoaded := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondLoaded := make(chan struct{})
	var loads atomic.Int32
	publicationAfterLoad = func() {
		switch loads.Add(1) {
		case 1:
			close(firstLoaded)
			<-releaseFirst
		case 2:
			close(secondLoaded)
		}
	}
	t.Cleanup(func() { publicationAfterLoad = nil })

	type result struct {
		report publicationReport
		err    error
	}
	results := make(chan result, 2)
	go func() {
		report, err := runPublication(context.Background(), f.workspace, f.private, "operator", f.fingerprint, "", "origin", "refs/heads/main", f.basis)
		results <- result{report: report, err: err}
	}()
	<-firstLoaded
	go func() {
		report, err := runPublication(context.Background(), linkedWorkspace, linkedPrivate, "operator", f.fingerprint, "", "origin", "refs/heads/main", f.basis)
		results <- result{report: report, err: err}
	}()
	loadedEarly := false
	select {
	case <-secondLoaded:
		loadedEarly = true
	case <-time.After(150 * time.Millisecond):
	}
	close(releaseFirst)
	if loadedEarly {
		t.Fatal("the second publisher loaded state while the first held the transaction")
	}
	select {
	case <-secondLoaded:
	case <-time.After(30 * time.Second):
		t.Fatal("the second publisher did not acquire the released publication lock")
	}
	firstResult, secondResult := <-results, <-results
	if firstResult.err != nil || secondResult.err != nil {
		t.Fatalf("concurrent publication errors = %v, %v", firstResult.err, secondResult.err)
	}
	if len(firstResult.report.Published)+len(secondResult.report.Published) != 1 {
		t.Fatalf("serialised reports = %+v and %+v", firstResult.report, secondResult.report)
	}
	facts := 0
	for _, statement := range f.snapshot().Projection.Statements {
		if statement.Body[publicationBodyPath] == "notes/one.md" {
			facts++
		}
	}
	if facts != 1 {
		t.Fatalf("concurrent publication left %d facts, want one", facts)
	}
}

// ---------------------------------------------------------------------------
// Fixture
// ---------------------------------------------------------------------------

type publicationFixture struct {
	t           *testing.T
	repo        string
	remote      string
	workspace   *app.Workspace
	fingerprint string
	private     ed25519.PrivateKey
	basis       string
}

func newPublicationFixture(t *testing.T) *publicationFixture {
	t.Helper()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	remote := filepath.Join(root, "remote.git")
	publicationGit(t, "", "init", "--bare", "-q", remote)
	publicationGit(t, "", "init", "-q", "-b", "main", repo)
	publicationGit(t, repo, "config", "user.name", "Test")
	publicationGit(t, repo, "config", "user.email", "test@example.invalid")
	publicationGit(t, repo, "remote", "add", "origin", remote)
	workspace, _, err := app.Init(context.Background(), repo, "operator", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	actor, private, err := workspace.Actor("operator")
	if err != nil {
		t.Fatal(err)
	}
	return &publicationFixture{
		t: t, repo: repo, remote: remote, workspace: workspace,
		fingerprint: actor.Fingerprint, private: private,
		basis: workspace.EventID(workspace.View().Genesis),
	}
}

func (f *publicationFixture) write(name, content string) {
	f.t.Helper()
	path := filepath.Join(f.repo, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *publicationFixture) commit(message string) string {
	f.t.Helper()
	f.git("add", "-A")
	f.git("commit", "-q", "-m", message)
	return strings.TrimSpace(f.git("rev-parse", "HEAD"))
}

func (f *publicationFixture) push(refspec string) {
	f.t.Helper()
	f.git("push", "-q", "origin", refspec)
}

func (f *publicationFixture) git(arguments ...string) string {
	f.t.Helper()
	return publicationGit(f.t, f.repo, arguments...)
}

func (f *publicationFixture) publish() publicationReport {
	f.t.Helper()
	report, err := f.tryPublish()
	if err != nil {
		f.t.Fatalf("publish: %v (report %+v)", err, report)
	}
	return report
}

func (f *publicationFixture) tryPublish() (publicationReport, error) {
	return runPublication(context.Background(), f.workspace, f.private, "operator", f.fingerprint, "", "origin", "refs/heads/main", f.basis)
}

// addActor seeds a second signing principal, so a test can put two authors on
// one repository's publication wires.
func (f *publicationFixture) addActor(name string) (string, ed25519.PrivateKey) {
	f.t.Helper()
	if _, _, err := f.workspace.AddActor(context.Background(), "operator", name, "agent"); err != nil {
		f.t.Fatal(err)
	}
	actor, private, err := f.workspace.Actor(name)
	if err != nil {
		f.t.Fatal(err)
	}
	return actor.Fingerprint, private
}

func (f *publicationFixture) publishAs(name, fingerprint string, private ed25519.PrivateKey) (publicationReport, error) {
	f.t.Helper()
	return runPublication(context.Background(), f.workspace, private, name, fingerprint, "", "origin", "refs/heads/main", f.basis)
}

func (f *publicationFixture) frontier() string {
	f.t.Helper()
	state, err := loadPublicationState(publicationStatePath(f.workspace.MetaDir))
	if err != nil {
		f.t.Fatal(err)
	}
	return state.Observed[publicationFrontierKey("origin", "refs/heads/main")]
}

func (f *publicationFixture) snapshot() app.Snapshot {
	f.t.Helper()
	snapshot, err := f.workspace.Snapshot(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	return snapshot
}

func (f *publicationFixture) act(act app.Act) string {
	f.t.Helper()
	submission, err := f.workspace.Act(context.Background(), "operator", act)
	if err != nil {
		f.t.Fatal(err)
	}
	return submission.Record.ID
}

func (f *publicationFixture) outbox() publicationOutbox {
	f.t.Helper()
	outbox, err := loadPublicationOutbox(publicationOutboxPath(f.workspace.MetaDir, f.fingerprint), f.fingerprint)
	if err != nil {
		f.t.Fatal(err)
	}
	return outbox
}

// handQueue writes one pending entry directly, for the reconciliation paths
// that need a queue without a remote behind it.
func (f *publicationFixture) handQueue(path, head string, withdraw bool) (string, string, publicationOutbox, publicationState) {
	f.t.Helper()
	return f.handQueueEntries(head, publicationEntry{Path: path, State: "pending", Withdraw: withdraw})
}

// handQueueEntries is the same, for the paths that need more than one entry in
// the batch.
func (f *publicationFixture) handQueueEntries(head string, entries ...publicationEntry) (string, string, publicationOutbox, publicationState) {
	f.t.Helper()
	outboxPath := publicationOutboxPath(f.workspace.MetaDir, f.fingerprint)
	statePath := publicationStatePath(f.workspace.MetaDir)
	outbox := publicationOutbox{Version: publicationOutboxV1, Actor: f.fingerprint, Batches: []publicationBatch{{
		Remote: "origin", Ref: "refs/heads/main", Head: head, Basis: f.basis, Entries: entries,
	}}}
	if err := savePublicationOutbox(outboxPath, outbox); err != nil {
		f.t.Fatal(err)
	}
	return outboxPath, statePath, outbox, publicationState{Version: publicationStateV1, Observed: map[string]string{}}
}

func publicationGit(t *testing.T, repo string, arguments ...string) string {
	t.Helper()
	args := arguments
	if repo != "" {
		args = append([]string{"-C", repo}, arguments...)
	}
	command := exec.Command("git", args...)
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(arguments, " "), err, output)
	}
	return string(output)
}

func outcomesByPath(report publicationReport) map[string]publicationOutcome {
	byPath := make(map[string]publicationOutcome, len(report.Published))
	for _, outcome := range report.Published {
		byPath[outcome.Path] = outcome
	}
	return byPath
}

func containsEvent(events []string, target string) bool {
	for _, event := range events {
		if event == target {
			return true
		}
	}
	return false
}
