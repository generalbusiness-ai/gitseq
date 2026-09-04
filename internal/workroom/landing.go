package workroom

import (
	"fmt"
	"strings"
)

// The landing obligation. An implementation request owes a Git artifact to a
// named destination; the fold records that destination, refuses the acts that
// would close the commitment without reaching it, and projects the states
// between the approval and the landing.
//
// Everything here is keyed to workroom/state@3. The same body names on an
// older record are opaque text and confer nothing, so no historical reading changes
// under this profile except where section 9 of
// notes/2026-09-04-landing-obligation.md says it must.

const (
	// targetInheritDepth bounds the section-2 ancestry walk. It is an
	// admission bound, not a rendering one: admitting a request must cost a
	// fixed amount however deep the log is. Eight exceeds the longest
	// request-to-request chain in this log by a margin; anything deeper
	// restates the triple by value.
	targetInheritDepth = 8
	// legacyTargetRef is what a request admitted before state@3 owed its
	// result to when its commitment carried a reporting artifact. It is a
	// reading of history, never a default for a new request.
	legacyTargetRef = "refs/heads/main"
	branchRefPrefix = "refs/heads/"
)

// requestResult is the section-1 result choice for one request, decided once at
// that request's own position and never recomputed. A request either owes a Git
// artifact landed into a named ref, or owes no Git artifact at all; there is no
// third class and the fold never infers one from prose.
type requestResult struct {
	landing    bool
	targetRepo string
	targetRef  string
	targetHead string
	// inherited records that the triple came from ancestry rather than from
	// this request's own body. A value triple ends the walk for descendants;
	// an inherited one does not.
	inherited bool
	held      bool
	holdOwner string
	// legacy marks a result read from a pre-state@3 commitment's own history
	// rather than stated by its author.
	legacy bool
}

func (r requestResult) sameTarget(other requestResult) bool {
	return r.targetRepo == other.targetRepo && r.targetRef == other.targetRef && r.targetHead == other.targetHead
}

// workroomOf reads the workroom half of a canonical event identifier. Every
// event in a real log is named <workroom>#<event>, so a request carries the
// genesis id of the repository it was filed in, and target_repo can be checked
// against it without the fold ever being told what repository it is reading.
// An identifier with no workroom half names no repository, and a landing
// target cannot be validated against nothing.
func workroomOf(event string) string {
	workroom, _, found := strings.Cut(event, "#")
	if !found {
		return ""
	}
	return workroom
}

// isObjectID reports whether a value is a full lowercase Git object id. The
// fold has no repository and resolves nothing; this is the whole check it can
// make on target_head, and target_head is advisory in any case — the release
// and the merge each re-measure the ref at act time.
func isObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

// decideRequestResult admits or refuses the section-1 choice on a state@3
// request. Exactly one of the three encodings must be present: the target
// triple by value, target=inherit, or no_git_artifact=true.
func (f *foldState) decideRequestResult(record *parsedRecord, state State) (*requestResult, string) {
	repo, ref, head := state.Body["target_repo"], state.Body["target_ref"], state.Body["target_head"]
	triple := repo != "" || ref != "" || head != ""
	inherit := false
	if value, stated := state.Body["target"]; stated {
		if value != "inherit" {
			return nil, `target must be "inherit" when stated`
		}
		inherit = true
	}
	noArtifact := false
	if value, stated := state.Body["no_git_artifact"]; stated {
		if value != "true" {
			return nil, `no_git_artifact must be "true" when stated`
		}
		noArtifact = true
	}
	encodings := 0
	for _, stated := range []bool{triple, inherit, noArtifact} {
		if stated {
			encodings++
		}
	}
	switch {
	case encodings == 0:
		return nil, "request states no result: name a target, inherit one, or state no_git_artifact"
	case encodings > 1:
		return nil, "request states more than one result"
	}
	result := &requestResult{}
	switch {
	case triple:
		if repo == "" || ref == "" || head == "" {
			return nil, "target triple is incomplete"
		}
		if workroom := workroomOf(record.record.ID); workroom == "" || repo != workroom {
			return nil, "target_repo must be this workroom's genesis id"
		}
		if !strings.HasPrefix(ref, branchRefPrefix) || len(ref) == len(branchRefPrefix) {
			return nil, "target_ref must name a branch under refs/heads/"
		}
		if !isObjectID(head) {
			return nil, "target_head must be a full lowercase object id"
		}
		result.landing, result.targetRepo, result.targetRef, result.targetHead = true, repo, ref, head
	case inherit:
		inherited, reason := f.inheritTarget(record)
		if reason != "" {
			return nil, reason
		}
		// A hold travels with the target: a child of a held request is held by
		// the same owner unless it says otherwise.
		*result = *inherited
		result.inherited, result.legacy = true, false
	}
	if value, stated := state.Body["landing"]; stated {
		if value != "held" {
			return nil, `landing must be "held" when stated`
		}
		if !result.landing {
			return nil, "landing hold applies only to a request that owes a landing"
		}
		result.held = true
	}
	if owner, stated := state.Body["hold_owner"]; stated {
		if !result.held {
			return nil, "hold_owner applies only to a held landing request"
		}
		// Delegating the release is the author's decision, so any live actor
		// may be named. An unknown or retired fingerprint names nobody who
		// could ever sign, and a hold nobody can lift is not a hold.
		if !f.hasActor(owner) {
			return nil, "hold_owner is not in the live roster"
		}
		result.holdOwner = owner
	}
	if result.held && result.holdOwner == "" {
		result.holdOwner = record.record.Actor
	}
	return result, ""
}

// inheritTarget is the section-2 walk, stated once: from the request, visit
// rests_on edges that point at request statements, breadth first in recorded
// edge order, stopping at depth eight; the nearest triples are the value
// triples found at the smallest depth at which any is found, and nothing
// deeper is read.
func (f *foldState) inheritTarget(record *parsedRecord) (*requestResult, string) {
	type step struct {
		event string
		depth int
	}
	seen := map[string]bool{record.record.ID: true}
	queue := make([]step, 0, len(record.record.RestsOn))
	enqueue := func(events []string, depth int) {
		for _, event := range events {
			if seen[event] {
				continue
			}
			seen[event] = true
			queue = append(queue, step{event: event, depth: depth})
		}
	}
	enqueue(record.record.RestsOn, 1)
	var nearest []*requestResult
	nearestDepth := 0
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current.depth > targetInheritDepth {
			continue
		}
		if nearestDepth != 0 && current.depth > nearestDepth {
			break
		}
		basis := f.byID[current.event]
		if basis == nil || basis.decision.Verdict != Effective || lifecycleOf(basis) != LifecycleRequest {
			continue
		}
		result := basis.result
		// A no-artifact request inherits nothing and passes nothing on. A
		// review or authorization request therefore sits under a landing
		// parent without acquiring its obligation, and neither do its own
		// children.
		if result != nil && !result.landing {
			continue
		}
		if result != nil && result.landing && !result.inherited {
			nearestDepth = current.depth
			nearest = append(nearest, result)
			continue
		}
		// A request that stated no result of its own — one admitted before
		// state@3, or one that inherited — carries no value triple and does
		// not block: the walk passes through it to its own bases.
		enqueue(basis.record.RestsOn, current.depth+1)
	}
	if len(nearest) == 0 {
		return nil, "no target to inherit"
	}
	for _, candidate := range nearest[1:] {
		if !candidate.sameTarget(*nearest[0]) {
			return nil, "conflicting target ancestry; restate all three target fields"
		}
	}
	return nearest[0], ""
}

// landingResult returns the stated result of a state@3 request, or nil for a
// request admitted under any older schema.
func (f *foldState) landingResult(event string) *requestResult {
	record := f.byID[event]
	if record == nil || record.decision.Verdict != Effective || lifecycleOf(record) != LifecycleRequest {
		return nil
	}
	return record.result
}

// releaseAuthorshipRefusal is the section-10 rule, and it is the one signing
// rule this design changes. A held landing request names exactly one actor who
// may lift its hold. The three-way merge-authorization list — original
// requester, the actor named planner, any live ratifier — does not apply to
// it: the request's author delegated the release by name, and a ratifier who
// wants the landing anyway supersedes the request rather than signing around
// its author.
func (f *foldState) releaseAuthorshipRefusal(record *parsedRecord, state State) string {
	request := state.Body["authorizes_request"]
	if request == "" {
		return ""
	}
	result := f.landingResult(request)
	if result == nil || !result.held {
		return ""
	}
	if record.record.Actor == result.holdOwner {
		return ""
	}
	return fmt.Sprintf("only the hold owner may release the landing hold on %s", request)
}

// releaseFor returns the release in force for one candidate and one approval:
// a live, ratified structured authorization report naming exactly this
// request, candidate and approval.
func (f *foldState) releaseFor(request, candidate, approval string) *parsedRecord {
	for _, record := range f.authorizations[request] {
		state, ok := record.body.(*State)
		if !ok {
			continue
		}
		if state.Body["authorizes_candidate"] != candidate || state.Body["authorizes_approval"] != approval {
			continue
		}
		if f.retired(record.record.ID) || !f.ratified(record.record.ID) {
			continue
		}
		return record
	}
	return nil
}

// ratifiedApproval returns the live ratified approval report that names this
// reporting artifact at its exact head. Nothing else is an approval: not an
// ordinary report, not a resolution report, not a release.
func (f *foldState) ratifiedApproval(artifact *parsedRecord) *parsedRecord {
	state, ok := artifact.body.(*State)
	if !ok {
		return nil
	}
	var newest *parsedRecord
	for _, verdict := range f.directDependents(artifact.record.ID, LifecycleReport) {
		report, ok := verdict.body.(*State)
		if !ok || report.Kind != KindReport || report.Body["verdict"] != "approved" ||
			report.Body["artifact"] != artifact.record.ID || f.retired(verdict.record.ID) || !f.ratified(verdict.record.ID) {
			continue
		}
		head := report.Body["head"]
		if head == "" {
			head = report.Body["commit"]
		}
		if head != state.Body["commit"] {
			continue
		}
		if newest == nil || verdict.index > newest.index {
			newest = verdict
		}
	}
	return newest
}

// reportingArtifacts returns the newest live reporting artifact on a claim and,
// separately, the newest live one whose head carries a ratified approval,
// alongside that approval. The first two are the second and third rungs of the
// section-5 precedence ladder, and the approved one is what the landing
// obligation is measured against however the commitment happens to have
// closed. The approval travels with it so no caller has to assume it is there.
func (f *foldState) reportingArtifacts(claim *parsedRecord, performer string) (newest, approved, approval *parsedRecord) {
	for _, record := range f.directDependents(claim.record.ID, LifecycleNone) {
		if !f.isReportingArtifact(record, claim, performer) || f.retired(record.record.ID) {
			continue
		}
		if newest == nil || record.index > newest.index {
			newest = record
		}
		ratified := f.ratifiedApproval(record)
		if ratified == nil {
			continue
		}
		if approved == nil || record.index > approved.index {
			approved, approval = record, ratified
		}
	}
	return newest, approved, approval
}

// isReportingArtifact reports whether this record is the artifact that answered
// the claim: the performer's own artifact carrying a commit, admitted against
// this very claim when it landed.
func (f *foldState) isReportingArtifact(record, claim *parsedRecord, performer string) bool {
	state, ok := record.body.(*State)
	if !ok || record.definition == nil || record.definition.Render != RenderArtifact ||
		state.Body["commit"] == "" || record.record.Actor != performer {
		return false
	}
	admitted, reported := f.admittedClaims[record.record.ID]
	return reported && admitted.claim.record.ID == claim.record.ID
}

// claimCarriedReportingArtifact answers the one question section 9's legacy
// reading asks of a pre-state@3 commitment: did it ever carry a reporting
// artifact? Retirement does not erase the fact, because the question is about
// what the commitment owed, not about what still stands.
func (f *foldState) claimCarriedReportingArtifact(claim *parsedRecord, performer string) bool {
	for _, record := range f.directDependents(claim.record.ID, LifecycleNone) {
		if f.isReportingArtifact(record, claim, performer) {
			return true
		}
	}
	return false
}

// commitmentResult reads the section-1 choice for one commitment row. A state@3
// request stated it. An older one is read from its own admitted history — a
// commitment that ever carried a reporting artifact owed a landing to
// refs/heads/main of this workroom's own repository, and says so as legacy —
// and never from prose.
func (f *foldState) commitmentResult(request, claim *parsedRecord, performer string) requestResult {
	if request.result != nil {
		return *request.result
	}
	if !f.claimCarriedReportingArtifact(claim, performer) {
		return requestResult{}
	}
	return requestResult{
		landing: true, legacy: true,
		targetRepo: workroomOf(request.record.ID), targetRef: legacyTargetRef,
	}
}

// latestResolution is the section-3 nonterminal evidence: the newest live
// resolution report on this claim. It is beside the completion, never in it.
func (f *foldState) latestResolution(claim *parsedRecord) *parsedRecord {
	var newest *parsedRecord
	for _, record := range f.directDependents(claim.record.ID, LifecycleReport) {
		state, ok := record.body.(*State)
		if !ok || state.Body["resolution"] == "" || f.retired(record.record.ID) {
			continue
		}
		admitted, reported := f.admittedClaims[record.record.ID]
		if !reported || admitted.claim.record.ID != claim.record.ID {
			continue
		}
		if newest == nil || record.index > newest.index {
			newest = record
		}
	}
	return newest
}

// landingReportRefusal is the section-3 rule for explicit reports on a request
// that owes a landing. A plain report cannot close the commitment and must not
// be allowed to outrank the artifact; a verdict belongs to its own review
// commitment; a resolution with a reason is admitted, as evidence beside the
// obligation rather than as an answer to it.
func (f *foldState) landingReportRefusal(claim reportClaim, state State) string {
	result := f.landingResult(claim.request.record.ID)
	if result == nil || !result.landing {
		return ""
	}
	if state.Body["resolution"] != "" {
		return ""
	}
	if state.Body["verdict"] != "" {
		return fmt.Sprintf("request owes a landing to %s; a review verdict belongs to its own review commitment", result.targetRef)
	}
	return fmt.Sprintf("request owes a landing to %s; land it or supersede it", result.targetRef)
}

// approvedHeldHead returns the live reporting artifact at an approved head that
// a request's commitment is holding, across the direct claim and every promise
// under it. It is what makes a supersession of that request a decision about
// approved work rather than an ordinary retirement.
func (f *foldState) approvedHeldHead(request *parsedRecord) *parsedRecord {
	state, ok := request.body.(*State)
	if !ok {
		return nil
	}
	claims := []*parsedRecord{request}
	performers := []string{state.Body["to"]}
	for _, promise := range f.directDependents(request.record.ID, LifecyclePromise) {
		claims = append(claims, promise)
		performers = append(performers, promise.record.Actor)
	}
	for index, claim := range claims {
		if _, approved, _ := f.reportingArtifacts(claim, performers[index]); approved != nil {
			return approved
		}
	}
	return nil
}

// carriedOrAbandoned reads a supersession's disposition of the approved head
// its target holds. The successor carries the head when it is the one request
// this supersession also rests on and it rests on that artifact itself;
// abandonment is stated in the supersession body with the reason in its text.
func (f *foldState) carriedOrAbandoned(record *parsedRecord, supersede Supersede, artifact *parsedRecord) (successor string, abandoned bool) {
	if record.supersedeBody["disposition"] == "abandoned" {
		return "", true
	}
	if artifact == nil || len(record.record.RestsOn) < 2 || record.record.RestsOn[0] != supersede.Target {
		return "", false
	}
	for _, basis := range record.record.RestsOn[1:] {
		candidate := f.byID[basis]
		if candidate == nil || candidate.decision.Verdict != Effective || lifecycleOf(candidate) != LifecycleRequest {
			continue
		}
		if !contains(candidate.record.RestsOn, artifact.record.ID) {
			continue
		}
		if successor != "" {
			return "", false
		}
		successor = candidate.record.ID
	}
	return successor, false
}

// carriedOrAbandonedRefusal enforces the section-7 rule. Nothing here is a new
// authority: it refuses a supersession the ordinary rules would otherwise
// admit, so that losing an approved head takes an explicit act rather than a
// refile.
func (f *foldState) carriedOrAbandonedRefusal(record *parsedRecord, supersede Supersede) string {
	target := f.byID[supersede.Target]
	if target == nil || target.result == nil || !target.result.landing {
		return ""
	}
	artifact := f.approvedHeldHead(target)
	if artifact == nil {
		return ""
	}
	if successor, abandoned := f.carriedOrAbandoned(record, supersede, artifact); successor != "" || abandoned {
		return ""
	}
	state := artifact.body.(*State)
	return fmt.Sprintf("request holds approved head %s; carry it in the successor or declare abandoned", state.Body["commit"])
}

// abandonedRequest reports whether a surviving supersession declared this
// request's approved head abandoned. Like the rejected-round transfer, the
// answer was sealed when the supersession landed.
func (f *foldState) abandonedRequest(request string) bool {
	for _, supersession := range f.supersessions {
		value, ok := supersession.body.(*Supersede)
		if !ok || value.Target != request || !supersession.abandonedSuccession || f.retired(supersession.record.ID) {
			continue
		}
		return true
	}
	return false
}

// receiptTarget reads where a sealed receipt landed. A legacy receipt carrying
// neither field landed on refs/heads/main of this workroom's own repository,
// which is exactly what the audit's history means.
func (f *foldState) receiptTarget(receipt *parsedRecord) (repo, ref string) {
	state, ok := receipt.body.(*State)
	if !ok {
		return "", ""
	}
	repo, ref = state.Body["merge_target_repo"], state.Body["merge_target_ref"]
	if repo == "" {
		repo = workroomOf(receipt.record.ID)
	}
	if ref == "" {
		ref = legacyTargetRef
	}
	return repo, ref
}

// dischargedBy reports whether a sealed receipt discharged this landing
// obligation. Only a receipt into the named ref of the named repository does:
// landing an approved head somewhere else is a real event and a real receipt,
// and it still leaves the obligation open.
func (f *foldState) dischargedBy(receipt *parsedRecord, result requestResult) bool {
	if receipt == nil {
		return false
	}
	repo, ref := f.receiptTarget(receipt)
	return repo == result.targetRepo && ref == result.targetRef
}
