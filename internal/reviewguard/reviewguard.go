// Package reviewguard owns the guarded review verdict path: head-news
// discovery, exact-set acknowledgment validation, canonical verdict encoding,
// and act construction for `gs review` and the MCP review tool.
//
// Everything a filing surface needs to decide what a verdict must say lives
// here exactly once, so the command line and the tool cannot drift; both
// surfaces only parse their arguments and submit what this package builds.
package reviewguard

import (
	"cmp"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/generalbusiness-ai/gitseq/internal/apphost"
	"github.com/generalbusiness-ai/gitseq/internal/intent"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// ReviewPath is the value of the reserved body.review_path field carried by
// every verdict this package builds. A generic state write may not supply it;
// its presence marks an act as having come through the guarded pipeline.
const ReviewPath = "reviewguard@1"

// Verdict words a review report may carry.
const (
	VerdictApproved         = "approved"
	VerdictChangesRequested = "changes-requested"
)

// echoCap bounds how many runes of a caller-controlled value an error quotes
// back. Full canonical event and object IDs are far shorter, so useful
// diagnostics survive whole; arbitrary argument text cannot inflate an error,
// however large the caller makes it.
const echoCap = 120

// quoted renders one caller-controlled value for an error message: JSON
// quoting escapes control characters so no argument can forge error lines,
// and the cap keeps any oversized value from inflating the message.
func quoted(value string) string {
	runes := []rune(value)
	if len(runes) > echoCap {
		runes = runes[:echoCap]
		return strconv.Quote(string(runes)) + "..."
	}
	return strconv.Quote(string(runes))
}

// IsVerdictWord reports whether a body field carries one of the two review
// verdicts. Both spellings count wherever they appear on a report.
func IsVerdictWord(value string) bool {
	return value == VerdictApproved || value == VerdictChangesRequested
}

// Basis is everything a signed verdict has to name: the exact immutable head
// reviewed, the request the review answers and the promise under which the
// reviewer took it, whatever had moved underneath while the reviewer read,
// and the canonical frontier event the confirming read observed.
type Basis struct {
	Head      string
	Request   string
	Promise   string
	Staleness string
	Frontier  string
}

// News is one durable statement sequenced after the review request that names
// the reviewed head or the review lane itself. It is attention, not authority:
// matched records are shown with the decision the fold gave them, whatever
// that decision was.
type News struct {
	Event    string
	Sequence int
	Actor    string
	Kind     workroom.Kind
	Verdict  workroom.Verdict
	Reason   string
}

// Target names what one guarded review is about: the exact reviewed head and
// the request and promise that make up its lane. The co-signed artifacts are
// not listed here, because every one of them is an effective artifact standing
// at Head, and discovery already reads any such artifact as part of the lane.
type Target struct {
	Head    string
	Request string
	Promise string
}

// Discover returns the head news for one review: every state statement
// sequenced strictly after the review request that either carries the full
// reviewed object ID in a structured body.head or body.commit field, or whose
// direct rests_on names the review request, the review promise, or an
// effective artifact standing at the reviewed head. Matched
// records are returned in sequence order and include ineffective, retired,
// and undefined-kind records, each with its fold decision.
//
// Nothing here reads prose, abbreviated hashes, or path@commit spellings: a
// match is exact equality against a full sha1 or sha256 object ID, and the
// path@commit case is the structured artifact citation.
func Discover(projection workroom.Projection, target Target) []News {
	boundary := sequenceOf(projection, target.Request)
	var news []News
	for _, statement := range projection.Statements {
		if statement.Sequence <= boundary {
			continue
		}
		if !matches(statement, projection, target) {
			continue
		}
		news = append(news, News{
			Event: statement.Event, Sequence: statement.Sequence,
			Actor: statement.Actor, Kind: statement.Kind,
			Verdict: decisionOf(projection, statement.Event),
			Reason:  reasonOf(projection, statement.Event),
		})
	}
	return news
}

// matches applies the two news rules to one statement. Provenance entries are
// the statement's direct rests_on and nothing more.
func matches(statement workroom.Statement, projection workroom.Projection, target Target) bool {
	if statement.Body["head"] == target.Head || statement.Body["commit"] == target.Head {
		return true
	}
	for _, basis := range projection.Provenance[statement.Event] {
		if basis == target.Request || basis == target.Promise {
			return true
		}
		if effectiveArtifactAt(projection, basis, target.Head) {
			return true
		}
	}
	return false
}

// effectiveArtifactAt reports whether the named basis is an effective
// artifact statement standing at exactly the reviewed head.
func effectiveArtifactAt(projection workroom.Projection, event, head string) bool {
	if decisionOf(projection, event) != workroom.Effective {
		return false
	}
	for _, artifact := range projection.Artifacts {
		if artifact.Event == event && artifact.Commit == head {
			return true
		}
	}
	return false
}

func sequenceOf(projection workroom.Projection, event string) int {
	for _, decision := range projection.Decisions {
		if decision.Event == event {
			return decision.Sequence
		}
	}
	return 0
}

func decisionOf(projection workroom.Projection, event string) workroom.Verdict {
	for _, decision := range projection.Decisions {
		if decision.Event == event {
			return decision.Verdict
		}
	}
	return ""
}

func reasonOf(projection workroom.Projection, event string) string {
	for _, decision := range projection.Decisions {
		if decision.Event == event {
			return decision.Reason
		}
	}
	return ""
}

// RequiredAcknowledgments names the news a verdict's own citations do not
// already carry. The promise, the request, and the co-signed artifacts are
// cited by the verdict anyway, so they count once there and need no separate
// acknowledgment.
func RequiredAcknowledgments(news []News, citations []string) []string {
	var required []string
	for _, item := range news {
		if !slices.Contains(citations, item.Event) {
			required = append(required, item.Event)
		}
	}
	return required
}

// ValidateAcknowledgments holds supplied acknowledgments to the exact required
// set: none missing, none duplicated, none extraneous. An empty supplied list
// is correct only when every piece of news is already cited.
func ValidateAcknowledgments(news []News, citations, supplied []string) error {
	seen := make(map[string]bool, len(supplied))
	cleaned := make([]string, 0, len(supplied))
	for _, event := range supplied {
		if seen[event] {
			return fmt.Errorf("head-news acknowledgment %s is given twice", quoted(event))
		}
		seen[event] = true
		cleaned = append(cleaned, event)
	}
	required := RequiredAcknowledgments(news, citations)
	var missing, extraneous []string
	for _, event := range required {
		if !seen[event] {
			missing = append(missing, event)
		}
	}
	for _, event := range cleaned {
		if !slices.Contains(required, event) {
			extraneous = append(extraneous, event)
		}
	}
	if len(missing) != 0 || len(extraneous) != 0 {
		list := func(events []string) string {
			shown := make([]string, len(events))
			for index, event := range events {
				shown[index] = quoted(event)
			}
			return strings.Join(shown, ", ")
		}
		detail := ""
		if len(missing) != 0 {
			detail += " missing: " + list(missing) + ";"
		}
		if len(extraneous) != 0 {
			detail += " extraneous: " + list(extraneous) + ";"
		}
		return fmt.Errorf("head news sequenced after the review request is not acknowledged;%s pass --ack-head-news for exactly the events named", detail)
	}
	return nil
}

// CanonicalAcknowledgments encodes the acknowledged event IDs as the canonical
// JSON array recorded in body.head_news_acknowledged: sequence order, no
// duplicates. The stable sequence sort keeps the encoding canonical whatever
// order the caller's news arrived in.
func CanonicalAcknowledgments(news []News) (string, error) {
	ordered := slices.Clone(news)
	slices.SortStableFunc(ordered, func(first, second News) int {
		return cmp.Compare(first.Sequence, second.Sequence)
	})
	events := make([]string, 0, len(ordered))
	for _, item := range ordered {
		events = append(events, item.Event)
	}
	encoded, err := json.Marshal(events)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// SplitVerdictBases classifies a verdict's causal references by the lifecycle
// and render the fold bound to them. A guarded verdict answers exactly one
// promise through its governing request, and cites at least the primary
// artifact.
func SplitVerdictBases(projection workroom.Projection, restsOn []string) (promise, request string, artifacts []string, err error) {
	effective := make(map[string]bool)
	lifecycles := make(map[string]workroom.Lifecycle)
	for _, statement := range projection.Statements {
		lifecycles[statement.Event] = statement.Lifecycle
	}
	for _, decision := range projection.Decisions {
		effective[decision.Event] = decision.Verdict == workroom.Effective
	}
	for _, reference := range restsOn {
		switch lifecycles[reference] {
		case workroom.LifecyclePromise:
			if promise != "" {
				return "", "", nil, fmt.Errorf("verdict cites %s and more than one promise", promise)
			}
			promise = reference
		case workroom.LifecycleRequest:
			if request != "" {
				return "", "", nil, fmt.Errorf("verdict cites %s and more than one request", request)
			}
			request = reference
		default:
			if effective[reference] {
				for _, artifact := range projection.Artifacts {
					if artifact.Event == reference {
						artifacts = append(artifacts, reference)
						break
					}
				}
			}
		}
	}
	if promise == "" || request == "" || len(artifacts) == 0 {
		return "", "", nil, errors.New("verdict bases do not name one promise, one request, and at least one artifact")
	}
	return promise, request, artifacts, nil
}

// Build constructs the guarded verdict act: its signed body and its causal
// references. Acknowledged news beyond the verdict's own citations is appended
// to rests_on, so acknowledgment means resting on what was seen. The whole
// reference list must stay within intent.MaxCausalReferences; overflow refuses
// rather than truncating.
func Build(basis Basis, verdict, text string, artifacts []string, news []News) (map[string]string, []string, error) {
	if !IsVerdictWord(verdict) {
		return nil, nil, fmt.Errorf("review --verdict must be %s or %s", VerdictApproved, VerdictChangesRequested)
	}
	if len(artifacts) == 0 {
		return nil, nil, errors.New("review requires at least one artifact citation")
	}
	acknowledged, err := CanonicalAcknowledgments(news)
	if err != nil {
		return nil, nil, err
	}
	body := map[string]string{
		"verdict":                verdict,
		"head":                   basis.Head,
		"artifact":               artifacts[0],
		"review_path":            ReviewPath,
		"review_frontier":        basis.Frontier,
		"head_news_acknowledged": acknowledged,
	}
	if basis.Staleness != "" {
		body["stale"] = "true"
		body["staleness"] = basis.Staleness
	}
	citations := append([]string{basis.Promise, basis.Request}, artifacts...)
	restsOn := append(citations, RequiredAcknowledgments(news, citations)...)
	if len(restsOn) > intent.MaxCausalReferences {
		return nil, nil, fmt.Errorf("promise, request, artifacts, and acknowledgments total %d references, over the %d a signed act may carry; refuse without truncation", len(restsOn), intent.MaxCausalReferences)
	}
	return body, restsOn, nil
}

// EvaluateVerdict is the authoritative judgement of a verdict-shaped report at
// the pre-sequence frontier. It re-derives the head news from the projection
// of the world the event would join and refuses unless the verdict already
// acknowledges all of it: every matched event cited, the canonical
// acknowledgment array exact, and the recorded frontier the very frontier the
// event extends. Movement between confirmation and sequencing therefore
// refuses here instead of chaining the verdict onto a world it never saw.
func EvaluateVerdict(projection workroom.Projection, body map[string]string, restsOn []string, frontierEvent string) error {
	if body["review_path"] != ReviewPath {
		return fmt.Errorf("a review verdict must be filed through gs review or the MCP review tool (%s), not written by hand", ReviewPath)
	}
	head := body["head"]
	if head == "" {
		return errors.New("review verdict body.head is required")
	}
	promise, request, _, err := SplitVerdictBases(projection, restsOn)
	if err != nil {
		return err
	}
	// The target is the lane the filing surface read, and nothing the verdict
	// acknowledged: an acknowledged artifact at another commit is a citation,
	// not part of the lane, and reading it in would demand acknowledgment of
	// its own dependents, which the surface never showed.
	news := Discover(projection, Target{Head: head, Request: request, Promise: promise})
	var uncited []string
	for _, item := range news {
		if !slices.Contains(restsOn, item.Event) {
			uncited = append(uncited, item.Event)
		}
	}
	if len(uncited) != 0 {
		return fmt.Errorf("head news arrived after the review request and is not acknowledged: %s; rerun gs review or the review tool to see it", strings.Join(uncited, ", "))
	}
	canonical, err := CanonicalAcknowledgments(news)
	if err != nil {
		return err
	}
	if body["head_news_acknowledged"] != canonical {
		return fmt.Errorf("body.head_news_acknowledged %s is not the canonical news array %s observed at this frontier", quoted(body["head_news_acknowledged"]), quoted(canonical))
	}
	if body["review_frontier"] != frontierEvent {
		return fmt.Errorf("body.review_frontier %s is not the frontier this event would extend (%s); the world moved after the verdict was confirmed", quoted(body["review_frontier"]), quoted(frontierEvent))
	}
	return nil
}

// SameRead compares two guarded reads: the named basis exactly, and the news
// by its canonical encoding, so a statement landing between the reads is a
// difference even when nothing else moved.
func SameRead(a Basis, aNews []News, b Basis, bNews []News) bool {
	if a != b {
		return false
	}
	aEncoded, _ := CanonicalAcknowledgments(aNews)
	bEncoded, _ := CanonicalAcknowledgments(bNews)
	return aEncoded == bEncoded
}

// Read is everything one basis read starts from. Callers take it from a
// verified snapshot and their own custody; nothing here opens a workspace.
// NoCheckout is for surfaces that hold no working tree, such as the MCP
// review tool: the reviewed head then comes from the durable artifact row,
// which is immutable, rather than from a checkout's HEAD.
type Read struct {
	Projection          workroom.Projection
	ReviewerFingerprint string
	Checkout            string
	CommonDir           string
	FrontierEvent       string
	NoCheckout          bool
}

// ReviewBasis admits a review of a world that has moved. Retirement and
// ineffectiveness still refuse: a withdrawn pointer names nothing to review,
// and neither does an act that never took force. Staleness does not refuse,
// because deciding whether the movement matters to this exact commit is the
// reviewer's work. The returned news is part of the basis: callers comparing
// two reads compare the news alongside Basis so a statement landing between
// the reads is seen rather than missed.
func ReviewBasis(read Read, artifactEvent, promiseEvent string) (Basis, []News, error) {
	projection := read.Projection
	artifact, err := StandingArtifact(projection, artifactEvent)
	if err != nil {
		return Basis{}, nil, err
	}
	implementation, err := StandingStatement(projection, artifactEvent, workroom.KindArtifact)
	if err != nil {
		return Basis{}, nil, fmt.Errorf("reviewed artifact: %w", err)
	}
	promise, err := StandingStatement(projection, promiseEvent, workroom.KindPromise)
	if err != nil {
		return Basis{}, nil, err
	}
	if promise.Actor != read.ReviewerFingerprint {
		return Basis{}, nil, errors.New("review actor did not make the named promise")
	}
	// Independence is a property of fingerprints, not of names.
	if implementation.Actor == read.ReviewerFingerprint {
		return Basis{}, nil, errors.New("review actor signed the artifact under review; an independent reviewer must sign the verdict")
	}
	request, err := UniqueStandingBasis(projection, promiseEvent, workroom.KindRequest)
	if err != nil {
		return Basis{}, nil, fmt.Errorf("review promise: %w", err)
	}
	if !read.NoCheckout {
		if err := ValidateCheckout(read.Checkout, artifact.Commit, read.CommonDir); err != nil {
			return Basis{}, nil, err
		}
	}
	basis := Basis{
		Head: artifact.Commit, Request: request.Event, Promise: promise.Event,
		Staleness: StalenessNote(projection, []Part{
			{Name: "artifact", Event: artifact.Event, Stale: artifact.Stale, World: artifact.DescribesSupersededWorld},
			{Name: "promise", Event: promise.Event, Stale: promise.Stale, World: promise.DescribesSupersededWorld},
			{Name: "request", Event: request.Event, Stale: request.Stale, World: request.DescribesSupersededWorld},
		}),
		Frontier: read.FrontierEvent,
	}
	news := Discover(projection, Target{Head: basis.Head, Request: basis.Request, Promise: basis.Promise})
	return basis, news, nil
}

// Part is one thing a review stands on, with the two staleness facts the
// projection keeps about it.
type Part struct {
	Name  string
	Event string
	Stale bool
	World bool
}

// stalenessCauseCap bounds the causes a verdict body names. A verdict is a
// message, not a projection: past a handful of retired bases a reader goes to
// gs provenance.
const stalenessCauseCap = 4

// StalenessNote says in one line what had moved under a review: the stale
// parts, then whether the movement was in the world they describe rather than
// the argument they stand on, then the retired bases themselves.
func StalenessNote(projection workroom.Projection, parts []Part) string {
	var moved, roots []string
	world := false
	for _, part := range parts {
		if !part.Stale {
			continue
		}
		moved = append(moved, part.Name)
		roots = append(roots, part.Event)
		world = world || part.World
	}
	if len(moved) == 0 {
		return ""
	}
	note := strings.Join(moved, ", ") + " stale"
	if world {
		note += "; describes a superseded world"
	}
	causes := RetiredBases(projection, roots)
	if len(causes) == 0 {
		return note
	}
	suffix := ""
	if len(causes) > stalenessCauseCap {
		suffix = fmt.Sprintf(" and %d more", len(causes)-stalenessCauseCap)
		causes = causes[:stalenessCauseCap]
	}
	return note + "; retired bases: " + strings.Join(causes, ", ") + suffix
}

// RetiredBases walks provenance from the given events down to the retired
// statements underneath them: the nearest acts a reader can act on.
// Breadth-first over a visited set, so a shared ancestor is named once and a
// diamond terminates.
func RetiredBases(projection workroom.Projection, events []string) []string {
	retired := make(map[string]bool)
	for _, statement := range projection.Statements {
		if statement.Retired {
			retired[statement.Event] = true
		}
	}
	seen := make(map[string]bool, len(events))
	queue := append([]string(nil), events...)
	for _, event := range events {
		seen[event] = true
	}
	var found []string
	for len(queue) > 0 {
		event := queue[0]
		queue = queue[1:]
		for _, basis := range projection.Provenance[event] {
			if seen[basis] {
				continue
			}
			seen[basis] = true
			if retired[basis] {
				found = append(found, basis)
				continue
			}
			queue = append(queue, basis)
		}
	}
	slices.Sort(found)
	return found
}

// ValidateSet holds every co-signed artifact to the primary's standard: live,
// effective, and naming exactly the reviewed head. Staleness of the reviewed
// pointers is not refused here; it is the reviewer's question to answer, and
// the verdict records what had moved.
func ValidateSet(projection workroom.Projection, head string, cited []string) error {
	for _, event := range cited {
		artifact, err := StandingArtifact(projection, event)
		if err != nil {
			return fmt.Errorf("reviewed artifact %s: %w", quoted(event), err)
		}
		if artifact.Commit != head {
			return fmt.Errorf("reviewed artifact %s stands at %s, not at the reviewed head %s", quoted(event), quoted(artifact.Commit), quoted(head))
		}
	}
	return nil
}

// CheckCitations bounds the reviewed set and refuses duplicates in one linear
// pass over it. The first citation remains the primary the verdict names.
func CheckCitations(cited []string) ([]string, error) {
	if len(cited) == 0 {
		return nil, errors.New("review requires at least one artifact citation")
	}
	seen := make(map[string]struct{}, len(cited))
	for _, artifact := range cited {
		if artifact == "" {
			return nil, errors.New("artifact citation may not be empty")
		}
		if _, duplicate := seen[artifact]; duplicate {
			return nil, fmt.Errorf("review cites artifact %s twice", quoted(artifact))
		}
		seen[artifact] = struct{}{}
	}
	return cited, nil
}

// StandingArtifact returns an artifact that may still be acted on. Retirement
// withdraws the pointer and is a refusal; being judged ineffective means the
// pointer was never conferred. Staleness is neither: the commit it names is
// immutable and still names exactly what it named.
func StandingArtifact(projection workroom.Projection, event string) (workroom.Artifact, error) {
	if DecisionVerdict(projection, event) != workroom.Effective {
		return workroom.Artifact{}, errors.New("artifact is not effective")
	}
	for _, artifact := range projection.Artifacts {
		if artifact.Event == event {
			if artifact.Retired {
				return workroom.Artifact{}, errors.New("artifact is retired")
			}
			return artifact, nil
		}
	}
	return workroom.Artifact{}, errors.New("artifact event is unknown")
}

// StandingStatement is the same judgement for a statement: refuse what was
// retired or judged ineffective, and report staleness rather than refuse it.
func StandingStatement(projection workroom.Projection, event string, kind workroom.Kind) (workroom.Statement, error) {
	if DecisionVerdict(projection, event) != workroom.Effective {
		return workroom.Statement{}, errors.New("statement is not effective")
	}
	for _, statement := range projection.Statements {
		if statement.Event == event {
			if statement.Kind != kind {
				return workroom.Statement{}, fmt.Errorf("statement is %s, want %s", statement.Kind, kind)
			}
			if statement.Retired {
				return workroom.Statement{}, errors.New("statement is retired")
			}
			return statement, nil
		}
	}
	return workroom.Statement{}, errors.New("statement event is unknown")
}

// UniqueStandingBasis finds the one standing statement of the given kind among
// an event's bases. A promise resting on zero or several requests names no
// single commitment the verdict would answer.
func UniqueStandingBasis(projection workroom.Projection, event string, kind workroom.Kind) (workroom.Statement, error) {
	var found []workroom.Statement
	for _, basis := range projection.Provenance[event] {
		statement, err := StandingStatement(projection, basis, kind)
		if err == nil {
			found = append(found, statement)
		}
	}
	if len(found) != 1 {
		return workroom.Statement{}, fmt.Errorf("expected one standing %s basis, found %d", kind, len(found))
	}
	return found[0], nil
}

// DecisionVerdict reports the fold's verdict for an event, and a non-effective
// answer for an event the log does not contain.
func DecisionVerdict(projection workroom.Projection, event string) workroom.Verdict {
	if decisionOf(projection, event) == "" {
		return workroom.Ineffective
	}
	return decisionOf(projection, event)
}

// ValidateCheckout refuses a checkout that cannot carry the verdict: the
// commit must be canonical, the checkout must belong to the workroom
// repository and be clean, and its HEAD must name exactly the reviewed
// commit.
func ValidateCheckout(checkout, commit, workroomCommonDir string) error {
	ctx := context.Background()
	want, err := canonicalCommit(ctx, checkout, commit)
	if err != nil {
		return err
	}
	if want != commit {
		return fmt.Errorf("commit must be the full canonical object ID: got %s, resolved %s", commit, want)
	}
	_, checkoutCommon, err := apphost.ResolveGitDirs(context.Background(), checkout)
	if err != nil {
		return err
	}
	if canonicalPath(workroomCommonDir) != canonicalPath(checkoutCommon) {
		return errors.New("checkout does not belong to the workroom repository")
	}
	status, err := Git(context.Background(), checkout, "status", "--porcelain=v1", "-z", "--untracked-files=normal")
	if err != nil {
		return err
	}
	if status != "" {
		return errors.New("checkout is dirty")
	}
	head, err := Git(context.Background(), checkout, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if strings.TrimSpace(head) != commit {
		return fmt.Errorf("checkout HEAD %s does not equal artifact head %s", strings.TrimSpace(head), commit)
	}
	return nil
}

func canonicalCommit(ctx context.Context, repo, commit string) (string, error) {
	format, err := Git(ctx, repo, "rev-parse", "--show-object-format")
	if err != nil {
		return "", err
	}
	wantBytes := 20
	if strings.TrimSpace(format) == "sha256" {
		wantBytes = 32
	}
	decoded, err := hex.DecodeString(commit)
	if err != nil || len(decoded) != wantBytes || strings.ToLower(commit) != commit {
		return "", errors.New("candidate must be a full lowercase commit object ID")
	}
	resolved, err := Git(ctx, repo, "rev-parse", "--verify", "--end-of-options", commit+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resolved), nil
}

func canonicalPath(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		absolute = resolved
	}
	return filepath.Clean(absolute)
}

// Git runs one git invocation in a repository and returns its combined output.
func Git(ctx context.Context, repo string, arguments ...string) (string, error) {
	args := make([]string, 0, len(arguments)+3)
	args = append(args, "--no-replace-objects", "-C", repo)
	args = append(args, arguments...)
	output, err := exec.CommandContext(ctx, "git", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(arguments, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}
