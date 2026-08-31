// Package mergeplan contains the read-only part of guarded merge preflight.
// Both the mutating merge command and the CLI/MCP planning surfaces call these
// functions, so the explanation cannot drift from the rule that later lands.
package mergeplan

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/reviewguard"
	"github.com/generalbusiness-ai/gitseq/internal/service"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

const (
	ClassInTargetPredecessor = "in-target predecessor"
	ClassReviewedCandidate   = "reviewed candidate"
	ClassProtectedSibling    = "protected sibling"
	ClassAbandoned           = "abandoned"
)

type Frontier struct {
	Genesis string `json:"genesis"`
	Head    string `json:"head"`
	Depth   int    `json:"depth"`
}

type Reason struct {
	Code    string `json:"code"`
	Check   string `json:"check"`
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason"`
}

type CandidateArtifact struct {
	Event    string `json:"event"`
	Path     string `json:"path"`
	Commit   string `json:"commit"`
	Author   string `json:"author"`
	Reviewed bool   `json:"reviewed"`
}

type CoveringArtifact struct {
	Event      string `json:"event"`
	Path       string `json:"path"`
	Commit     string `json:"commit"`
	Author     string `json:"author"`
	Class      string `json:"class"`
	Commitment string `json:"commitment,omitempty"`
	Successor  string `json:"successor,omitempty"`
}

type Retirement struct {
	Artifact  string `json:"artifact"`
	Path      string `json:"path"`
	Successor string `json:"successor,omitempty"`
}

type Result struct {
	Frontier           Frontier            `json:"frontier"`
	Mode               string              `json:"mode"`
	Approval           string              `json:"approval"`
	ExactHead          string              `json:"exact_head"`
	Implementer        string              `json:"implementer,omitempty"`
	TargetPreHead      string              `json:"target_pre_head,omitempty"`
	MergeHead          string              `json:"merge_head,omitempty"`
	CandidateArtifacts []CandidateArtifact `json:"candidate_artifacts"`
	ReviewedPaths      []string            `json:"reviewed_paths"`
	ChangedPaths       []string            `json:"changed_paths"`
	CoveringArtifacts  []CoveringArtifact  `json:"covering_artifacts"`
	Retirements        []Retirement        `json:"retirements"`
	Successors         []string            `json:"successors"`
	Allowed            bool                `json:"allowed"`
	Reasons            []Reason            `json:"reasons"`
}

type Change struct {
	Status string
	Old    string
	New    string
}

type LeftLive struct {
	Class      string `json:"class"`
	Commitment string `json:"commitment,omitempty"`
}

type Candidate struct {
	Class    string
	LeftLive LeftLive
}

type Succession struct {
	Publish      []string
	Retire       map[string]string
	ChangedPaths []string
	LeftLive     map[string]LeftLive
}

type Approval struct {
	Statement         workroom.Statement
	Artifact          workroom.Artifact
	Implementer       string
	Staleness         string
	ReviewedArtifacts map[string]bool
	ReviewedPaths     []string
}

// Signer carries the local custody needed to prove that the exact durable
// succession suffix can be encoded and admitted. CheckResidentCeiling mirrors
// whether the eventual merge will submit through the resident service.
type Signer struct {
	Name                 string
	Private              ed25519.PrivateKey
	CheckResidentCeiling bool
}

// ProspectiveAct is one act in the canonical merge succession suffix. Labels
// are local placeholders for earlier events in the same suffix.
type ProspectiveAct struct {
	Label string
	Act   app.Act
}

func git(ctx context.Context, repo string, arguments ...string) (string, error) {
	args := append([]string{"--no-optional-locks", "--no-replace-objects", "-C", repo}, arguments...)
	output, err := exec.CommandContext(ctx, "git", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(arguments, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
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

func CanonicalCommit(ctx context.Context, repo, commit string) (string, error) {
	format, err := git(ctx, repo, "rev-parse", "--show-object-format")
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
	resolved, err := git(ctx, repo, "rev-parse", "--verify", "--end-of-options", commit+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resolved), nil
}

func ValidateCheckout(ctx context.Context, workroomRepo, checkout, commit string, requireHead bool) error {
	want, err := CanonicalCommit(ctx, checkout, commit)
	if err != nil {
		return err
	}
	if want != commit {
		return fmt.Errorf("commit must be the full canonical object ID: got %s, resolved %s", commit, want)
	}
	_, workroomCommon, err := resolveGitDirs(ctx, workroomRepo)
	if err != nil {
		return err
	}
	_, checkoutCommon, err := resolveGitDirs(ctx, checkout)
	if err != nil {
		return err
	}
	if canonicalPath(workroomCommon) != canonicalPath(checkoutCommon) {
		return errors.New("checkout does not belong to the workroom repository")
	}
	status, err := git(ctx, checkout, "status", "--porcelain=v1", "-z", "--untracked-files=normal")
	if err != nil {
		return err
	}
	if status != "" {
		return errors.New("checkout is dirty")
	}
	if requireHead {
		head, err := git(ctx, checkout, "rev-parse", "HEAD")
		if err != nil {
			return err
		}
		if strings.TrimSpace(head) != commit {
			return fmt.Errorf("checkout HEAD %s does not equal artifact head %s", strings.TrimSpace(head), commit)
		}
	}
	return nil
}

func resolveGitDirs(ctx context.Context, repo string) (string, string, error) {
	output, err := git(ctx, repo, "rev-parse", "--path-format=absolute", "--absolute-git-dir", "--git-common-dir")
	if err != nil {
		return "", "", err
	}
	paths := strings.Split(strings.TrimSpace(output), "\n")
	if len(paths) != 2 {
		return "", "", fmt.Errorf("resolve git dirs: expected worktree and common paths, got %q", strings.TrimSpace(output))
	}
	return strings.TrimSpace(paths[0]), strings.TrimSpace(paths[1]), nil
}

func RequireImplementer(projection workroom.Projection, approvalEvent, merger string) error {
	if merger == "" {
		return errors.New("merge needs the signing actor's fingerprint")
	}
	review, found := projection.Review(approvalEvent)
	if !found || review.Implementer == "" {
		return errors.New("the record cannot say who implemented this approved head, so nobody may merge it on that approval")
	}
	if review.Implementer != merger {
		return fmt.Errorf("merge must be signed by the actor whose approved work is landing (%s); --as names %s", review.Implementer, merger)
	}
	return nil
}

func ValidateApproval(projection workroom.Projection, candidate, approvalEvent string) (Approval, error) {
	verdict := verdictSequence(projection, approvalEvent)
	approval, err := liveStatementAsOf(projection, approvalEvent, workroom.KindReport, verdict)
	if err != nil {
		return Approval{}, fmt.Errorf("approval: %w", err)
	}
	if !approval.Ratified {
		return Approval{}, errors.New("approval report is not ratified by its requester")
	}
	if approval.Body["verdict"] != "approved" {
		return Approval{}, errors.New("review verdict is not approved")
	}
	if approval.Body["head"] != candidate {
		return Approval{}, fmt.Errorf("candidate %s does not equal approved head %s", candidate, approval.Body["head"])
	}
	artifactEvent := approval.Body["artifact"]
	if artifactEvent == "" || !slices.Contains(projection.Provenance[approvalEvent], artifactEvent) {
		return Approval{}, errors.New("approval does not rest on its named artifact")
	}
	artifact, err := liveArtifactAsOf(projection, artifactEvent, approval.Sequence)
	if err != nil {
		return Approval{}, fmt.Errorf("approval artifact: %w", err)
	}
	if artifact.Commit != candidate {
		return Approval{}, fmt.Errorf("approved artifact head %s does not equal candidate %s", artifact.Commit, candidate)
	}
	review, found := projection.Review(approvalEvent)
	if !found {
		return Approval{}, errors.New("approval is not projected as a review")
	}
	switch review.Independence {
	case workroom.IndependenceSelfReview:
		return Approval{}, errors.New("approval was signed by the actor who implemented this head; an independent review is required")
	case workroom.IndependenceIndependent:
		artifacts, paths := ReviewedScope(projection, approvalEvent)
		return Approval{
			Statement: approval, Artifact: artifact, Implementer: review.Implementer,
			Staleness: reviewguard.StalenessNote(projection, []reviewguard.Part{
				{Name: "approval", Event: approval.Event, Stale: approval.Stale, World: approval.DescribesSupersededWorld},
				{Name: "artifact", Event: artifact.Event, Stale: artifact.Stale, World: artifact.DescribesSupersededWorld},
			}),
			ReviewedArtifacts: artifacts, ReviewedPaths: paths,
		}, nil
	default:
		return Approval{}, errors.New("the record cannot say whether this approval was independent; name the reviewed artifact in the review report")
	}
}

func decisionEffective(projection workroom.Projection, event string) bool {
	for _, decision := range projection.Decisions {
		if decision.Event == event {
			return decision.Verdict == workroom.Effective
		}
	}
	return false
}

func verdictSequence(projection workroom.Projection, approval string) int {
	for _, decision := range projection.Decisions {
		if decision.Event == approval {
			return decision.Sequence
		}
	}
	return math.MaxInt
}

func worldDatedAfter(datedAt, verdict int) bool { return datedAt != 0 && datedAt > verdict }

func liveStatementAsOf(projection workroom.Projection, event string, kind workroom.Kind, verdict int) (workroom.Statement, error) {
	if !decisionEffective(projection, event) {
		return workroom.Statement{}, errors.New("statement is not effective")
	}
	for _, statement := range projection.Statements {
		if statement.Event != event {
			continue
		}
		if statement.Kind != kind {
			return workroom.Statement{}, fmt.Errorf("statement is %s, want %s", statement.Kind, kind)
		}
		if statement.Retired {
			return workroom.Statement{}, errors.New("statement is retired")
		}
		if statement.DescribesSupersededWorld && !worldDatedAfter(statement.WorldSupersededAt, verdict) {
			return workroom.Statement{}, errors.New("statement describes a superseded world")
		}
		return statement, nil
	}
	return workroom.Statement{}, errors.New("statement event is unknown")
}

func liveArtifactAsOf(projection workroom.Projection, event string, verdict int) (workroom.Artifact, error) {
	if !decisionEffective(projection, event) {
		return workroom.Artifact{}, errors.New("artifact is not effective")
	}
	for _, artifact := range projection.Artifacts {
		if artifact.Event != event {
			continue
		}
		if artifact.Retired {
			return workroom.Artifact{}, errors.New("artifact is retired")
		}
		if artifact.DescribesSupersededWorld && !worldDatedAfter(artifact.WorldSupersededAt, verdict) {
			return workroom.Artifact{}, errors.New("artifact describes a superseded world")
		}
		return artifact, nil
	}
	return workroom.Artifact{}, errors.New("artifact event is unknown")
}

func ParseChanges(raw string) ([]Change, error) {
	fields := strings.Split(raw, "\x00")
	if len(fields) != 0 && fields[len(fields)-1] == "" {
		fields = fields[:len(fields)-1]
	}
	var changes []Change
	for position := 0; position < len(fields); {
		status := fields[position]
		position++
		if status == "" || position >= len(fields) {
			return nil, errors.New("malformed NUL-delimited merge diff")
		}
		change := Change{Status: status, New: fields[position]}
		position++
		if status[0] == 'R' || status[0] == 'C' {
			change.Old = change.New
			if position >= len(fields) {
				return nil, errors.New("malformed NUL-delimited merge diff")
			}
			change.New = fields[position]
			position++
		} else if status[0] == 'D' {
			change.Old, change.New = change.New, ""
		}
		changes = append(changes, change)
	}
	return changes, nil
}

func ValidateChangePaths(changes []Change) error {
	for _, change := range changes {
		for _, path := range []string{change.Old, change.New} {
			if path != "" && !utf8.ValidString(path) {
				return fmt.Errorf("merge diff path is not valid UTF-8: %q", path)
			}
		}
	}
	return nil
}

func ChangedPaths(changes []Change) []string {
	set := make(map[string]bool)
	for _, change := range changes {
		if change.Old != "" {
			set[change.Old] = true
		}
		if change.New != "" {
			set[change.New] = true
		}
	}
	paths := make([]string, 0, len(set))
	for path := range set {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func artifactCoversPath(artifact, changed string) bool {
	return artifact != "" && changed != "" && (artifact == changed || strings.HasPrefix(changed, strings.TrimSuffix(artifact, "/")+"/"))
}

func CoveredArtifacts(projection workroom.Projection, changes []Change) []workroom.Artifact {
	var covered []workroom.Artifact
	for _, artifact := range projection.Artifacts {
		if artifact.Retired || artifact.Path == "." || strings.Contains(artifact.Path, ",") {
			continue
		}
		for _, change := range changes {
			if artifactCoversPath(artifact.Path, change.Old) || artifactCoversPath(artifact.Path, change.New) {
				covered = append(covered, artifact)
				break
			}
		}
	}
	return covered
}

func Classify(ctx context.Context, checkout string, projection workroom.Projection, changes []Change, targetPreHead, candidate string, reviewed map[string]bool) map[string]Candidate {
	classified := make(map[string]Candidate)
	covered := CoveredArtifacts(projection, changes)
	protected := protectionIndex(projection, covered)
	byCommit := make(map[string]bool)
	for _, artifact := range covered {
		if artifact.Commit == candidate {
			class := ClassInTargetPredecessor
			if reviewed[artifact.Event] {
				class = ClassReviewedCandidate
			}
			classified[artifact.Event] = Candidate{Class: class}
			continue
		}
		ancestor := false
		if artifact.Commit != "" {
			var checked bool
			ancestor, checked = byCommit[artifact.Commit]
			if !checked {
				_, err := git(ctx, checkout, "merge-base", "--is-ancestor", artifact.Commit, targetPreHead)
				ancestor = err == nil
				byCommit[artifact.Commit] = ancestor
			}
		}
		if ancestor {
			classified[artifact.Event] = Candidate{Class: ClassInTargetPredecessor}
		} else if commitment := protected[artifact.Event]; commitment != "" {
			classified[artifact.Event] = Candidate{Class: ClassProtectedSibling, LeftLive: LeftLive{Class: "sibling", Commitment: commitment}}
		} else {
			classified[artifact.Event] = Candidate{Class: ClassAbandoned, LeftLive: LeftLive{Class: "abandoned"}}
		}
	}
	return classified
}

func PlanSuccession(projection workroom.Projection, changes []Change, candidates map[string]Candidate) Succession {
	var live []workroom.Artifact
	for _, artifact := range projection.Artifacts {
		if artifact.Retired || artifact.Path == "." || strings.Contains(artifact.Path, ",") {
			continue
		}
		live = append(live, artifact)
	}
	published := map[string]bool{}
	retire := map[string]string{}
	leftLive := make(map[string]LeftLive)
	covering := func(path string, exact bool) []workroom.Artifact {
		var found []workroom.Artifact
		for _, artifact := range live {
			if artifact.Path == path {
				if exact {
					found = append(found, artifact)
				}
				continue
			}
			if strings.HasPrefix(path, strings.TrimSuffix(artifact.Path, "/")+"/") {
				found = append(found, artifact)
			}
		}
		return found
	}
	widest := func(artifacts []workroom.Artifact, fallback string) string {
		winner := ""
		for _, artifact := range artifacts {
			if winner == "" || widerPath(artifact.Path, winner) {
				winner = artifact.Path
			}
		}
		if winner == "" {
			return fallback
		}
		return winner
	}
	assign := func(artifact workroom.Artifact, successor string) {
		candidate := candidates[artifact.Event]
		if candidate.Class == ClassProtectedSibling || candidate.Class == ClassAbandoned {
			leftLive[artifact.Event] = candidate.LeftLive
			return
		}
		current, exists := retire[artifact.Event]
		if !exists || current == "" || (successor != "" && widerPath(successor, current)) {
			retire[artifact.Event] = successor
		}
	}
	landed := func(path string) {
		covers := covering(path, true)
		winner := widest(covers, path)
		published[winner] = true
		for _, artifact := range covers {
			assign(artifact, winner)
		}
	}
	removed := func(path string) {
		for _, artifact := range covering(path, true) {
			if artifact.Path == path {
				assign(artifact, "")
			}
		}
		covers := covering(path, false)
		if len(covers) == 0 {
			return
		}
		winner := widest(covers, path)
		published[winner] = true
		for _, artifact := range covers {
			assign(artifact, winner)
		}
	}
	for _, change := range changes {
		switch change.Status[0] {
		case 'D':
			removed(change.Old)
		case 'R':
			removed(change.Old)
			landed(change.New)
		default:
			landed(change.New)
		}
	}
	paths := make([]string, 0, len(published))
	for path := range published {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return Succession{Publish: paths, Retire: retire, ChangedPaths: ChangedPaths(changes), LeftLive: leftLive}
}

func ValidateSuccession(ctx context.Context, workspace *app.Workspace, checkout string, plan Succession) error {
	published := make(map[string]bool, len(plan.Publish))
	for _, path := range plan.Publish {
		if path == "." || strings.Contains(path, ",") {
			return fmt.Errorf("merge would publish an invalid artifact path %q", path)
		}
		published[path] = true
	}
	targets := make([]string, 0, len(plan.Retire))
	for target := range plan.Retire {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	for _, target := range targets {
		successor := plan.Retire[target]
		if successor == "" {
			if err := workspace.RefuseCitedRetirementInCheckout(ctx, checkout, target, false); err != nil {
				return err
			}
		} else if !published[successor] {
			return fmt.Errorf("merge would retire %s naming a successor at %q that it does not publish", target, successor)
		}
	}
	return nil
}

func ReviewedScope(projection workroom.Projection, approval string) (map[string]bool, []string) {
	review, found := projection.Review(approval)
	if !found || review.Implementer == "" || review.Head == "" {
		return map[string]bool{}, nil
	}
	authors := make(map[string]string, len(projection.Statements))
	for _, statement := range projection.Statements {
		authors[statement.Event] = statement.Actor
	}
	standing := make(map[string]workroom.Artifact, len(projection.Artifacts))
	for _, artifact := range projection.Artifacts {
		standing[artifact.Event] = artifact
	}
	verdict := verdictSequence(projection, approval)
	events := make(map[string]bool)
	seen := make(map[string]bool)
	var paths []string
	for _, basis := range projection.Provenance[approval] {
		artifact, ok := standing[basis]
		if !ok || artifact.Retired || artifact.Path == "" {
			continue
		}
		if artifact.DescribesSupersededWorld && !worldDatedAfter(artifact.WorldSupersededAt, verdict) {
			continue
		}
		if artifact.Commit != review.Head || authors[basis] != review.Implementer {
			continue
		}
		events[basis] = true
		if !seen[artifact.Path] {
			seen[artifact.Path] = true
			paths = append(paths, artifact.Path)
		}
	}
	sort.Strings(paths)
	return events, paths
}

func ValidateReach(projection workroom.Projection, plan Succession, approval, actor string) error {
	if actor == "" {
		return errors.New("merge succession needs the merging actor's fingerprint")
	}
	_, reviewed := ReviewedScope(projection, approval)
	if len(reviewed) == 0 {
		return fmt.Errorf("approval %s puts no artifact path within reach, so it bounds no retirement", approval)
	}
	authors := make(map[string]string, len(projection.Statements))
	for _, statement := range projection.Statements {
		authors[statement.Event] = statement.Actor
	}
	targets := make([]string, 0, len(plan.Retire))
	for target := range plan.Retire {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	for _, target := range targets {
		path := artifactPath(projection, target)
		if authors[target] == actor || withinReviewedPaths(reviewed, path) {
			continue
		}
		return fmt.Errorf("merge would retire %s at %q, which belongs to another actor and lies outside the reviewed paths %s:\nhave the approval cover it, or ask its author or an actor holding ratifier to retire it", target, path, strings.Join(reviewed, ", "))
	}
	return nil
}

func widerPath(candidate, current string) bool {
	return candidate != current && strings.HasPrefix(current, strings.TrimSuffix(candidate, "/")+"/")
}

func withinReviewedPaths(reviewed []string, path string) bool {
	if path == "" {
		return false
	}
	for _, within := range reviewed {
		if within != "" && (within == path || widerPath(within, path)) {
			return true
		}
	}
	return false
}

func artifactPath(projection workroom.Projection, event string) string {
	for _, artifact := range projection.Artifacts {
		if artifact.Event == event {
			return artifact.Path
		}
	}
	return ""
}

func protectionIndex(projection workroom.Projection, artifacts []workroom.Artifact) map[string]string {
	statements := make(map[string]workroom.Statement, len(projection.Statements))
	for _, statement := range projection.Statements {
		statements[statement.Event] = statement
	}
	effective := make(map[string]bool, len(projection.Decisions))
	for _, decision := range projection.Decisions {
		effective[decision.Event] = decision.Verdict == workroom.Effective
	}
	active := make(map[string]bool)
	for _, commitment := range projection.Commitments {
		if !unsettledCommitment(commitment.Status) {
			continue
		}
		for _, event := range []string{commitment.Request, commitment.Promise, commitment.Report} {
			statement, found := statements[event]
			if !found || (statement.Lifecycle != workroom.LifecycleRequest && statement.Lifecycle != workroom.LifecyclePromise && statement.Lifecycle != workroom.LifecycleReport) {
				continue
			}
			active[event] = true
		}
	}
	artifactByEvent := make(map[string]workroom.Artifact, len(artifacts))
	for _, artifact := range artifacts {
		artifactByEvent[artifact.Event] = artifact
	}
	protected := make(map[string]string)
	consider := func(artifact, commitment string) {
		if current := protected[artifact]; current == "" || commitment < current {
			protected[artifact] = commitment
		}
	}
	byCommit := make(map[string]string)
	for event := range active {
		statement := statements[event]
		for _, commit := range []string{statement.Body["head"], statement.Body["commit"]} {
			if commit != "" && (byCommit[commit] == "" || event < byCommit[commit]) {
				byCommit[commit] = event
			}
		}
		for reached := range provenanceClosure(projection.Provenance, effective, event) {
			if _, ok := artifactByEvent[reached]; ok {
				consider(reached, event)
			}
		}
	}
	for _, artifact := range artifacts {
		if event := byCommit[artifact.Commit]; artifact.Commit != "" && event != "" {
			consider(artifact.Event, event)
		}
		for reached := range provenanceClosure(projection.Provenance, effective, artifact.Event) {
			if active[reached] {
				consider(artifact.Event, reached)
			}
		}
	}
	return protected
}

func unsettledCommitment(status string) bool {
	switch status {
	case "open", "promised", "reported", "stale":
		return true
	default:
		return false
	}
}

func provenanceClosure(provenance map[string][]string, effective map[string]bool, from string) map[string]bool {
	seen := map[string]bool{from: true}
	queue := []string{from}
	for len(queue) > 0 {
		event := queue[0]
		queue = queue[1:]
		if decision, projected := effective[event]; projected && !decision {
			continue
		}
		for _, basis := range provenance[event] {
			if !seen[basis] {
				seen[basis] = true
				queue = append(queue, basis)
			}
		}
	}
	return seen
}

type sealedReceipt struct {
	Approval      string
	Candidate     string
	TargetPreHead string
	MergeHead     string
	Retire        map[string]string
	Publish       []string
	LeftLive      map[string]LeftLive
	ChangedPaths  []string
}

const (
	approvalTrailer    = "Gitseq-Approval: "
	candidateTrailer   = "Gitseq-Candidate: "
	targetTrailer      = "Gitseq-Target-Pre-Head: "
	retirementsTrailer = "Gitseq-Retirements: "
	successorsTrailer  = "Gitseq-Successors: "
	leftLiveTrailer    = "Gitseq-Left-Live: "
	changedTrailer     = "Gitseq-Changed-Paths: "
)

func receiptRef(approval string) string {
	sum := sha256.Sum256([]byte(approval))
	return "refs/gitseq/merge-receipts/" + hex.EncodeToString(sum[:])
}

func readReceipt(ctx context.Context, checkout, head string) (sealedReceipt, bool, error) {
	message, err := git(ctx, checkout, "show", "-s", "--format=%B", head)
	if err != nil {
		return sealedReceipt{}, false, err
	}
	receipt := sealedReceipt{MergeHead: head}
	var retirements, successors, leftLive, changed string
	for _, line := range strings.Split(message, "\n") {
		switch {
		case strings.HasPrefix(line, approvalTrailer):
			receipt.Approval = strings.TrimPrefix(line, approvalTrailer)
		case strings.HasPrefix(line, candidateTrailer):
			receipt.Candidate = strings.TrimPrefix(line, candidateTrailer)
		case strings.HasPrefix(line, targetTrailer):
			receipt.TargetPreHead = strings.TrimPrefix(line, targetTrailer)
		case strings.HasPrefix(line, retirementsTrailer):
			retirements = strings.TrimPrefix(line, retirementsTrailer)
		case strings.HasPrefix(line, successorsTrailer):
			successors = strings.TrimPrefix(line, successorsTrailer)
		case strings.HasPrefix(line, leftLiveTrailer):
			leftLive = strings.TrimPrefix(line, leftLiveTrailer)
		case strings.HasPrefix(line, changedTrailer):
			changed = strings.TrimPrefix(line, changedTrailer)
		}
	}
	parents, err := git(ctx, checkout, "rev-list", "--parents", "-n", "1", head)
	if err != nil {
		return sealedReceipt{}, false, err
	}
	fields := strings.Fields(parents)
	if receipt.Approval == "" || receipt.Candidate == "" || receipt.TargetPreHead == "" || retirements == "" || successors == "" ||
		len(fields) != 3 || fields[0] != head || fields[1] != receipt.TargetPreHead || fields[2] != receipt.Candidate {
		return sealedReceipt{}, false, nil
	}
	if err := json.Unmarshal([]byte(retirements), &receipt.Retire); err != nil {
		return sealedReceipt{}, false, fmt.Errorf("decode Git receipt retirements: %w", err)
	}
	if err := json.Unmarshal([]byte(successors), &receipt.Publish); err != nil {
		return sealedReceipt{}, false, fmt.Errorf("decode Git receipt successors: %w", err)
	}
	if leftLive != "" {
		if err := json.Unmarshal([]byte(leftLive), &receipt.LeftLive); err != nil {
			return sealedReceipt{}, false, fmt.Errorf("decode Git receipt left-live accounting: %w", err)
		}
	}
	if changed != "" {
		if err := json.Unmarshal([]byte(changed), &receipt.ChangedPaths); err != nil {
			return sealedReceipt{}, false, fmt.Errorf("decode Git receipt changed paths: %w", err)
		}
	}
	return receipt, true, nil
}

func existingReceipt(ctx context.Context, checkout, approval string) (sealedReceipt, bool, bool, error) {
	ref := receiptRef(approval)
	if head, err := git(ctx, checkout, "rev-parse", "--verify", ref); err == nil {
		head = strings.TrimSpace(head)
		receipt, ok, readErr := readReceipt(ctx, checkout, head)
		if readErr != nil {
			return sealedReceipt{}, false, true, readErr
		}
		if ok && receipt.Approval == approval {
			return receipt, true, true, nil
		}
		return sealedReceipt{}, false, true, nil
	}
	heads, err := git(ctx, checkout, "log", "--all", "--fixed-strings", "--grep="+approvalTrailer+approval, "--format=%H")
	if err != nil {
		return sealedReceipt{}, false, false, err
	}
	for _, head := range strings.Fields(heads) {
		receipt, ok, err := readReceipt(ctx, checkout, head)
		if err != nil {
			return sealedReceipt{}, false, false, err
		}
		if ok && receipt.Approval == approval {
			return receipt, true, false, nil
		}
	}
	return sealedReceipt{}, false, false, nil
}

func renderReceipt(result *Result, projection workroom.Projection, receipt sealedReceipt) {
	result.TargetPreHead = receipt.TargetPreHead
	result.MergeHead = receipt.MergeHead
	result.ChangedPaths = append(result.ChangedPaths, receipt.ChangedPaths...)
	result.Successors = append(result.Successors, receipt.Publish...)
	sort.Strings(result.ChangedPaths)
	sort.Strings(result.Successors)
	_, result.ReviewedPaths = ReviewedScope(projection, receipt.Approval)
	authors := make(map[string]string)
	for _, statement := range projection.Statements {
		authors[statement.Event] = statement.Actor
	}
	for event, successor := range receipt.Retire {
		result.Retirements = append(result.Retirements, Retirement{Artifact: event, Path: artifactPath(projection, event), Successor: successor})
	}
	for event, left := range receipt.LeftLive {
		class := ClassAbandoned
		if left.Class == "sibling" {
			class = ClassProtectedSibling
		}
		for _, artifact := range projection.Artifacts {
			if artifact.Event == event {
				result.CoveringArtifacts = append(result.CoveringArtifacts, CoveringArtifact{Event: event, Path: artifact.Path, Commit: artifact.Commit, Author: authors[event], Class: class, Commitment: left.Commitment})
				break
			}
		}
	}
	sort.Slice(result.Retirements, func(i, j int) bool { return result.Retirements[i].Artifact < result.Retirements[j].Artifact })
	sort.Slice(result.CoveringArtifacts, func(i, j int) bool { return result.CoveringArtifacts[i].Event < result.CoveringArtifacts[j].Event })
}

func mergeReceiptKey(approval string) string {
	sum := sha256.Sum256([]byte(approval))
	return "merge-receipt-" + hex.EncodeToString(sum[:])
}

func successionKey(approval, class, value string) string {
	sum := sha256.Sum256([]byte(approval + "\x00" + class + "\x00" + value))
	return "merge-succession-" + hex.EncodeToString(sum[:])
}

// SuccessionActs is the single encoder for the durable suffix used both by a
// read-only plan and by merge. mergeHead is the candidate during prospective
// admission: both are full object IDs in the same repository, so the signed
// envelope and body have the exact eventual byte lengths.
func SuccessionActs(approval, candidate, targetPreHead, mergeHead, staleness string, plan Succession) []ProspectiveAct {
	retirements, err := json.Marshal(plan.Retire)
	if err != nil {
		return nil
	}
	successors, err := json.Marshal(plan.Publish)
	if err != nil {
		return nil
	}
	receiptBody := map[string]string{
		"merge_approval": approval, "merge_candidate": candidate,
		"merge_target_pre_head": targetPreHead, "merge_head": mergeHead,
		"merge_retirements": string(retirements), "merge_successors": string(successors),
	}
	if plan.LeftLive != nil {
		leftLive, err := json.Marshal(plan.LeftLive)
		if err != nil {
			return nil
		}
		receiptBody["merge_left_live"] = string(leftLive)
	}
	if plan.ChangedPaths != nil {
		changedPaths, err := json.Marshal(plan.ChangedPaths)
		if err != nil {
			return nil
		}
		receiptBody["merge_changed_paths"] = string(changedPaths)
	}
	if staleness != "" {
		receiptBody["stale"] = "true"
		receiptBody["staleness"] = staleness
	}
	acts := []ProspectiveAct{{Label: "merge", Act: app.Act{
		Verb: app.VerbState, Kind: workroom.KindAssert, Text: "approved candidate merged",
		Body: receiptBody, RestsOn: []string{approval}, IdempotencyKey: mergeReceiptKey(approval), AllowDeadBasis: true,
	}}}
	labels := make(map[string]string, len(plan.Publish))
	for index, path := range plan.Publish {
		label := fmt.Sprintf("successor-%d", index)
		labels[path] = label
		acts = append(acts, ProspectiveAct{Label: label, Act: app.Act{
			Verb: app.VerbState, Kind: workroom.KindArtifact, Text: "Merge published the current artifact at " + path,
			Body: map[string]string{"path": path, "commit": mergeHead}, RestsOn: []string{"$merge"},
			IdempotencyKey: successionKey(approval, "publish", path), AllowDeadBasis: true,
		}})
	}
	targets := make([]string, 0, len(plan.Retire))
	for target := range plan.Retire {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	for _, target := range targets {
		successor := plan.Retire[target]
		rests := []string{"$merge"}
		text := "Merge retired a covered predecessor with no successor at its old path."
		if successor != "" {
			rests = append(rests, "$"+labels[successor])
			text = "Merge retired a covered predecessor; the successor is at " + successor + "."
		}
		acts = append(acts, ProspectiveAct{Act: app.Act{
			Verb: app.VerbSupersede, Target: target, Text: text, RestsOn: rests,
			IdempotencyKey: successionKey(approval, "retire", target), CitedOK: true,
		}})
	}
	return acts
}

func resolveProspective(reference string, minted map[string]string) string {
	if name, cited := strings.CutPrefix(reference, "$"); cited {
		return minted[name]
	}
	return reference
}

// ValidateAdmission constructs every act through the application's exact
// request builder over the immutable plan snapshot and checks the same kernel
// and optional resident byte ceilings as merge, without appending anything.
func ValidateAdmission(ctx context.Context, workspace *app.Workspace, snapshot app.Snapshot, signer Signer, acts []ProspectiveAct) error {
	if signer.Name == "" || len(signer.Private) != ed25519.PrivateKeySize {
		return errors.New("merge plan needs local custody for the approved implementer to prove succession admission")
	}
	view := workspace.View()
	syntheticEvent := workspace.EventID(strings.Repeat("0", len(view.Genesis)))
	minted := make(map[string]string, len(acts))
	for position, entry := range acts {
		act := entry.Act
		act.Target = resolveProspective(act.Target, minted)
		act.RestsOn = make([]string, 0, len(entry.Act.RestsOn))
		for _, reference := range entry.Act.RestsOn {
			act.RestsOn = append(act.RestsOn, resolveProspective(reference, minted))
		}
		request, err := workspace.BuildActRequestReadOnly(ctx, snapshot, signer.Private, signer.Name, act)
		if err != nil {
			return fmt.Errorf("act %d: admission: %w", position, err)
		}
		if err := kernel.ValidateRequestSize(request, view.PayloadCeiling); err != nil {
			return fmt.Errorf("act %d: admission: %w", position, err)
		}
		if signer.CheckResidentCeiling {
			if err := service.ValidateSubmissionRequestSize(request); err != nil {
				return fmt.Errorf("act %d: admission: %w", position, err)
			}
		}
		if entry.Label != "" {
			minted[entry.Label] = syntheticEvent
		}
	}
	return nil
}

// Build computes the same prospective plan as a fresh merge in a disposable
// clone. Git may stage and abandon a tentative merge there; the governed
// checkout, its refs and index, and the durable workroom remain untouched.
const OutputLimit = 2 << 20

func Build(ctx context.Context, workspace *app.Workspace, checkout, candidate, approvalEvent, merger string, signer Signer) (result Result) {
	result = Result{Mode: "fresh", Approval: approvalEvent, ExactHead: candidate, CandidateArtifacts: []CandidateArtifact{}, ReviewedPaths: []string{}, ChangedPaths: []string{}, CoveringArtifacts: []CoveringArtifact{}, Retirements: []Retirement{}, Successors: []string{}, Reasons: []Reason{}}
	defer func() { result = boundResult(result) }()
	fail := func(check string, err error) Result {
		result.Allowed = false
		result.Reasons = append(result.Reasons, Reason{Code: check + "_refused", Check: check, Allowed: false, Reason: err.Error()})
		return result
	}
	if err := ValidateCheckout(ctx, workspace.Repo, checkout, candidate, false); err != nil {
		return fail("checkout", err)
	}
	result.Reasons = append(result.Reasons, Reason{Code: "checkout_allowed", Check: "checkout", Allowed: true, Reason: "candidate is canonical, checkout belongs to the workroom repository, and the checkout is clean"})
	snapshot, err := workspace.ReadOnlySnapshot(ctx)
	if err != nil {
		return fail("frontier", err)
	}
	result.Frontier = Frontier{Genesis: snapshot.Genesis, Head: snapshot.Head, Depth: snapshot.Depth}
	result.Reasons = append(result.Reasons, Reason{Code: "frontier_allowed", Check: "frontier", Allowed: true, Reason: "the complete signed workroom verified and folded without publishing acceleration state"})
	receipt, foundReceipt, reserved, err := existingReceipt(ctx, checkout, approvalEvent)
	if err != nil {
		return fail("approval_use", err)
	}
	if reserved && !foundReceipt {
		result.Mode = "used"
		return fail("approval_use", errors.New("approval is reserved or used by another merge"))
	}
	if foundReceipt {
		result.Mode = "used"
		if receipt.Candidate != candidate {
			return fail("approval_use", fmt.Errorf("approval was already used for candidate %s", receipt.Candidate))
		}
		review, ok := snapshot.Projection.Review(approvalEvent)
		if ok {
			result.Implementer = review.Implementer
		}
		if err := RequireImplementer(snapshot.Projection, approvalEvent, merger); err != nil {
			return fail("implementer", err)
		}
		renderReceipt(&result, snapshot.Projection, receipt)
		head, err := git(ctx, checkout, "rev-parse", "--verify", "HEAD^{commit}")
		if err != nil {
			return fail("target", err)
		}
		if strings.TrimSpace(head) != receipt.MergeHead {
			return fail("approval_use", fmt.Errorf("approval was already used by merge %s, but checkout is at another head", receipt.MergeHead))
		}
		result.Mode = "resume"
		result.Allowed = true
		result.Reasons = append(result.Reasons, Reason{Code: "resume_allowed", Check: "approval_use", Allowed: true, Reason: "checkout is at the sealed merge head; only the recorded durable succession suffix remains"})
		return result
	}
	for _, statement := range snapshot.Projection.Statements {
		if statement.Body["merge_approval"] == approvalEvent && decisionEffective(snapshot.Projection, statement.Event) {
			result.Mode = "used"
			return fail("approval_use", fmt.Errorf("approval already has durable merge receipt %s", statement.Event))
		}
	}
	result.Reasons = append(result.Reasons, Reason{Code: "approval_use_allowed", Check: "approval_use", Allowed: true, Reason: "approval has no reservation, Git receipt, or durable merge receipt"})
	approved, err := ValidateApproval(snapshot.Projection, candidate, approvalEvent)
	if err != nil {
		return fail("approval", err)
	}
	result.Implementer = approved.Implementer
	result.ReviewedPaths = append(result.ReviewedPaths, approved.ReviewedPaths...)
	if err := RequireImplementer(snapshot.Projection, approvalEvent, merger); err != nil {
		return fail("implementer", err)
	}
	target, err := git(ctx, checkout, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return fail("target", err)
	}
	result.TargetPreHead = strings.TrimSpace(target)
	result.Reasons = append(result.Reasons, Reason{Code: "target_allowed", Check: "target", Allowed: true, Reason: "target pre-head is an exact commit in the governed checkout"})
	if _, err := git(ctx, checkout, "merge-base", "--is-ancestor", candidate, result.TargetPreHead); err == nil {
		return fail("candidate", errors.New("approved candidate is already contained in the target"))
	}
	result.Reasons = append(result.Reasons, Reason{Code: "candidate_allowed", Check: "candidate", Allowed: true, Reason: "approved candidate is not already contained in the target"})
	authors := make(map[string]string)
	for _, statement := range snapshot.Projection.Statements {
		authors[statement.Event] = statement.Actor
	}
	for _, artifact := range snapshot.Projection.Artifacts {
		if artifact.Retired || artifact.Commit != candidate {
			continue
		}
		result.CandidateArtifacts = append(result.CandidateArtifacts, CandidateArtifact{Event: artifact.Event, Path: artifact.Path, Commit: artifact.Commit, Author: authors[artifact.Event], Reviewed: approved.ReviewedArtifacts[artifact.Event]})
	}
	sort.Slice(result.CandidateArtifacts, func(i, j int) bool { return result.CandidateArtifacts[i].Event < result.CandidateArtifacts[j].Event })
	changes, err := disposableMergeChanges(ctx, checkout, result.TargetPreHead, candidate)
	if err != nil {
		return fail("tentative_merge", err)
	}
	if err := ValidateChangePaths(changes); err != nil {
		return fail("changed_paths", err)
	}
	result.Reasons = append(result.Reasons,
		Reason{Code: "tentative_merge_allowed", Check: "tentative_merge", Allowed: true, Reason: "the exact candidate merges cleanly with the target pre-head in an isolated disposable clone"},
		Reason{Code: "changed_paths_allowed", Check: "changed_paths", Allowed: true, Reason: "the complete staged merge path frontier is valid UTF-8 and representable"},
	)
	classified := Classify(ctx, checkout, snapshot.Projection, changes, result.TargetPreHead, candidate, approved.ReviewedArtifacts)
	plan := PlanSuccession(snapshot.Projection, changes, classified)
	result.ChangedPaths = append(result.ChangedPaths, plan.ChangedPaths...)
	result.Successors = append(result.Successors, plan.Publish...)
	for _, artifact := range CoveredArtifacts(snapshot.Projection, changes) {
		candidate := classified[artifact.Event]
		result.CoveringArtifacts = append(result.CoveringArtifacts, CoveringArtifact{Event: artifact.Event, Path: artifact.Path, Commit: artifact.Commit, Author: authors[artifact.Event], Class: candidate.Class, Commitment: candidate.LeftLive.Commitment, Successor: plan.Retire[artifact.Event]})
	}
	sort.Slice(result.CoveringArtifacts, func(i, j int) bool { return result.CoveringArtifacts[i].Event < result.CoveringArtifacts[j].Event })
	for event, successor := range plan.Retire {
		result.Retirements = append(result.Retirements, Retirement{Artifact: event, Path: artifactPath(snapshot.Projection, event), Successor: successor})
	}
	sort.Slice(result.Retirements, func(i, j int) bool { return result.Retirements[i].Artifact < result.Retirements[j].Artifact })
	if err := ValidateSuccession(ctx, workspace, checkout, plan); err != nil {
		return fail("succession", err)
	}
	if err := ValidateReach(snapshot.Projection, plan, approvalEvent, merger); err != nil {
		return fail("reviewed_scope", err)
	}
	acts := SuccessionActs(approvalEvent, candidate, result.TargetPreHead, candidate, approved.Staleness, plan)
	if acts == nil {
		return fail("admission", errors.New("merge succession could not be represented as a durable act suffix"))
	}
	if err := ValidateAdmission(ctx, workspace, snapshot, signer, acts); err != nil {
		return fail("admission", err)
	}
	result.Allowed = true
	result.Reasons = append(result.Reasons,
		Reason{Code: "approval_allowed", Check: "approval", Allowed: true, Reason: "ratified independent approval names the exact candidate head"},
		Reason{Code: "implementer_allowed", Check: "implementer", Allowed: true, Reason: "merger is the approved implementation artifact's author"},
		Reason{Code: "succession_allowed", Check: "succession", Allowed: true, Reason: "every retirement has a valid successor or passes the citation guard"},
		Reason{Code: "reviewed_scope_allowed", Check: "reviewed_scope", Allowed: true, Reason: "every cross-author retirement lies at or beneath a reviewed candidate path"},
		Reason{Code: "admission_allowed", Check: "admission", Allowed: true, Reason: "the exact durable succession suffix is representable and within its admission ceilings"},
	)
	return result
}

func boundResult(result Result) Result {
	encoded, err := json.Marshal(result)
	if err == nil && len(encoded) <= OutputLimit {
		return result
	}
	reason := "encoded merge plan exceeds the output ceiling"
	if err != nil {
		reason = "encode merge plan: " + err.Error()
	}
	return Result{
		Frontier: result.Frontier, Mode: result.Mode, Approval: result.Approval, ExactHead: result.ExactHead,
		Implementer: result.Implementer, TargetPreHead: result.TargetPreHead, MergeHead: result.MergeHead,
		CandidateArtifacts: []CandidateArtifact{}, ReviewedPaths: []string{}, ChangedPaths: []string{},
		CoveringArtifacts: []CoveringArtifact{}, Retirements: []Retirement{}, Successors: []string{},
		Allowed: false, Reasons: []Reason{{Code: "plan_output_too_large", Check: "output", Allowed: false, Reason: reason}},
	}
}

func disposableMergeChanges(ctx context.Context, checkout, target, candidate string) ([]Change, error) {
	root, err := os.MkdirTemp("", "gitseq-merge-plan-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(root)
	clone := filepath.Join(root, "checkout")
	output, err := exec.CommandContext(ctx, "git", "--no-optional-locks", "--no-replace-objects", "clone", "--quiet", "--shared", "--no-checkout", "--", checkout, clone).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git clone read-only merge sandbox: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if _, err := git(ctx, clone, "checkout", "--quiet", "--detach", target); err != nil {
		return nil, err
	}
	if _, err := git(ctx, clone,
		"-c", "user.name=gitseq merge plan",
		"-c", "user.email=gitseq-merge-plan@invalid",
		"merge", "--no-ff", "--no-commit", "--", candidate); err != nil {
		return nil, err
	}
	raw, err := git(ctx, clone, "diff", "--cached", "--name-status", "-z", "--find-renames")
	if err != nil {
		return nil, err
	}
	return ParseChanges(raw)
}
