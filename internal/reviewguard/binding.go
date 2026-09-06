package reviewguard

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// Binding kinds are guard results, not fold lifecycle or result classes. They
// say what an explicitly examined set of artifacts at one exact head is, as
// far as the projected commitment facts can witness it.
const (
	// BindingAssigned: at least one projected commitment reports an examined
	// artifact, so landing the head discharges that request where it is owed.
	BindingAssigned = "assigned"
	// BindingSelfInitiated: no commitment reports the primary, and the
	// reviewer named the adopted decision the primary rests on directly.
	BindingSelfInitiated = "self-initiated"
	// BindingEvidenceOnly: the primary was filed by its performer straight
	// against a request that owes no Git artifact. It may be reviewed; it
	// cannot be a delivery primary and manufactures no landing obligation.
	BindingEvidenceOnly = "evidence-only"
)

// Body field names a guarded verdict carries for its binding. Authorization
// and merge re-resolve from these and from the verdict's own citations; they
// do not trust the recorded kind.
const (
	BodyBinding         = "binding"
	BodyImplementations = "implementations"
	BodyDecision        = "decision"
)

// Scope is everything a reviewer explicitly says about what one review is of.
// Examined lists the exact artifact events, first the primary the verdict will
// name. Implementations optionally selects the implementation lifecycles a
// combined candidate closes: a request, or that request's exact promise or
// report when several lifecycles would otherwise match. Decision names the
// adopted decision a self-initiated primary rests on. EvidenceOnly says the
// primary is evidence against a request that owes no Git artifact.
type Scope struct {
	Candidate       string
	Examined        []string
	Implementations []string
	Decision        string
	EvidenceOnly    bool
}

// Implementation is one projected commitment an examined artifact reports,
// with the exact witnesses a consumer compares rather than trusts.
type Implementation struct {
	Request    string
	Promise    string
	Report     string
	Path       string
	Performer  string
	TargetRepo string
	TargetRef  string
	Legacy     bool
	HoldOwner  string
	Release    string
	Approval   string
	Candidate  string
}

// Binding is the resolver's result: the kind, the primary and the examined
// set exactly as supplied, every implementation the set represents, the
// decision witness for self-initiated work, and the request the primary is
// evidence for when it is evidence.
type Binding struct {
	Kind            string
	Candidate       string
	Primary         string
	Examined        []string
	Implementations []Implementation
	Decision        string
	Evidence        string
}

// Witnesses lists every durable event the classification stands on, in a
// stable order, so two consumers can compare what they resolved.
func (b Binding) Witnesses() []string {
	var events []string
	for _, implementation := range b.Implementations {
		events = append(events, implementation.Request, implementation.Promise, implementation.Report)
	}
	if b.Decision != "" {
		events = append(events, b.Decision)
	}
	if b.Evidence != "" {
		events = append(events, b.Evidence)
	}
	events = slices.DeleteFunc(events, func(event string) bool { return event == "" })
	sort.Strings(events)
	return slices.Compact(events)
}

// Requests lists the implementation requests, in examined order.
func (b Binding) Requests() []string {
	requests := make([]string, 0, len(b.Implementations))
	for _, implementation := range b.Implementations {
		requests = append(requests, implementation.Request)
	}
	return requests
}

// Lifecycles lists one exact lifecycle witness per implementation, in
// examined order: the selected promise, or the report when the lane made no
// promise. A request may carry several lifecycles (a withdrawn promise and a
// renewed one), so the request alone cannot name what was reviewed; the
// witness re-resolves to exactly one commitment. Review finding d850bca9
// reproduced the loss when only requests were recorded.
func (b Binding) Lifecycles() []string {
	witnesses := make([]string, 0, len(b.Implementations))
	for _, implementation := range b.Implementations {
		if implementation.Promise != "" {
			witnesses = append(witnesses, implementation.Promise)
		} else {
			witnesses = append(witnesses, implementation.Report)
		}
	}
	return witnesses
}

// SameBinding reports whether two resolutions agree on every witness: kind,
// candidate, primary, examined set, implementation triples and their targets,
// decision and evidence request.
func SameBinding(a, b Binding) bool {
	if a.Kind != b.Kind || a.Candidate != b.Candidate || a.Primary != b.Primary || a.Decision != b.Decision || a.Evidence != b.Evidence {
		return false
	}
	if !slices.Equal(a.Examined, b.Examined) || len(a.Implementations) != len(b.Implementations) {
		return false
	}
	for index := range a.Implementations {
		if a.Implementations[index] != b.Implementations[index] {
			return false
		}
	}
	return true
}

// Resolve is the one pure classification every consumer runs over a verified
// projection. It never opens Git, never walks request ancestry, never sweeps
// every artifact at the head, and never treats an empty report lookup as a
// fact about independence: every kind needs its positive witness.
func Resolve(projection workroom.Projection, scope Scope) (Binding, error) {
	examined, err := CheckCitations(scope.Examined)
	if err != nil {
		return Binding{}, err
	}
	if scope.Candidate == "" {
		return Binding{}, errors.New("binding needs the exact candidate")
	}
	if err := ValidateSet(projection, scope.Candidate, examined); err != nil {
		return Binding{}, err
	}
	primary := examined[0]
	binding := Binding{Candidate: scope.Candidate, Primary: primary, Examined: slices.Clone(examined)}
	modes := 0
	if scope.EvidenceOnly {
		modes++
	}
	if scope.Decision != "" {
		modes++
	}
	if len(scope.Implementations) != 0 {
		modes++
	}
	if modes > 1 {
		return Binding{}, errors.New("choose one of --implementation, --self-initiated, or --evidence-only")
	}
	reports := reportIndex(projection)
	switch {
	case scope.EvidenceOnly:
		return resolveEvidence(projection, reports, binding)
	case scope.Decision != "":
		return resolveSelfInitiated(projection, reports, binding, scope.Decision)
	default:
		return resolveAssigned(projection, reports, binding, scope.Implementations)
	}
}

// reportIndex maps each exact reporting artifact to the commitments that name
// it. The fold sets Commitment.Report only for the report that closes a
// lifecycle, so a match is the fold's own binding, not an inference.
func reportIndex(projection workroom.Projection) map[string][]workroom.Commitment {
	index := make(map[string][]workroom.Commitment)
	for _, commitment := range projection.Commitments {
		if commitment.Report != "" {
			index[commitment.Report] = append(index[commitment.Report], commitment)
		}
	}
	return index
}

func artifactOf(projection workroom.Projection, event string) (workroom.Artifact, workroom.Statement, error) {
	artifact, err := StandingArtifact(projection, event)
	if err != nil {
		return workroom.Artifact{}, workroom.Statement{}, err
	}
	statement, err := StandingStatement(projection, event, workroom.KindArtifact)
	if err != nil {
		return workroom.Artifact{}, workroom.Statement{}, err
	}
	return artifact, statement, nil
}

func commitmentOf(projection workroom.Projection, request string) (workroom.Commitment, bool) {
	for _, commitment := range projection.Commitments {
		if commitment.Request == request {
			return commitment, true
		}
	}
	return workroom.Commitment{}, false
}

// ownedEdge follows the artifact's own direct bases one step: a promise the
// artifact's author made, or a request addressed to that author. It returns
// the request that edge leads to, the promise if one was crossed, and whether
// an edge existed at all. This is the only provenance the resolver reads, and
// it reads exactly one hop.
func ownedEdge(projection workroom.Projection, artifact workroom.Statement) (request, promise string, found bool) {
	for _, basis := range projection.Provenance[artifact.Event] {
		if statement, err := StandingStatement(projection, basis, workroom.KindPromise); err == nil && statement.Actor == artifact.Actor {
			if owner, err := UniqueStandingBasis(projection, basis, workroom.KindRequest); err == nil {
				return owner.Event, basis, true
			}
		}
	}
	for _, basis := range projection.Provenance[artifact.Event] {
		if statement, err := StandingStatement(projection, basis, workroom.KindRequest); err == nil && statement.Body["to"] == artifact.Actor {
			return basis, "", true
		}
	}
	return "", "", false
}

func implementationOf(projection workroom.Projection, commitment workroom.Commitment, report string) (Implementation, error) {
	artifact, statement, err := artifactOf(projection, report)
	if err != nil {
		return Implementation{}, fmt.Errorf("reporting artifact %s: %w", quoted(report), err)
	}
	performer := commitment.Performer
	if performer == "" {
		performer = commitment.AddressedTo
	}
	if performer != "" && statement.Actor != performer {
		return Implementation{}, fmt.Errorf("reporting artifact %s was signed by %s, not by the performer of request %s", quoted(report), quoted(statement.Actor), quoted(commitment.Request))
	}
	return Implementation{
		Request: commitment.Request, Promise: commitment.Promise, Report: report, Path: artifact.Path,
		Performer: statement.Actor, TargetRepo: commitment.TargetRepo, TargetRef: commitment.TargetRef,
		Legacy: commitment.Legacy, HoldOwner: commitment.HoldOwner, Release: commitment.Release,
		Approval: commitment.Approval, Candidate: commitment.Candidate,
	}, nil
}

// resolveAssigned binds the primary by exact report equality, then discovers
// further implementation reports only inside the explicitly examined set.
// Selectors disambiguate; they never add artifacts or reorder the primary.
func resolveAssigned(projection workroom.Projection, reports map[string][]workroom.Commitment, binding Binding, selectors []string) (Binding, error) {
	var selected []workroom.Commitment
	if len(selectors) == 0 {
		matches := reports[binding.Primary]
		switch len(matches) {
		case 0:
			return Binding{}, missingPrimary(projection, reports, binding.Primary)
		case 1:
			selected = append(selected, matches[0])
		default:
			return Binding{}, fmt.Errorf("primary %s reports %d commitment lifecycles; select one with --implementation", quoted(binding.Primary), len(matches))
		}
		for _, event := range binding.Examined[1:] {
			for _, commitment := range reports[event] {
				if !containsRequest(selected, commitment.Request) {
					selected = append(selected, commitment)
				}
			}
		}
	} else {
		seen := make(map[string]bool, len(selectors))
		for _, selector := range selectors {
			if selector == "" {
				return Binding{}, errors.New("implementation selector may not be empty")
			}
			var matches []workroom.Commitment
			for _, commitment := range projection.Commitments {
				if commitment.Request == selector || (commitment.Promise != "" && commitment.Promise == selector) || (commitment.Report != "" && commitment.Report == selector) {
					matches = append(matches, commitment)
				}
			}
			if len(matches) != 1 {
				return Binding{}, fmt.Errorf("implementation selector %s matches %d commitment lifecycles, want exactly one", quoted(selector), len(matches))
			}
			commitment := matches[0]
			if seen[commitment.Request] {
				return Binding{}, fmt.Errorf("implementation %s is selected twice", quoted(commitment.Request))
			}
			seen[commitment.Request] = true
			if commitment.Report == "" {
				return Binding{}, fmt.Errorf("implementation %s has no reporting artifact; a delivery review needs its exact report", quoted(commitment.Request))
			}
			if !slices.Contains(binding.Examined, commitment.Report) {
				path := ""
				if artifact, err := StandingArtifact(projection, commitment.Report); err == nil {
					path = " (" + artifact.Path + ")"
				}
				return Binding{}, fmt.Errorf("implementation %s reports %s%s, which is not in the examined set; cite it explicitly", quoted(commitment.Request), quoted(commitment.Report), path)
			}
			selected = append(selected, commitment)
		}
		if selected[0].Report != binding.Primary {
			path := ""
			if artifact, err := StandingArtifact(projection, selected[0].Report); err == nil {
				path = " (" + artifact.Path + ")"
			}
			return Binding{}, fmt.Errorf("supplied primary %s differs from the first selected implementation's report %s%s; name that report first", quoted(binding.Primary), quoted(selected[0].Report), path)
		}
		// A selector disambiguates the lifecycle of the examined report it
		// names. It never narrows the delivery: every other examined artifact
		// that reports a commitment joins the resolved set exactly as it would
		// without selectors, so a held or differently targeted companion
		// cannot be hidden by naming only its neighbour. Review finding
		// 12182bd2 reproduced that omission with real commands.
		disambiguated := make(map[string]bool, len(selected))
		for _, commitment := range selected {
			disambiguated[commitment.Report] = true
		}
		for _, event := range binding.Examined {
			if disambiguated[event] {
				continue
			}
			for _, commitment := range reports[event] {
				if !containsRequest(selected, commitment.Request) {
					selected = append(selected, commitment)
				}
			}
		}
	}
	for _, commitment := range selected {
		implementation, err := implementationOf(projection, commitment, commitment.Report)
		if err != nil {
			return Binding{}, err
		}
		binding.Implementations = append(binding.Implementations, implementation)
	}
	first := binding.Implementations[0]
	for _, implementation := range binding.Implementations[1:] {
		if implementation.TargetRepo != first.TargetRepo || implementation.TargetRef != first.TargetRef {
			return Binding{}, fmt.Errorf("implementations %s and %s owe their landings to different targets (%s, %s); one delivery cannot close both", quoted(first.Request), quoted(implementation.Request), first.TargetRef, implementation.TargetRef)
		}
	}
	binding.Kind = BindingAssigned
	return binding, nil
}

func containsRequest(commitments []workroom.Commitment, request string) bool {
	for _, commitment := range commitments {
		if commitment.Request == request {
			return true
		}
	}
	return false
}

// missingPrimary explains a primary no commitment reports, using only the
// artifact's own one-hop edge: which request it was filed for, and what that
// request's actual report is. Absence of a report is never independence.
func missingPrimary(projection workroom.Projection, reports map[string][]workroom.Commitment, primary string) error {
	_, statement, err := artifactOf(projection, primary)
	if err != nil {
		return err
	}
	request, _, found := ownedEdge(projection, statement)
	if !found {
		return fmt.Errorf("primary %s reports no implementation commitment and rests on no request or promise of its author; name the implementation with --implementation, the adopted decision with --self-initiated, or review it as --evidence-only", quoted(primary))
	}
	owner, _ := StandingStatement(projection, request, workroom.KindRequest)
	if owner.Body["no_git_artifact"] == "true" {
		return fmt.Errorf("primary %s is evidence filed against request %s, which owes no Git artifact; it cannot be a delivery primary, review it with --evidence-only", quoted(primary), quoted(request))
	}
	commitment, ok := commitmentOf(projection, request)
	if ok && commitment.Report != "" && commitment.Report != primary {
		path := ""
		if artifact, err := StandingArtifact(projection, commitment.Report); err == nil {
			path = " (" + artifact.Path + ")"
		}
		return fmt.Errorf("supplied primary %s reports no implementation commitment; request %s is reported by %s%s, name that artifact first and keep %s as a companion if examined", quoted(primary), quoted(request), quoted(commitment.Report), path, quoted(primary))
	}
	return fmt.Errorf("primary %s rests on request %s, but no effective report of that request names it; the assignment's reporting link is broken, so this head cannot be reviewed as its delivery", quoted(primary), quoted(request))
}

// resolveSelfInitiated needs the positive witness: an effective, unretired,
// adopted decision the primary rests on directly, and no commitment that
// claims the primary or that the primary's own edge leads to.
func resolveSelfInitiated(projection workroom.Projection, reports map[string][]workroom.Commitment, binding Binding, decision string) (Binding, error) {
	if len(reports[binding.Primary]) != 0 {
		return Binding{}, fmt.Errorf("primary %s reports implementation commitment %s; assigned work cannot be reviewed as self-initiated", quoted(binding.Primary), quoted(reports[binding.Primary][0].Request))
	}
	_, statement, err := artifactOf(projection, binding.Primary)
	if err != nil {
		return Binding{}, err
	}
	if request, _, found := ownedEdge(projection, statement); found {
		return Binding{}, fmt.Errorf("primary %s was filed for request %s; a broken or evidence-only assignment does not become self-initiated by selecting a mode", quoted(binding.Primary), quoted(request))
	}
	// The witness is one direct edge in either direction: the primary rests
	// on the decision that authorized the work, or the decision is the
	// adoption of this very artifact and rests on it.
	if !slices.Contains(projection.Provenance[binding.Primary], decision) && !slices.Contains(projection.Provenance[decision], binding.Primary) {
		return Binding{}, fmt.Errorf("primary %s and adopted decision %s do not rest directly on each other", quoted(binding.Primary), quoted(decision))
	}
	if err := adoptedDecision(projection, decision); err != nil {
		return Binding{}, err
	}
	for _, event := range binding.Examined[1:] {
		if len(reports[event]) != 0 {
			return Binding{}, fmt.Errorf("examined artifact %s reports implementation commitment %s; a self-initiated review cannot also close an assignment", quoted(event), quoted(reports[event][0].Request))
		}
	}
	binding.Kind = BindingSelfInitiated
	binding.Decision = decision
	return binding, nil
}

// adoptedDecision accepts the two authority shapes SKILL.md names: a ratified
// proposal, or an authority-bearing request whose commitment is satisfied.
// Whether the four authority facts hold is the reviewer's duty; this checks
// only that the witness exists, took force, and stands.
func adoptedDecision(projection workroom.Projection, decision string) error {
	if statement, err := StandingStatement(projection, decision, workroom.KindPropose); err == nil {
		if !statement.Ratified {
			return fmt.Errorf("decision %s is a proposal that is not ratified", quoted(decision))
		}
		return nil
	}
	if _, err := StandingStatement(projection, decision, workroom.KindRequest); err == nil {
		commitment, ok := commitmentOf(projection, decision)
		if !ok || commitment.Status != "satisfied" {
			return fmt.Errorf("decision %s is a request whose commitment is not satisfied", quoted(decision))
		}
		return nil
	}
	return fmt.Errorf("decision %s is not a standing ratified proposal or satisfied request", quoted(decision))
}

// resolveEvidence needs its own positive witness: the primary's author filed it
// directly against a request addressed to them that owes no Git artifact.
func resolveEvidence(projection workroom.Projection, reports map[string][]workroom.Commitment, binding Binding) (Binding, error) {
	for _, event := range binding.Examined {
		if len(reports[event]) != 0 {
			return Binding{}, fmt.Errorf("examined artifact %s reports implementation commitment %s; an assigned delivery cannot be reviewed as evidence-only", quoted(event), quoted(reports[event][0].Request))
		}
	}
	_, statement, err := artifactOf(projection, binding.Primary)
	if err != nil {
		return Binding{}, err
	}
	request, _, found := ownedEdge(projection, statement)
	if !found {
		return Binding{}, fmt.Errorf("primary %s rests on no request or promise of its author; evidence-only review needs the request it is evidence for", quoted(binding.Primary))
	}
	owner, _ := StandingStatement(projection, request, workroom.KindRequest)
	if owner.Body["no_git_artifact"] != "true" {
		return Binding{}, fmt.Errorf("primary %s was filed for request %s, which owes a Git artifact; review it as that assignment's delivery, not as evidence", quoted(binding.Primary), quoted(request))
	}
	binding.Kind = BindingEvidenceOnly
	binding.Evidence = request
	return binding, nil
}

// BodyFields renders the binding into the string fields a verdict body
// carries. Consumers re-resolve from the verdict's citations and these
// selectors; the recorded kind is what was resolved, not what is trusted.
func (b Binding) BodyFields() map[string]string {
	fields := map[string]string{BodyBinding: b.Kind}
	if len(b.Implementations) != 0 {
		encoded, _ := json.Marshal(b.Lifecycles())
		fields[BodyImplementations] = string(encoded)
	}
	if b.Decision != "" {
		fields[BodyDecision] = b.Decision
	}
	return fields
}

// ScopeFromVerdict rebuilds the scope a recorded verdict was signed with, from
// its body and its cited artifacts. A verdict filed before bindings were
// recorded has no selectors: its actual primary is reclassified by exact
// report equality, and an unsupported self-initiation claim refuses.
func ScopeFromVerdict(projection workroom.Projection, verdict workroom.Statement) (Scope, error) {
	return scopeFromBody(projection, verdict.Body, projection.Provenance[verdict.Event])
}

func scopeFromBody(projection workroom.Projection, body map[string]string, restsOn []string) (Scope, error) {
	_, _, artifacts, err := SplitVerdictBases(projection, restsOn)
	if err != nil {
		return Scope{}, err
	}
	primary := body["artifact"]
	head := body["head"]
	// The examined set is the citations standing at the reviewed head. A
	// verdict also cites acknowledged artifact news at other commits, which
	// was never examined scope, and a companion retired since the verdict is
	// no longer a pointer to re-resolve; the reporting artifacts themselves
	// are held live by the resolver.
	examined := []string{primary}
	for _, event := range artifacts {
		if event == primary {
			continue
		}
		for _, artifact := range projection.Artifacts {
			if artifact.Event == event && artifact.Commit == head && !artifact.Retired {
				examined = append(examined, event)
				break
			}
		}
	}
	scope := Scope{Candidate: head, Examined: examined, Decision: body[BodyDecision], EvidenceOnly: body[BodyBinding] == BindingEvidenceOnly}
	if encoded := body[BodyImplementations]; encoded != "" {
		if err := json.Unmarshal([]byte(encoded), &scope.Implementations); err != nil {
			return Scope{}, fmt.Errorf("verdict body.%s is not a JSON array of implementation lifecycle events", BodyImplementations)
		}
	}
	return scope, nil
}

// Explain renders one binding for a reviewer, in the shape the design shows:
// candidate, primary, examined pairs, implementation triples with their
// targets, the decision or evidence witness, and the intended closures.
func Explain(binding Binding, projection workroom.Projection) string {
	var lines []string
	lines = append(lines, "Candidate: "+binding.Candidate, "Binding: "+binding.Kind, "Primary: "+binding.Primary)
	for _, event := range binding.Examined {
		path := ""
		if artifact, err := StandingArtifact(projection, event); err == nil {
			path = " (" + artifact.Path + ")"
		}
		lines = append(lines, "Examined: "+event+path)
	}
	for _, implementation := range binding.Implementations {
		lines = append(lines, fmt.Sprintf("Implementation: request %s promise %s report %s (%s) target %s %s", implementation.Request, implementation.Promise, implementation.Report, implementation.Path, implementation.TargetRepo, implementation.TargetRef))
		if implementation.HoldOwner != "" {
			lines = append(lines, "Hold owner: "+implementation.HoldOwner)
		}
	}
	switch binding.Kind {
	case BindingAssigned:
		lines = append(lines, "Closes on a sealed receipt: "+strings.Join(binding.Requests(), ", "))
	case BindingSelfInitiated:
		lines = append(lines, "Decision: "+binding.Decision, "No implementation commitment to close.")
	case BindingEvidenceOnly:
		lines = append(lines, "Evidence for: "+binding.Evidence, "Not mergeable; confers no implementation closure.")
	}
	return strings.Join(lines, "\n")
}
