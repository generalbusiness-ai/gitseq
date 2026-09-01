package reviewguard

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/intent"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

const (
	head    = "1111111111111111111111111111111111111111"
	head256 = "2222222222222222222222222222222222222222222222222222222222222222"
)

// lane builds one durable review lane from statements listed in landing order.
// A statement without an explicit sequence takes its position in the list;
// explicit sequences let a test place records between and after the fixed
// request, promise, and artifact rows exactly as a concurrent workroom would.
func lane(provenance map[string][]string, statements ...workroom.Statement) workroom.Projection {
	numbered := make([]workroom.Statement, len(statements))
	copy(numbered, statements)
	for index := range numbered {
		if numbered[index].Sequence == 0 {
			numbered[index].Sequence = index + 1
		}
	}
	decisions := make([]workroom.Decision, 0, len(numbered))
	for _, statement := range numbered {
		verdict := workroom.Effective
		reason := "statement recorded"
		switch statement.Kind {
		case "whisper":
			verdict = workroom.UndefinedKind
			reason = `undefined kind "whisper"`
		case workroom.KindReport:
			if statement.Body["broken"] == "true" {
				verdict = workroom.Ineffective
				reason = "report has no promise or request"
			}
		}
		decisions = append(decisions, workroom.Decision{
			Event: statement.Event, Sequence: statement.Sequence,
			Verdict: verdict, Reason: reason,
		})
	}
	return workroom.Projection{Statements: numbered, Decisions: decisions, Provenance: provenance}
}

// reviewLane is the quiet shape every review starts from: ground, request,
// promise, artifact, each standing on the last.
func reviewLane(commit string, after ...workroom.Statement) workroom.Projection {
	statements := []workroom.Statement{
		{Event: "ground", Actor: "operator", Kind: workroom.KindArtifact, Body: map[string]string{"path": "base.txt", "commit": "9999999999999999999999999999999999999999"}},
		{Event: "request", Actor: "operator", Kind: workroom.KindRequest, Lifecycle: workroom.LifecycleRequest, Body: map[string]string{"to": "reviewer", "conditions": "exact head"}},
		{Event: "promise", Actor: "reviewer", Kind: workroom.KindPromise, Lifecycle: workroom.LifecyclePromise},
		{Event: "artifact", Actor: "implementer", Kind: workroom.KindArtifact, Body: map[string]string{"path": "feature.txt", "commit": commit}},
	}
	return lane(map[string][]string{
		"ground":   nil,
		"request":  {"ground"},
		"promise":  {"request"},
		"artifact": {"promise", "request"},
	}, append(statements, after...)...)
}

func quietTarget(head string) Target {
	return Target{Head: head, Request: "request", Promise: "promise"}
}

// The first failure mode this guard closes: news sequenced after the review
// request but before the review promise. A scan bounded by the promise never
// sees it; the request is the exclusive lower bound.
func TestDiscoverFindsNewsBetweenRequestAndPromise(t *testing.T) {
	fixture := lane(map[string][]string{
		"ground":   nil,
		"request":  {"ground"},
		"promise":  {"request"},
		"artifact": {"promise", "request"},
	},
		workroom.Statement{Event: "ground", Actor: "operator", Kind: workroom.KindArtifact, Body: map[string]string{"path": "base.txt", "commit": "9999999999999999999999999999999999999999"}},
		workroom.Statement{Event: "request", Actor: "operator", Kind: workroom.KindRequest, Lifecycle: workroom.LifecycleRequest, Body: map[string]string{"to": "reviewer", "conditions": "exact head"}},
		workroom.Statement{Event: "early", Actor: "stranger", Kind: workroom.KindAssert, Body: map[string]string{"note": "landed before the promise", "head": head}},
		workroom.Statement{Event: "promise", Actor: "reviewer", Kind: workroom.KindPromise, Lifecycle: workroom.LifecyclePromise},
		workroom.Statement{Event: "artifact", Actor: "implementer", Kind: workroom.KindArtifact, Body: map[string]string{"path": "feature.txt", "commit": head}},
	)
	var events []string
	for _, item := range Discover(fixture, quietTarget(head)) {
		events = append(events, item.Event)
	}
	// The promise and the artifact match through their direct bases; they are
	// cited by any verdict, so they never need separate attention. The point
	// here is `early`: sequenced before the promise, visible anyway.
	want := []string{"early", "promise", "artifact"}
	if got := strings.Join(events, ","); got != strings.Join(want, ",") {
		t.Fatalf("news around the request = [%s], want [%s]", got, strings.Join(want, ","))
	}
	if news := Discover(fixture, quietTarget(head)); news[0].Verdict != workroom.Effective || news[0].Reason != "statement recorded" {
		t.Fatalf("matched record lost its decision: %#v", news[0])
	}
}

func TestDiscoverMatchesStructuredHeadCommitAndDirectBasesForBothFormats(t *testing.T) {
	for _, test := range []struct {
		name  string
		head  string
		short bool
	}{{name: "sha1", head: head}, {name: "sha256", head: head256}} {
		t.Run(test.name, func(t *testing.T) {
			fixture := reviewLane(test.head,
				workroom.Statement{Event: "by-head", Actor: "stranger", Kind: workroom.KindAssert, Body: map[string]string{"head": test.head}},
				workroom.Statement{Event: "by-commit", Actor: "stranger", Kind: workroom.KindArtifact, Body: map[string]string{"path": "other.txt", "commit": test.head}},
				workroom.Statement{Event: "cites-request", Actor: "stranger", Kind: workroom.KindAssert},
				workroom.Statement{Event: "cites-artifact", Actor: "stranger", Kind: workroom.KindAssert},
				workroom.Statement{Event: "cites-late-artifact", Actor: "stranger", Kind: workroom.KindAssert},
				workroom.Statement{Event: "cites-ineffective-artifact", Actor: "stranger", Kind: workroom.KindAssert},
			)
			fixture.Provenance["cites-request"] = []string{"request"}
			fixture.Provenance["cites-artifact"] = []string{"artifact"}
			fixture.Provenance["cites-late-artifact"] = []string{"by-commit"}
			fixture.Provenance["cites-ineffective-artifact"] = []string{"refused-artifact"}
			// The projection lists only decision-effective artifacts, so
			// `refused-artifact` stays out however it spells itself. The
			// artifact itself still shows as news: its own structured commit
			// names the head and the guard shows matched ineffective records.
			fixture.Statements = append(fixture.Statements,
				workroom.Statement{Event: "refused-artifact", Actor: "implementer", Kind: workroom.KindArtifact, Sequence: len(fixture.Statements) + 1, Body: map[string]string{"path": "x", "commit": test.head}})
			fixture.Decisions = append(fixture.Decisions, workroom.Decision{
				Event: "refused-artifact", Sequence: len(fixture.Statements), Verdict: workroom.Ineffective, Reason: "not effective",
			})
			fixture.Artifacts = []workroom.Artifact{
				{Event: "artifact", Path: "feature.txt", Commit: test.head},
				{Event: "by-commit", Path: "other.txt", Commit: test.head},
			}
			news := Discover(fixture, quietTarget(test.head))
			var events []string
			for _, item := range news {
				events = append(events, item.Event)
			}
			// cites-late-artifact matches because its direct basis is an
			// effective artifact standing at the reviewed head. A basis the
			// fold judged ineffective matches nothing through that clause.
			want := "promise,artifact,by-head,by-commit,cites-request,cites-artifact,cites-late-artifact,refused-artifact"
			if got := strings.Join(events, ","); got != want {
				t.Fatalf("news = [%s], want [%s]", got, want)
			}
			shown := news[len(news)-1]
			if shown.Event != "refused-artifact" || shown.Verdict != workroom.Ineffective || shown.Reason != "not effective" {
				t.Fatalf("matched ineffective record lost its decision: %#v", shown)
			}
		})
	}
}

func TestDiscoverIgnoresProseShortHashesPathAtCommitAndOtherCommits(t *testing.T) {
	fixture := reviewLane(head,
		workroom.Statement{Event: "prose", Actor: "stranger", Kind: workroom.KindAssert, Body: map[string]string{"note": "the head " + head + " looks good"}},
		workroom.Statement{Event: "short", Actor: "stranger", Kind: workroom.KindAssert, Body: map[string]string{"head": head[:8]}},
		workroom.Statement{Event: "path-at-commit", Actor: "stranger", Kind: workroom.KindAssert, Body: map[string]string{"pointer": "feature.txt@" + head}},
		workroom.Statement{Event: "other-commit", Actor: "stranger", Kind: workroom.KindArtifact, Body: map[string]string{"path": "x", "commit": strings.Repeat("a", 40)}},
	)
	fixture.Provenance["prose"] = nil
	fixture.Provenance["short"] = nil
	fixture.Provenance["path-at-commit"] = nil
	fixture.Provenance["other-commit"] = nil
	var events []string
	for _, item := range Discover(fixture, quietTarget(head)) {
		events = append(events, item.Event)
	}
	for _, noise := range []string{"prose", "short", "path-at-commit", "other-commit"} {
		if strings.Contains(strings.Join(events, ","), noise) {
			t.Fatalf("non-structured spelling %q was read as news: %v", noise, events)
		}
	}
}

// The scan is bounded below by the review request alone: a head-naming
// statement placed at every other position of the lane shows whether each gap
// reads as news. Positions before the request stay outside however loudly they
// name the head; every position after it comes in, in sequence order.
func TestDiscoverFollowsTheNewsSetThroughEveryPosition(t *testing.T) {
	statements := []workroom.Statement{
		{Event: "ground", Actor: "operator", Kind: workroom.KindArtifact, Sequence: 10, Body: map[string]string{"path": "base.txt", "commit": "9999999999999999999999999999999999999999"}},
		{Event: "request", Actor: "operator", Kind: workroom.KindRequest, Lifecycle: workroom.LifecycleRequest, Sequence: 20, Body: map[string]string{"to": "reviewer"}},
		{Event: "promise", Actor: "reviewer", Kind: workroom.KindPromise, Lifecycle: workroom.LifecyclePromise, Sequence: 30},
		{Event: "artifact", Actor: "implementer", Kind: workroom.KindArtifact, Sequence: 40, Body: map[string]string{"path": "feature.txt", "commit": head}},
	}
	provenance := map[string][]string{
		"ground":   nil,
		"request":  {"ground"},
		"promise":  {"request"},
		"artifact": {"promise", "request"},
	}
	for _, test := range []struct {
		name     string
		sequence int
		want     string
	}{
		{name: "before the ground", sequence: 5, want: "promise,artifact"},
		{name: "after the ground before the request", sequence: 15, want: "promise,artifact"},
		{name: "between the request and the promise", sequence: 25, want: "stranger,promise,artifact"},
		{name: "between the promise and the artifact", sequence: 35, want: "promise,stranger,artifact"},
		{name: "after the artifact", sequence: 45, want: "promise,artifact,stranger"},
	} {
		t.Run(test.name, func(t *testing.T) {
			stranger := workroom.Statement{Event: "stranger", Actor: "stranger", Kind: workroom.KindAssert, Sequence: test.sequence, Body: map[string]string{"head": head}}
			placed := make([]workroom.Statement, 0, len(statements)+1)
			inserted := false
			for _, statement := range statements {
				if !inserted && statement.Sequence > test.sequence {
					placed = append(placed, stranger)
					inserted = true
				}
				placed = append(placed, statement)
			}
			if !inserted {
				placed = append(placed, stranger)
			}
			var events []string
			for _, item := range Discover(lane(provenance, placed...), quietTarget(head)) {
				events = append(events, item.Event)
			}
			if got := strings.Join(events, ","); got != test.want {
				t.Fatalf("news with a statement at #%d = [%s], want [%s]", test.sequence, got, test.want)
			}
		})
	}
}

func TestDiscoverShowsIneffectiveRetiredAndUndefinedKindRecords(t *testing.T) {
	fixture := reviewLane(head,
		workroom.Statement{Event: "undefined", Actor: "stranger", Kind: "whisper", Body: map[string]string{"head": head}},
		workroom.Statement{Event: "retired", Actor: "stranger", Kind: workroom.KindAssert, Retired: true, Body: map[string]string{"head": head}},
		workroom.Statement{Event: "broken-report", Actor: "stranger", Kind: workroom.KindReport, Body: map[string]string{"head": head, "broken": "true"}},
	)
	shown := make(map[string]News)
	for _, item := range Discover(fixture, quietTarget(head)) {
		shown[item.Event] = item
	}
	if shown["undefined"].Verdict != workroom.UndefinedKind || !strings.Contains(shown["undefined"].Reason, "whisper") {
		t.Fatalf("undefined-kind record was not shown with its decision: %#v", shown["undefined"])
	}
	if shown["retired"].Verdict != workroom.Effective || shown["retired"].Reason != "statement recorded" {
		t.Fatalf("retired record kept its row but not its decision: %#v", shown["retired"])
	}
	if shown["broken-report"].Verdict != workroom.Ineffective {
		t.Fatalf("ineffective report was not shown with its decision: %#v", shown["broken-report"])
	}
}

func TestRequiredAcknowledgmentsCountsCitedNewsOnce(t *testing.T) {
	news := []News{{Event: "one"}, {Event: "two"}, {Event: "three"}}
	citations := []string{"promise", "request", "artifact", "one"}
	required := RequiredAcknowledgments(news, citations)
	if got := strings.Join(required, ","); got != "two,three" {
		t.Fatalf("required acknowledgments = [%s], want [two,three]", got)
	}
}

func TestValidateAcknowledgmentsHoldsTheExactSet(t *testing.T) {
	news := []News{{Event: "one"}, {Event: "two"}, {Event: "three"}}
	citations := []string{"promise", "request", "artifact", "one"}
	if err := ValidateAcknowledgments(news, citations, []string{"two", "three"}); err != nil {
		t.Fatalf("exact acknowledgments refused: %v", err)
	}
	if err := ValidateAcknowledgments(news, citations, nil); err == nil || !strings.Contains(err.Error(), `missing: "two", "three"`) {
		t.Fatalf("missing acknowledgment error = %v", err)
	}
	if err := ValidateAcknowledgments(news, citations, []string{"two", "three", "four"}); err == nil || !strings.Contains(err.Error(), `extraneous: "four"`) {
		t.Fatalf("extraneous acknowledgment error = %v", err)
	}
	if err := ValidateAcknowledgments(news, citations, []string{"two", "two"}); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("duplicate acknowledgment error = %v", err)
	}
	// Overlap deduplication: an already-cited event counts once, so supplying
	// it again is extraneous rather than missing, and citing it changes the
	// required set by exactly that one name.
	if err := ValidateAcknowledgments(news, []string{"promise", "one"}, []string{"two"}); err == nil ||
		!strings.Contains(err.Error(), `missing: "three"`) {
		t.Fatalf("overlap deduplication error = %v", err)
	}
	if err := ValidateAcknowledgments(news, []string{"promise", "one"}, []string{"one", "two", "three"}); err == nil ||
		!strings.Contains(err.Error(), `extraneous: "one"`) {
		t.Fatalf("re-acknowledging a cited event error = %v", err)
	}
}

func TestCanonicalAcknowledgmentsIsSequenceOrderedJSON(t *testing.T) {
	encoded, err := CanonicalAcknowledgments([]News{{Event: "a", Sequence: 3}, {Event: "b", Sequence: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if encoded != `["b","a"]` {
		t.Fatalf("canonical array = %s, want sequence order whatever the input order", encoded)
	}
	var decoded []string
	if err := json.Unmarshal([]byte(encoded), &decoded); err != nil || len(decoded) != 2 {
		t.Fatalf("canonical array is not JSON: %s %v", encoded, err)
	}
	if empty, err := CanonicalAcknowledgments(nil); err != nil || empty != `[]` {
		t.Fatalf("empty news encodes as %q err %v, want []", empty, err)
	}
}

func TestBuildAppendsAcksAndRefusesCapacityWithoutTruncation(t *testing.T) {
	basis := Basis{Head: head, Request: "request", Promise: "promise", Frontier: "git:sha1:g#git:sha1:h1"}
	body, restsOn, err := Build(basis, VerdictApproved, "approved exact head", []string{"artifact"},
		[]News{{Event: "extra-one"}, {Event: "extra-two"}})
	if err != nil {
		t.Fatal(err)
	}
	if body["review_path"] != ReviewPath || body["verdict"] != VerdictApproved || body["head"] != head ||
		body["artifact"] != "artifact" || body["review_frontier"] != basis.Frontier {
		t.Fatalf("built body = %#v", body)
	}
	if body["head_news_acknowledged"] != `["extra-one","extra-two"]` {
		t.Fatalf("acknowledged array = %#v", body["head_news_acknowledged"])
	}
	want := []string{"promise", "request", "artifact", "extra-one", "extra-two"}
	if got := strings.Join(restsOn, ","); got != strings.Join(want, ",") {
		t.Fatalf("rests_on = [%s], want [%s]", got, strings.Join(want, ","))
	}
	if _, held := body["stale"]; held {
		t.Fatalf("an unmoved world recorded staleness: %#v", body)
	}
	var overflow []News
	for index := 0; len(overflow) < intent.MaxCausalReferences; index++ {
		overflow = append(overflow, News{Event: fmt.Sprintf("%s%06d", strings.Repeat("n", 34), index)})
	}
	if _, _, err := Build(basis, VerdictApproved, "text", []string{"artifact"}, overflow); err == nil ||
		!strings.Contains(err.Error(), "without truncation") {
		t.Fatalf("capacity overflow error = %v", err)
	}
	if _, _, err := Build(basis, "needs-work", "text", []string{"artifact"}, nil); err == nil ||
		!strings.Contains(err.Error(), "must be approved or changes-requested") {
		t.Fatalf("unknown verdict word error = %v", err)
	}
}

func TestBuildRecordsStalenessWhenTheWorldMoved(t *testing.T) {
	basis := Basis{Head: head, Request: "r", Promise: "p", Staleness: "artifact stale", Frontier: "f"}
	body, restsOn, err := Build(basis, VerdictChangesRequested, "changes requested", []string{"a"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if body["stale"] != "true" || body["staleness"] != "artifact stale" {
		t.Fatalf("moved-world verdict body = %#v", body)
	}
	if got := strings.Join(restsOn, ","); got != "p,r,a" {
		t.Fatalf("quiet-news rests_on = [%s], want [p,r,a]", got)
	}
}

// The second historical failure mode: news arriving after preflight but before
// the final write. EvaluateVerdict stands in for the post-dedup admission
// guard judging the newer projection the event would actually join.
// quietReview is a review lane whose artifact row is projected, which is what
// SplitVerdictBases and the artifact clause of discovery read.
func quietReview() workroom.Projection {
	fixture := reviewLane(head)
	fixture.Artifacts = []workroom.Artifact{{Event: "artifact", Path: "feature.txt", Commit: head}}
	return fixture
}

func TestEvaluateVerdictRefusesLateArrivingUncitedNews(t *testing.T) {
	fixture := quietReview()
	basis := Basis{Head: head, Request: "request", Promise: "promise", Frontier: "frontier-one"}
	body, restsOn, err := Build(basis, VerdictApproved, "approved exact head", []string{"artifact"}, Discover(fixture, quietTarget(head)))
	if err != nil {
		t.Fatal(err)
	}
	if err := EvaluateVerdict(fixture, body, restsOn, "frontier-one"); err != nil {
		t.Fatalf("quiet world refused: %v", err)
	}
	moved := reviewLane(head, workroom.Statement{Event: "late", Actor: "stranger", Kind: workroom.KindAssert, Body: map[string]string{"head": head}})
	moved.Artifacts = fixture.Artifacts
	err = EvaluateVerdict(moved, body, restsOn, "frontier-one")
	if err == nil || !strings.Contains(err.Error(), "late") || !strings.Contains(err.Error(), "not acknowledged") {
		t.Fatalf("late news error = %v", err)
	}
}

// The mirror mutation: news the verdict acknowledged at confirmation is gone
// from the world the write would join, so the recorded array no longer matches
// what that frontier shows and the event refuses rather than chaining onto a
// world shaped differently from the one the reviewer read.
func TestEvaluateVerdictRefusesANewsSetThatShrankAfterConfirmation(t *testing.T) {
	moved := reviewLane(head, workroom.Statement{Event: "late", Actor: "stranger", Kind: workroom.KindAssert, Body: map[string]string{"head": head}})
	moved.Artifacts = []workroom.Artifact{{Event: "artifact", Path: "feature.txt", Commit: head}}
	basis := Basis{Head: head, Request: "request", Promise: "promise", Frontier: "frontier-one"}
	body, restsOn, err := Build(basis, VerdictApproved, "approved exact head", []string{"artifact"}, Discover(moved, quietTarget(head)))
	if err != nil {
		t.Fatal(err)
	}
	if err := EvaluateVerdict(moved, body, restsOn, "frontier-one"); err != nil {
		t.Fatalf("the confirmed world refused its own verdict: %v", err)
	}
	err = EvaluateVerdict(quietReview(), body, restsOn, "frontier-one")
	if err == nil || !strings.Contains(err.Error(), "canonical news array") {
		t.Fatalf("shrunken news set error = %v", err)
	}
}

func TestEvaluateVerdictBindsTheWriteToTheObservedFrontier(t *testing.T) {
	fixture := quietReview()
	basis := Basis{Head: head, Request: "request", Promise: "promise", Frontier: "frontier-one"}
	body, restsOn, err := Build(basis, VerdictApproved, "approved exact head", []string{"artifact"}, Discover(fixture, quietTarget(head)))
	if err != nil {
		t.Fatal(err)
	}
	if err := EvaluateVerdict(fixture, body, restsOn, "frontier-two"); err == nil ||
		!strings.Contains(err.Error(), "world moved after the verdict was confirmed") {
		t.Fatalf("frontier mismatch error = %v", err)
	}
	body["review_frontier"] = "frontier-two"
	if err := EvaluateVerdict(fixture, body, restsOn, "frontier-two"); err != nil {
		t.Fatalf("matching frontier refused: %v", err)
	}
}

func TestEvaluateVerdictRejectsHandFiledVerdictsAndBrokenEncodings(t *testing.T) {
	fixture := quietReview()
	basis := Basis{Head: head, Request: "request", Promise: "promise", Frontier: "f"}
	body, restsOn, err := Build(basis, VerdictApproved, "text", []string{"artifact"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	handFiled := cloneBody(body)
	delete(handFiled, "review_path")
	if err := EvaluateVerdict(fixture, handFiled, restsOn, "f"); err == nil || !strings.Contains(err.Error(), ReviewPath) {
		t.Fatalf("hand-filed verdict error = %v", err)
	}
	badArray := cloneBody(body)
	badArray["head_news_acknowledged"] = `["nonsense"]`
	if err := EvaluateVerdict(fixture, badArray, restsOn, "f"); err == nil || !strings.Contains(err.Error(), "canonical news array") {
		t.Fatalf("broken acknowledgment array error = %v", err)
	}
	noHead := cloneBody(body)
	delete(noHead, "head")
	if err := EvaluateVerdict(fixture, noHead, restsOn, "f"); err == nil || !strings.Contains(err.Error(), "body.head is required") {
		t.Fatalf("missing head error = %v", err)
	}
	if _, _, _, err := SplitVerdictBases(fixture, []string{"promise"}); err == nil {
		t.Fatal("bases without a request and artifact were accepted")
	}
	if _, _, _, err := SplitVerdictBases(func() workroom.Projection {
		double := reviewLane(head)
		double.Provenance["second-request"] = []string{"request"}
		double.Statements = append(double.Statements,
			workroom.Statement{Event: "second-request", Actor: "operator", Kind: workroom.KindRequest, Lifecycle: workroom.LifecycleRequest})
		return double
	}(), []string{"promise", "request", "second-request", "artifact"}); err == nil || !strings.Contains(err.Error(), "more than one request") {
		t.Fatalf("two requests error = %v", err)
	}
}

func cloneBody(input map[string]string) map[string]string {
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func TestStandingHelpersRefuseWithdrawnPointers(t *testing.T) {
	fixture := reviewLane(head)
	fixture.Artifacts = []workroom.Artifact{{Event: "artifact", Path: "feature.txt", Commit: head}}
	if _, err := StandingArtifact(fixture, "artifact"); err != nil {
		t.Fatalf("live artifact refused: %v", err)
	}
	retired := fixture
	retired.Artifacts = []workroom.Artifact{{Event: "artifact", Path: "feature.txt", Commit: head, Retired: true}}
	if _, err := StandingArtifact(retired, "artifact"); err == nil || !strings.Contains(err.Error(), "retired") {
		t.Fatalf("retired artifact error = %v", err)
	}
	if _, err := StandingStatement(fixture, "promise", workroom.KindPromise); err != nil {
		t.Fatalf("standing promise refused: %v", err)
	}
	if _, err := StandingStatement(fixture, "promise", workroom.KindRequest); err == nil ||
		!strings.Contains(err.Error(), "is promise, want request") {
		t.Fatalf("wrong-kind lookup error = %v", err)
	}
	if _, err := UniqueStandingBasis(fixture, "promise", workroom.KindRequest); err != nil {
		t.Fatalf("unique request lookup failed: %v", err)
	}
	if DecisionVerdict(fixture, "missing") != workroom.Ineffective {
		t.Fatal("unknown events must answer ineffective, never effective")
	}
}

// Caller-controlled citation and acknowledgment values reach error text only
// quoted and bounded, whatever size or shape the caller made them.
func TestErrorSitesBoundCallerSuppliedValues(t *testing.T) {
	huge := strings.Repeat("x", 5000)
	prefix := fmt.Sprintf("%q", huge[:echoCap])

	if _, err := CheckCitations([]string{"artifact", huge, huge}); err == nil ||
		!strings.Contains(err.Error(), "twice") || !strings.Contains(err.Error(), prefix) ||
		len(err.Error()) > 300 {
		t.Fatalf("duplicate citation error not bounded and diagnostic: %v", err)
	}

	fixture := reviewLane(head)
	other := strings.Repeat("2", 40)
	fixture.Artifacts = []workroom.Artifact{{Event: "artifact", Path: "feature.txt", Commit: other}}
	err := ValidateSet(fixture, head, []string{"artifact"})
	if err == nil || !strings.Contains(err.Error(), "not at the reviewed head") || len(err.Error()) > 400 {
		t.Fatalf("cited-set error not bounded and diagnostic: %v", err)
	}

	body := map[string]string{
		"review_path":            ReviewPath,
		"head":                   head,
		"head_news_acknowledged": `["` + huge + `"]`,
		"review_frontier":        "frontier",
	}
	restsOn := []string{"promise", "request", "artifact"}
	err = EvaluateVerdict(quietReview(), body, restsOn, "frontier")
	if err == nil || !strings.Contains(err.Error(), "not the canonical news array") ||
		!strings.Contains(err.Error(), strings.Repeat("x", 100)) || len(err.Error()) > 300 {
		t.Fatalf("canonical-array error not bounded and diagnostic: %v", err)
	}

	body["head_news_acknowledged"] = `["promise","artifact"]`
	body["review_frontier"] = strings.Repeat("9", 5000)
	err = EvaluateVerdict(quietReview(), body, restsOn, "frontier")
	if err == nil || !strings.Contains(err.Error(), "review_frontier") || len(err.Error()) > 300 {
		t.Fatalf("frontier error not bounded and diagnostic: %v", err)
	}
}

// otherCommit is a head no review names; artifacts standing there are lane
// news when they rest on the reviewed artifact, and nothing more.
const otherCommit = "3333333333333333333333333333333333333333"

// readOf serves one fixed world to Confirm exactly as the filing surfaces
// do: the basis and news come from ReviewBasis over the same projection the
// resident will judge, so the two halves are compared on one world.
func readOf(projection workroom.Projection) ReadFunc {
	return func() (Basis, []News, workroom.Projection, error) {
		basis, news, err := ReviewBasis(Read{
			Projection: projection, ReviewerFingerprint: "reviewer",
			FrontierEvent: "frontier", NoCheckout: true,
		}, "artifact", "promise")
		return basis, news, projection, err
	}
}

// reportedShape is the lane from the 2026-09-01 bug report: a note artifact
// at another commit rests on the reviewed artifact, and a later statement
// rests on that note. The note is news; the statement under it is not.
func reportedShape() workroom.Projection {
	fixture := reviewLane(head,
		workroom.Statement{Event: "note", Actor: "stranger", Kind: workroom.KindArtifact, Body: map[string]string{"path": "notes/review.md", "commit": otherCommit}},
		workroom.Statement{Event: "downstream", Actor: "stranger", Kind: workroom.KindAssert, Body: map[string]string{"note": "rests on the note only"}},
	)
	fixture.Provenance["note"] = []string{"artifact"}
	fixture.Provenance["downstream"] = []string{"note"}
	fixture.Artifacts = []workroom.Artifact{
		{Event: "artifact", Path: "feature.txt", Commit: head},
		{Event: "note", Path: "notes/review.md", Commit: otherCommit},
	}
	return fixture
}

// The regression for that report. The client computes exactly one
// acknowledgment, the note, and refuses more as extraneous; the verdict it
// builds must then be the verdict the resident admits. Before the fix the
// resident read the acknowledged note into the lane, counted `downstream` as
// unacknowledged news, and refused a verdict no acknowledgment set could
// repair.
func TestEvaluateVerdictAdmitsTheClientSetWhenArtifactNewsRestsOnTheReviewedArtifact(t *testing.T) {
	fixture := reportedShape()
	read := readOf(fixture)
	_, _, err := Confirm(read, []string{"artifact"}, []string{"note", "downstream"}, VerdictApproved, "approved")
	if err == nil || !strings.Contains(err.Error(), `extraneous: "downstream"`) {
		t.Fatalf("the client accepted a statement under the note as news: %v", err)
	}
	body, restsOn, err := Confirm(read, []string{"artifact"}, []string{"note"}, VerdictApproved, "approved exact head")
	if err != nil {
		t.Fatalf("the client refused its own required set: %v", err)
	}
	if got := strings.Join(restsOn, ","); got != "promise,request,artifact,note" {
		t.Fatalf("rests_on = [%s], want the note acknowledged as a citation and nothing under it", got)
	}
	if err := EvaluateVerdict(fixture, body, restsOn, "frontier"); err != nil {
		t.Fatalf("the resident refused the verdict the client built: %v", err)
	}
}

// Both halves must derive one news set for one world, whatever shape the
// lane has taken since the request: the client's required acknowledgments
// are then exactly what the resident demands, and the canonical array the
// client signed is the array the resident re-derives.
func TestClientAndResidentDeriveTheSameNewsSet(t *testing.T) {
	for _, test := range []struct {
		name    string
		fixture func() workroom.Projection
		acks    []string
	}{
		{name: "quiet lane", fixture: quietReview},
		{name: "assert naming the head", fixture: func() workroom.Projection {
			fixture := quietReview()
			fixture.Statements = append(fixture.Statements, workroom.Statement{Event: "late", Sequence: 5, Actor: "stranger", Kind: workroom.KindAssert, Body: map[string]string{"head": head}})
			fixture.Decisions = append(fixture.Decisions, workroom.Decision{Event: "late", Sequence: 5, Verdict: workroom.Effective, Reason: "statement recorded"})
			return fixture
		}, acks: []string{"late"}},
		{name: "artifact news at another commit with a dependent", fixture: reportedShape, acks: []string{"note"}},
		{name: "artifact news at the reviewed head with a dependent", fixture: func() workroom.Projection {
			fixture := reviewLane(head,
				workroom.Statement{Event: "sibling", Actor: "implementer", Kind: workroom.KindArtifact, Body: map[string]string{"path": "other.txt", "commit": head}},
				workroom.Statement{Event: "under-sibling", Actor: "stranger", Kind: workroom.KindAssert},
			)
			fixture.Provenance["sibling"] = []string{"request"}
			fixture.Provenance["under-sibling"] = []string{"sibling"}
			fixture.Artifacts = []workroom.Artifact{
				{Event: "artifact", Path: "feature.txt", Commit: head},
				{Event: "sibling", Path: "other.txt", Commit: head},
			}
			return fixture
		}, acks: []string{"sibling", "under-sibling"}},
		{name: "statement resting on the promise", fixture: func() workroom.Projection {
			fixture := reviewLane(head, workroom.Statement{Event: "on-promise", Actor: "stranger", Kind: workroom.KindAssert})
			fixture.Provenance["on-promise"] = []string{"promise"}
			fixture.Artifacts = []workroom.Artifact{{Event: "artifact", Path: "feature.txt", Commit: head}}
			return fixture
		}, acks: []string{"on-promise"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := test.fixture()
			body, restsOn, err := Confirm(readOf(fixture), []string{"artifact"}, test.acks, VerdictApproved, "approved exact head")
			if err != nil {
				t.Fatalf("client refused acknowledgments %v: %v", test.acks, err)
			}
			if err := EvaluateVerdict(fixture, body, restsOn, "frontier"); err != nil {
				t.Fatalf("resident refused the client-built verdict: %v", err)
			}
		})
	}
}
