package workroom

import (
	"fmt"
	"sort"
	"strings"
)

type Verdict string

const (
	Effective   Verdict = "effective"
	Ineffective Verdict = "ineffective"
	Disputed    Verdict = "disputed"
)

type Record struct {
	ID          string            `json:"id"`
	Timestamp   int64             `json:"timestamp,omitempty"`
	Actor       string            `json:"actor"`
	Schema      string            `json:"schema"`
	RestsOn     []string          `json:"rests_on"`
	Payload     []byte            `json:"payload"`
	Attachments map[string][]byte `json:"attachments,omitempty"`
}

type Decision struct {
	Event   string  `json:"event"`
	Verdict Verdict `json:"verdict"`
	Reason  string  `json:"reason"`
}

type Statement struct {
	Event     string            `json:"event"`
	Timestamp int64             `json:"timestamp,omitempty"`
	Actor     string            `json:"actor"`
	Kind      Kind              `json:"kind"`
	Text      string            `json:"text"`
	Body      map[string]string `json:"body,omitempty"`
	Ratified  bool              `json:"ratified,omitempty"`
	Retired   bool              `json:"retired,omitempty"`
	Stale     bool              `json:"stale,omitempty"`
	// DescribesSupersededWorld narrows Stale: the retired ancestor that made
	// this statement stale is itself an artifact, so what moved is the world
	// the statement describes rather than the argument it stands on. Both are
	// staleness; only this one means go and re-read the code.
	DescribesSupersededWorld bool `json:"describes_superseded_world,omitempty"`
}

type Commitment struct {
	Request   string `json:"request"`
	Requester string `json:"requester"`
	Performer string `json:"performer,omitempty"`
	Promise   string `json:"promise,omitempty"`
	Report    string `json:"report,omitempty"`
	Status    string `json:"status"`
	WaitingOn string `json:"waiting_on,omitempty"`
}

type Artifact struct {
	Event  string `json:"event"`
	Path   string `json:"path"`
	Commit string `json:"commit"`
	Stale  bool   `json:"stale"`
	// DescribesSupersededWorld carries the same narrowing as on Statement.
	DescribesSupersededWorld bool `json:"describes_superseded_world,omitempty"`
	// UnableToFlare records that this artifact has no basis that any act could
	// ever retire, so no supersession anywhere can make it stale. Its silence
	// is not currency and the projection must not let it read as currency.
	// Citing nothing is one way to get here; citing only events that are not
	// in the log is the other, because supersede needs a target it can resolve.
	UnableToFlare bool `json:"unable_to_flare,omitempty"`
	// LivePredecessors counts earlier artifacts at the identical path that are
	// still live, so a reader can tell two forgotten retirements from one. It
	// is a per-row figure and must not be summed across rows: with A, B and C
	// at one path, B counts A and C counts both, so the total double-counts A.
	// The situation count is Projection.OmittedSupersessions.
	LivePredecessors int `json:"live_predecessors,omitempty"`
	// SuccessionUnrecorded records that an earlier artifact for the identical
	// path is still live: a probable forgotten supersession. It is a warning
	// about practice, not a verdict — the act itself stays effective. Paths
	// are compared as exact strings, because path is a free body field and
	// inferring which spellings mean the same tree would be guesswork.
	SuccessionUnrecorded bool `json:"succession_unrecorded,omitempty"`
}

// Act is a ratify or supersede event in client-friendly form: what it
// targeted, who performed it, and how the fold judged it.
type Act struct {
	Event     string  `json:"event"`
	Timestamp int64   `json:"timestamp,omitempty"`
	Actor     string  `json:"actor"`
	Type      string  `json:"type"`
	Target    string  `json:"target"`
	Text      string  `json:"text,omitempty"`
	Verdict   Verdict `json:"verdict"`
	Reason    string  `json:"reason"`
}

// ActorState is the fold's complete durable view of a principal. Local
// configuration may hold custody for the same fingerprint, but names, kinds,
// membership and authority come from effective roster statements here.
type ActorState struct {
	Name            string              `json:"name"`
	Kind            string              `json:"kind,omitempty"`
	Roles           []string            `json:"roles"`
	MembershipEvent string              `json:"membership_event,omitempty"`
	RoleSources     map[string][]string `json:"role_sources"`
}

type Projection struct {
	Decisions   []Decision            `json:"decisions"`
	Acts        []Act                 `json:"acts"`
	Statements  []Statement           `json:"statements"`
	Commitments []Commitment          `json:"commitments"`
	Artifacts   []Artifact            `json:"artifacts"`
	Actors      map[string]ActorState `json:"actors"`
	Provenance  map[string][]string   `json:"provenance"`
	OpaqueKinds map[string][]string   `json:"opaque_kinds,omitempty"`
	// OmittedSupersessions counts live artifacts that a later live artifact at
	// the same path should have retired and did not, each counted once. It is
	// the number of supersessions still owed — not the number of artifacts
	// noticing one, and not the number of paths, both of which understate the
	// repair. A predecessor whose every successor was withdrawn owes nothing:
	// it is the current artifact for its path again.
	OmittedSupersessions int `json:"omitted_supersessions,omitempty"`
}

type parsedRecord struct {
	record   Record
	body     any
	index    int
	decision Decision
}

type roleGrant struct {
	actor           string
	name            string
	kind            string
	role            string
	statement       string
	ratification    string
	membership      bool
	membershipBasis string
}

type actorRole struct {
	actor string
	role  string
}

type actorStatement struct {
	actor     string
	statement string
}

type dependentKind struct {
	basis string
	kind  Kind
}

type foldState struct {
	records          []parsedRecord
	byID             map[string]*parsedRecord
	decisions        map[string]Decision
	effectiveSup     map[string]string
	retirementCauses map[string]int
	roleGrants       []roleGrant
	roleGrantsByRole map[actorRole][]roleGrant
	membershipGrants map[actorStatement][]roleGrant
	ratifications    map[string][]string
	dependents       map[dependentKind][]*parsedRecord
}

// Folder retains the verified application state for a resident reader. It is
// deliberately not concurrency-safe; the application owns serialization.
// Projection may be called after any append and preserves Fold's output.
type Folder struct {
	state *foldState
}

func Fold(records []Record) Projection {
	folder := NewFolder(records)
	return folder.Projection()
}

// NewFolder constructs the same state as Fold while allowing later verified
// records to be applied without decoding and judging the prefix again.
func NewFolder(records []Record) *Folder {
	state := &foldState{
		byID: make(map[string]*parsedRecord), decisions: make(map[string]Decision),
		effectiveSup:     make(map[string]string),
		retirementCauses: make(map[string]int),
		roleGrantsByRole: make(map[actorRole][]roleGrant),
		membershipGrants: make(map[actorStatement][]roleGrant),
		ratifications:    make(map[string][]string),
		dependents:       make(map[dependentKind][]*parsedRecord),
	}
	for index, record := range records {
		state.append(index, record)
	}
	return &Folder{state: state}
}

// Append applies the next verified record in sequence order.
func (f *Folder) Append(record Record) {
	f.state.append(len(f.state.records), record)
}

// Projection renders the current deterministic workroom view.
func (f *Folder) Projection() Projection {
	return f.state.project()
}

func (f *foldState) append(index int, record Record) {
	decision := Decision{Event: record.ID, Verdict: Ineffective}
	if record.ID == "" || record.Actor == "" {
		decision.Reason = "event id and actor are required"
		f.addDecision(record, nil, index, decision)
		return
	}
	if _, exists := f.byID[record.ID]; exists {
		decision.Verdict = Disputed
		decision.Reason = "duplicate event id"
		f.addDecision(record, nil, index, decision)
		return
	}
	body, err := Decode(record.Schema, record.Payload)
	// The decoded body is the fold input retained below. Payload bytes and
	// attachments are transport material and must not stay pinned for the
	// resident process lifetime.
	record.Payload = nil
	record.Attachments = nil
	if err != nil {
		decision.Reason = err.Error()
		f.addDecision(record, nil, index, decision)
		return
	}
	parsed := &parsedRecord{record: record, body: body, index: index}
	switch value := body.(type) {
	case *State:
		decision = f.decideState(parsed, *value)
	case *Ratify:
		decision = f.decideRatify(parsed, *value)
	case *Supersede:
		decision = f.decideSupersede(parsed, *value)
	}
	f.addDecision(record, parsed, index, decision)
	if decision.Verdict != Effective {
		return
	}
	if state, ok := body.(*State); ok {
		// A record may repeat a basis. directDependents historically returned
		// the record once because it used contains; preserve that behavior while
		// indexing every effective statement in one pass.
		seen := make(map[string]struct{}, len(record.RestsOn))
		for _, basis := range record.RestsOn {
			if _, exists := seen[basis]; exists {
				continue
			}
			seen[basis] = struct{}{}
			key := dependentKind{basis: basis, kind: state.Kind}
			f.dependents[key] = append(f.dependents[key], parsed)
		}
	}
	switch value := body.(type) {
	case *State:
		if value.Kind == KindRoster && index == 0 {
			f.addRoleGrant(value.Body["actor"], value.Body["name"], value.Body["kind"], value.Body["role"], record.ID, "", record.RestsOn)
		}
	case *Ratify:
		f.ratifications[value.Target] = append(f.ratifications[value.Target], record.ID)
		if target := f.byID[value.Target]; target != nil {
			if roster, ok := target.body.(*State); ok && roster.Kind == KindRoster {
				f.addRoleGrant(roster.Body["actor"], roster.Body["name"], roster.Body["kind"], roster.Body["role"], value.Target, record.ID, target.record.RestsOn)
			}
		}
	case *Supersede:
		f.effectiveSup[record.ID] = value.Target
		f.changeRetirement(value.Target, 1)
	}
}

func (f *foldState) addDecision(record Record, parsed *parsedRecord, index int, decision Decision) {
	if decision.Event == "" {
		decision.Event = record.ID
	}
	if decision.Reason == "" {
		decision.Reason = string(decision.Verdict)
	}
	if parsed == nil {
		parsed = &parsedRecord{record: record, index: index}
	}
	parsed.decision = decision
	f.records = append(f.records, *parsed)
	if record.ID != "" {
		if _, exists := f.byID[record.ID]; !exists {
			f.byID[record.ID] = parsed
			f.decisions[record.ID] = decision
		}
	}
}

func (f *foldState) decideState(record *parsedRecord, state State) Decision {
	decision := Decision{Event: record.record.ID, Verdict: Effective, Reason: "statement recorded"}
	if record.index == 0 {
		if state.Kind != KindRoster || state.Body["actor"] != record.record.Actor || state.Body["role"] != "operator" {
			return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: "first event must self-seed the operator roster"}
		}
		return Decision{Event: record.record.ID, Verdict: Effective, Reason: "operator roster seed"}
	}
	if state.Kind == KindRequest && !f.hasActor(state.Body["to"]) {
		return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: "requested performer is not in the live roster"}
	}
	if state.Kind == KindPromise {
		requests := f.basesOfKind(record.record.RestsOn, KindRequest)
		if len(requests) == 0 {
			return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: "dangling promise has no request"}
		}
		if len(requests) != 1 {
			return Decision{Event: record.record.ID, Verdict: Disputed, Reason: "promise rests on multiple requests"}
		}
		request := requests[0].body.(*State)
		if request.Body["to"] != record.record.Actor {
			return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: "promise actor is not the requested performer"}
		}
	}
	if state.Kind == KindReport {
		promises := f.basesOfKind(record.record.RestsOn, KindPromise)
		if len(promises) == 0 {
			return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: "report has no promise"}
		}
		if len(promises) != 1 {
			return Decision{Event: record.record.ID, Verdict: Disputed, Reason: "report rests on multiple promises"}
		}
		if promises[0].record.Actor != record.record.Actor {
			return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: "only the promisor may report completion"}
		}
	}
	if !knownKinds[state.Kind] {
		decision.Reason = "opaque statement kind recorded"
	}
	return decision
}

func (f *foldState) decideRatify(record *parsedRecord, ratify Ratify) Decision {
	target := f.byID[ratify.Target]
	if target == nil {
		return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: "ratify target is unknown"}
	}
	if len(record.record.RestsOn) != 1 || record.record.RestsOn[0] != ratify.Target {
		return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: "ratify must rest on exactly its target"}
	}
	statement, ok := target.body.(*State)
	if !ok {
		return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: "only statements may be ratified"}
	}
	if statement.Kind == KindReport {
		request := f.originatingRequest(target)
		if request == nil {
			return Decision{Event: record.record.ID, Verdict: Disputed, Reason: "report has no unique originating requester"}
		}
		if request.record.Actor != record.record.Actor {
			return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: "only the requester may declare satisfaction"}
		}
		return Decision{Event: record.record.ID, Verdict: Effective, Reason: "requester declared satisfaction"}
	}
	if statement.Kind == KindAssert || statement.Kind == KindPropose || isGovernance(statement.Kind) {
		if !f.hasRole(record.record.Actor, "ratifier") {
			return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: "actor lacks ratifier role"}
		}
		return Decision{Event: record.record.ID, Verdict: Effective, Reason: "authorized ratification"}
	}
	return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: "statement kind is not ratifiable"}
}

func (f *foldState) decideSupersede(record *parsedRecord, supersede Supersede) Decision {
	target := f.byID[supersede.Target]
	if target == nil {
		return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: "supersede target is unknown"}
	}
	if len(record.record.RestsOn) == 0 || record.record.RestsOn[0] != supersede.Target {
		return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: "supersede must rest first on its target"}
	}
	if target.record.Actor == record.record.Actor || f.hasRole(record.record.Actor, "ratifier") {
		return Decision{Event: record.record.ID, Verdict: Effective, Reason: "authorized supersession"}
	}
	return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: "actor may not supersede target"}
}

func isGovernance(kind Kind) bool {
	return kind == KindRoster || kind == KindInfraKey || kind == KindSeal
}

func (f *foldState) addRoleGrant(actor, name, kind, role, statement, ratification string, restsOn []string) {
	if actor == "" || role == "" {
		return
	}
	grant := roleGrant{
		actor: actor, name: name, kind: kind, role: role,
		statement: statement, ratification: ratification,
	}
	if kind == "" {
		// Before kind and authority became independent, every roster record
		// admitted its actor and stored either a descriptive kind or an
		// authority in role. Treat all such records uniformly as combined
		// membership records; a kind-bearing record is the wire discriminator
		// for the modern split model.
		grant.membership = true
		switch role {
		case "agent", "human", "service":
			grant.kind, grant.role = role, "participant"
		case "operator":
			grant.kind = "human"
		default:
			grant.kind = "unspecified"
		}
	} else if role == "participant" || ratification == "" {
		// The only unratified grant is the genesis operator seed, which is
		// necessarily both membership and authority.
		grant.membership = true
	} else if len(restsOn) != 0 {
		grant.membershipBasis = restsOn[0]
	}
	f.roleGrants = append(f.roleGrants, grant)
	f.indexRoleGrant(grant, grant.role)
	if grant.membership && grant.role != "participant" {
		f.indexRoleGrant(grant, "participant")
	}
	if grant.role == "operator" {
		f.indexRoleGrant(grant, "ratifier")
	}
	if grant.membership {
		key := actorStatement{actor: grant.actor, statement: grant.statement}
		f.membershipGrants[key] = append(f.membershipGrants[key], grant)
	}
}

func (f *foldState) indexRoleGrant(grant roleGrant, role string) {
	key := actorRole{actor: grant.actor, role: role}
	f.roleGrantsByRole[key] = append(f.roleGrantsByRole[key], grant)
}

func (f *foldState) roleGrantActive(grant roleGrant) bool {
	if !f.roleGrantDirectActive(grant) {
		return false
	}
	if grant.membership {
		return true
	}
	return grant.membershipBasis != "" && f.membershipGrantActive(grant.actor, grant.membershipBasis)
}

func (f *foldState) roleGrantDirectActive(grant roleGrant) bool {
	if f.retirementCauses[grant.statement] != 0 || f.decisions[grant.statement].Verdict != Effective {
		return false
	}
	if grant.ratification == "" {
		return true
	}
	return f.retirementCauses[grant.ratification] == 0 && f.decisions[grant.ratification].Verdict == Effective
}

func (f *foldState) membershipGrantActive(actor, statement string) bool {
	for _, grant := range f.membershipGrants[actorStatement{actor: actor, statement: statement}] {
		if f.roleGrantDirectActive(grant) {
			return true
		}
	}
	return false
}

func (f *foldState) hasRole(actor, role string) bool {
	for _, grant := range f.roleGrantsByRole[actorRole{actor: actor, role: role}] {
		if f.roleGrantActive(grant) {
			return true
		}
	}
	return false
}

func (f *foldState) hasActor(actor string) bool {
	return f.hasRole(actor, "participant")
}

func (f *foldState) basesOfKind(refs []string, kind Kind) []*parsedRecord {
	var found []*parsedRecord
	for _, ref := range refs {
		record := f.byID[ref]
		if record == nil || f.decisions[ref].Verdict != Effective {
			continue
		}
		state, ok := record.body.(*State)
		if ok && state.Kind == kind {
			found = append(found, record)
		}
	}
	return found
}

func (f *foldState) originatingRequest(report *parsedRecord) *parsedRecord {
	promises := f.basesOfKind(report.record.RestsOn, KindPromise)
	if len(promises) != 1 {
		return nil
	}
	requests := f.basesOfKind(promises[0].record.RestsOn, KindRequest)
	if len(requests) != 1 {
		return nil
	}
	return requests[0]
}

// changeRetirement maintains the live supersession projection as each
// effective supersession lands. A target changes state only when its count of
// live superseders crosses zero. If the target is itself a supersession, that
// transition removes or restores its effect and continues down the chain.
func (f *foldState) changeRetirement(target string, delta int) {
	for target != "" {
		before := f.retirementCauses[target]
		after := before + delta
		if after <= 0 {
			after = 0
			delete(f.retirementCauses, target)
		} else {
			f.retirementCauses[target] = after
		}
		wasRetired, isRetired := before != 0, after != 0
		if wasRetired == isRetired {
			return
		}
		next, supersession := f.effectiveSup[target]
		if !supersession {
			return
		}
		if isRetired {
			delta = -1
		} else {
			delta = 1
		}
		target = next
	}
}

func (f *foldState) retired() map[string]bool {
	retired := make(map[string]bool, len(f.retirementCauses))
	for event := range f.retirementCauses {
		retired[event] = true
	}
	return retired
}

// staleness returns transitive staleness and, narrowing it, the records whose
// staleness traces back to a retired artifact. A record is world-stale when a
// retired artifact is the basis that made it stale, or when it rests on
// something already world-stale, so the distinction survives any number of
// hops. Records are visited in sequence order and a basis is always cited
// before its dependent, so one pass settles both maps.
func (f *foldState) staleness(retired map[string]bool) (map[string]bool, map[string]bool) {
	stale := make(map[string]bool)
	world := make(map[string]bool)
	for _, record := range f.records {
		if record.decision.Verdict != Effective {
			continue
		}
		for _, basis := range record.record.RestsOn {
			if target, ok := f.effectiveSup[record.record.ID]; ok && target == basis {
				continue
			}
			if !retired[basis] && !stale[basis] {
				continue
			}
			stale[record.record.ID] = true
			if world[basis] || (retired[basis] && f.isArtifact(basis)) {
				world[record.record.ID] = true
				break
			}
		}
	}
	return stale, world
}

// isArtifact reports whether an event is an effective artifact statement. It
// is the whole basis of the world-staleness distinction: what the fold can
// tell about a dead ancestor is the kind its author gave it.
func (f *foldState) isArtifact(event string) bool {
	record, ok := f.byID[event]
	if !ok || record.decision.Verdict != Effective {
		return false
	}
	state, ok := record.body.(*State)
	return ok && state.Kind == KindArtifact
}

// unableToFlare reports whether nothing in the log could ever make a statement
// with these bases stale. Citing nothing qualifies; so does citing only events
// the log does not contain, since supersede requires a resolvable target and
// no act can retire what is not there. One resolvable basis is enough to
// escape — it is a handle a future supersession can take hold of.
func (f *foldState) unableToFlare(restsOn []string) bool {
	for _, basis := range restsOn {
		if _, exists := f.byID[basis]; exists {
			return false
		}
	}
	return true
}

func (f *foldState) ratified(target string, retired map[string]bool) bool {
	for _, event := range f.ratifications[target] {
		if !retired[event] && f.decisions[event].Verdict == Effective {
			return true
		}
	}
	return false
}

func (f *foldState) project() Projection {
	retired := f.retired()
	stale, world := f.staleness(retired)
	// Every artifact seen at a path, in order, not only the most recent.
	// Tracking just the immediate predecessor let a retirement hide a live
	// ancestor: with A, B and C at one path, retiring B cleared C's warning
	// while A stayed live.
	seenByPath := make(map[string][]string)
	projection := Projection{
		Decisions: []Decision{}, Acts: []Act{}, Statements: []Statement{}, Commitments: []Commitment{}, Artifacts: []Artifact{},
		Actors: make(map[string]ActorState), Provenance: make(map[string][]string),
		OpaqueKinds: make(map[string][]string),
	}
	for _, record := range f.records {
		projection.Decisions = append(projection.Decisions, record.decision)
		if _, exists := projection.Provenance[record.record.ID]; !exists {
			projection.Provenance[record.record.ID] = append([]string(nil), record.record.RestsOn...)
		}
		switch act := record.body.(type) {
		case *Ratify:
			projection.Acts = append(projection.Acts, Act{Event: record.record.ID, Timestamp: record.record.Timestamp, Actor: record.record.Actor, Type: "ratify", Target: act.Target, Verdict: record.decision.Verdict, Reason: record.decision.Reason})
		case *Supersede:
			projection.Acts = append(projection.Acts, Act{Event: record.record.ID, Timestamp: record.record.Timestamp, Actor: record.record.Actor, Type: "supersede", Target: act.Target, Text: act.Text, Verdict: record.decision.Verdict, Reason: record.decision.Reason})
		}
		state, ok := record.body.(*State)
		if !ok {
			continue
		}
		projection.Statements = append(projection.Statements, Statement{
			Event: record.record.ID, Timestamp: record.record.Timestamp, Actor: record.record.Actor, Kind: state.Kind,
			Text: state.Text, Body: cloneStringMap(state.Body), Ratified: f.ratified(record.record.ID, retired),
			Retired: retired[record.record.ID], Stale: stale[record.record.ID],
			DescribesSupersededWorld: world[record.record.ID],
		})
		if !knownKinds[state.Kind] {
			projection.OpaqueKinds[string(state.Kind)] = append(projection.OpaqueKinds[string(state.Kind)], record.record.ID)
		}
		if state.Kind == KindArtifact {
			path := state.Body["path"]
			live := 0
			for _, predecessor := range seenByPath[path] {
				if !retired[predecessor] {
					live++
				}
			}
			projection.Artifacts = append(projection.Artifacts, Artifact{
				Event: record.record.ID, Path: path, Commit: state.Body["commit"],
				Stale:                    retired[record.record.ID] || stale[record.record.ID],
				DescribesSupersededWorld: world[record.record.ID],
				UnableToFlare:            f.unableToFlare(record.record.RestsOn),
				SuccessionUnrecorded:     live > 0,
				LivePredecessors:         live,
			})
			seenByPath[path] = append(seenByPath[path], record.record.ID)
		}
	}
	// A live artifact owes its retirement only while a live artifact later at
	// the same path stands in its place. A successor that was itself withdrawn
	// asks for nothing: acting on that warning would retire the current
	// artifact because of a replacement that no longer exists. Walking each
	// path backwards, a live artifact is owed a supersession when a live one
	// has already been passed. Counted here rather than summed from
	// LivePredecessors, which counts a shared ancestor once per successor.
	for _, events := range seenByPath {
		successor := false
		for i := len(events) - 1; i >= 0; i-- {
			if retired[events[i]] {
				continue
			}
			if successor {
				projection.OmittedSupersessions++
			}
			successor = true
		}
	}
	for _, grant := range f.roleGrants {
		if !f.roleGrantActive(grant) {
			continue
		}
		actor := projection.Actors[grant.actor]
		if actor.RoleSources == nil {
			actor.RoleSources = make(map[string][]string)
		}
		addActorRole(&actor, grant.role, grant.statement)
		if grant.membership {
			if grant.name != "" {
				actor.Name = grant.name
			}
			if grant.kind != "" {
				actor.Kind = grant.kind
			}
			actor.MembershipEvent = grant.statement
			addActorRole(&actor, "participant", grant.statement)
		}
		if grant.role == "operator" {
			addActorRole(&actor, "ratifier", "")
		}
		projection.Actors[grant.actor] = actor
	}
	for fingerprint, actor := range projection.Actors {
		sort.Strings(actor.Roles)
		for role := range actor.RoleSources {
			sort.Strings(actor.RoleSources[role])
		}
		projection.Actors[fingerprint] = actor
	}
	projection.Commitments = f.projectCommitments(retired, stale)
	if projection.Commitments == nil {
		projection.Commitments = []Commitment{}
	}
	if len(projection.OpaqueKinds) == 0 {
		projection.OpaqueKinds = nil
	}
	return projection
}

func cloneStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func addActorRole(actor *ActorState, role, source string) {
	actor.Roles = appendUnique(actor.Roles, role)
	if source != "" {
		actor.RoleSources[role] = appendUnique(actor.RoleSources[role], source)
	}
}

func (f *foldState) projectCommitments(retired, stale map[string]bool) []Commitment {
	var commitments []Commitment
	for _, requestRecord := range f.records {
		request, ok := requestRecord.body.(*State)
		if !ok || request.Kind != KindRequest || requestRecord.decision.Verdict != Effective {
			continue
		}
		promises := f.directDependents(requestRecord.record.ID, KindPromise)
		if len(promises) == 0 {
			status := "requested"
			if retired[requestRecord.record.ID] {
				status = "withdrawn"
			} else if stale[requestRecord.record.ID] {
				status = "stale"
			}
			commitments = append(commitments, Commitment{Request: requestRecord.record.ID, Requester: requestRecord.record.Actor, Performer: request.Body["to"], Status: status, WaitingOn: request.Body["to"]})
			continue
		}
		for _, promiseRecord := range promises {
			entry := Commitment{Request: requestRecord.record.ID, Requester: requestRecord.record.Actor, Performer: promiseRecord.record.Actor, Promise: promiseRecord.record.ID, Status: "promised", WaitingOn: promiseRecord.record.Actor}
			switch {
			case retired[requestRecord.record.ID]:
				entry.Status = "cancelled"
				entry.WaitingOn = ""
			case retired[promiseRecord.record.ID]:
				entry.Status = "reneged"
				entry.WaitingOn = ""
			case stale[requestRecord.record.ID] || stale[promiseRecord.record.ID]:
				entry.Status = "stale"
			default:
				reports := f.directDependents(promiseRecord.record.ID, KindReport)
				for index := len(reports) - 1; index >= 0; index-- {
					report := reports[index]
					if retired[report.record.ID] {
						continue
					}
					entry.Report = report.record.ID
					entry.Status = "reported"
					entry.WaitingOn = requestRecord.record.Actor
					if stale[report.record.ID] {
						entry.Status = "stale"
					} else if f.ratified(report.record.ID, retired) {
						entry.Status = "satisfied"
						entry.WaitingOn = ""
					}
					break
				}
			}
			commitments = append(commitments, entry)
		}
	}
	return commitments
}

func (f *foldState) directDependents(target string, kind Kind) []*parsedRecord {
	return f.dependents[dependentKind{basis: target, kind: kind}]
}

func appendUnique(values []string, value string) []string {
	if !contains(values, value) {
		return append(values, value)
	}
	return values
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func (p Projection) Explain(event string) []string {
	seen := make(map[string]bool)
	var result []string
	var walk func(string)
	walk = func(current string) {
		if current == "" || seen[current] {
			return
		}
		seen[current] = true
		result = append(result, current)
		for _, basis := range p.Provenance[current] {
			walk(basis)
		}
	}
	walk(event)
	return result
}

func (p Projection) Decision(event string) (Decision, bool) {
	for _, decision := range p.Decisions {
		if decision.Event == event {
			return decision, true
		}
	}
	return Decision{}, false
}

func (p Projection) Summary() string {
	counts := make(map[string]int)
	for _, commitment := range p.Commitments {
		counts[commitment.Status]++
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var parts []string
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", key, counts[key]))
	}
	return strings.Join(parts, " ")
}
