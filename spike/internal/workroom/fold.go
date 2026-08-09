package workroom

import (
	"fmt"
	"sort"
	"strings"
)

type Verdict string

const (
	Effective       Verdict = "effective"
	Ineffective     Verdict = "ineffective"
	Disputed        Verdict = "disputed"
	UndefinedKind   Verdict = "undefined-kind"
	Uninterpretable Verdict = "uninterpretable"
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
	Request     string `json:"request"`
	Requester   string `json:"requester"`
	AddressedTo string `json:"addressed_to,omitempty"`
	Performer   string `json:"performer,omitempty"`
	Promise     string `json:"promise,omitempty"`
	Report      string `json:"report,omitempty"`
	Status      string `json:"status"`
	WaitingOn   string `json:"waiting_on,omitempty"`
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
	record     Record
	body       any
	index      int
	decision   Decision
	definition *KindDefinition
	declared   *KindDefinition
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

// dependentKey indexes effective statements by the basis they cite and the
// lifecycle role their governing definition gives them. Lifecycle rather than
// kind, because a declared kind may take a lifecycle role under any name.
type dependentKey struct {
	basis     string
	lifecycle Lifecycle
}

type foldState struct {
	records            []parsedRecord
	byID               map[string]*parsedRecord
	decisions          map[string]Decision
	effectiveSup       map[string]string
	retirementCauses   map[string]int
	roleGrants         []roleGrant
	roleGrantsByRole   map[actorRole][]roleGrant
	membershipGrants   map[actorStatement][]roleGrant
	ratifications      map[string][]string
	dependents         map[dependentKey][]*parsedRecord
	definitions        map[Kind]KindDefinition
	definitionVersions map[Kind][]*parsedRecord
	transitions        []FoldTransition
	beyondSeam         bool
}

// Folder retains the verified application state for a resident reader. It is
// deliberately not concurrency-safe; the application owns serialization.
// Projection may be called after any append and preserves Fold's output.
type Folder struct {
	state *foldState
}

// FoldResult carries both faces of one fold: what the log says, and the
// vocabulary the log was read with.
type FoldResult struct {
	Projection Projection
	Vocabulary Vocabulary
}

func Fold(records []Record) Projection {
	folder := NewFolder(records)
	return folder.Projection()
}

// Evaluate folds a whole log and returns the projection alongside the
// vocabulary that governed it.
func Evaluate(records []Record) FoldResult {
	folder := NewFolder(records)
	return FoldResult{Projection: folder.Projection(), Vocabulary: folder.Vocabulary()}
}

// NewFolder constructs the same state as Fold while allowing later verified
// records to be applied without decoding and judging the prefix again.
func NewFolder(records []Record) *Folder {
	state := &foldState{
		byID: make(map[string]*parsedRecord), decisions: make(map[string]Decision),
		effectiveSup:       make(map[string]string),
		retirementCauses:   make(map[string]int),
		roleGrantsByRole:   make(map[actorRole][]roleGrant),
		membershipGrants:   make(map[actorStatement][]roleGrant),
		ratifications:      make(map[string][]string),
		dependents:         make(map[dependentKey][]*parsedRecord),
		definitions:        starterCatalog(),
		definitionVersions: make(map[Kind][]*parsedRecord),
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

// Vocabulary renders the kinds this fold interprets and how its interpreter
// is bound.
func (f *Folder) Vocabulary() Vocabulary {
	return f.state.vocabulary()
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
	if len(f.transitions) != 0 {
		f.beyondSeam = true
		decision.Verdict = Uninterpretable
		decision.Reason = "uninterpretable: activated interpreter execution is not held"
		f.addDecision(record, parsed, index, decision)
		return
	}
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
	if _, ok := body.(*State); ok && parsed.definition != nil {
		// A record may repeat a basis. directDependents historically returned
		// the record once because it used contains; preserve that behavior while
		// indexing every effective statement in one pass.
		seen := make(map[string]struct{}, len(record.RestsOn))
		for _, basis := range record.RestsOn {
			if _, exists := seen[basis]; exists {
				continue
			}
			seen[basis] = struct{}{}
			key := dependentKey{basis: basis, lifecycle: parsed.definition.Lifecycle}
			f.dependents[key] = append(f.dependents[key], parsed)
		}
	}
	switch value := body.(type) {
	case *State:
		if value.Kind == KindRoster && index == 0 {
			f.addRoleGrant(value.Body["actor"], value.Body["name"], value.Body["kind"], value.Body["role"], record.ID, "", record.RestsOn)
		}
		if value.Kind == KindKindDef && parsed.declared != nil {
			f.definitionVersions[parsed.declared.Name] = append(f.definitionVersions[parsed.declared.Name], parsed)
		}
	case *Ratify:
		f.ratifications[value.Target] = append(f.ratifications[value.Target], record.ID)
		if target := f.byID[value.Target]; target != nil {
			if roster, ok := target.body.(*State); ok && roster.Kind == KindRoster {
				f.addRoleGrant(roster.Body["actor"], roster.Body["name"], roster.Body["kind"], roster.Body["role"], value.Target, record.ID, target.record.RestsOn)
			}
			if statement, ok := target.body.(*State); ok && statement.Kind == KindKindDef {
				f.refreshDefinition(target.declared.Name)
			}
			if statement, ok := target.body.(*State); ok && statement.Kind == KindFoldActivation {
				f.transitions = append(f.transitions, FoldTransition{
					Activation: value.Target, Ratification: record.ID,
					Fold: statement.Body["fold"], Entry: statement.Body["entry"],
					Interface: statement.Body["interface"], Toolchain: statement.Body["toolchain"],
					Prefix: statement.Body["prefix"] == "genesis",
				})
			}
		}
	case *Supersede:
		f.effectiveSup[record.ID] = value.Target
		changed := f.changeRetirement(value.Target, 1)
		f.refreshDefinitionsAffectedBy(changed)
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
	definition, exists := f.definitions[state.Kind]
	if !exists {
		return Decision{Event: record.record.ID, Verdict: UndefinedKind, Reason: fmt.Sprintf("undefined kind %q", state.Kind)}
	}
	record.definition = &definition
	if err := validateFields(definition, state.Body); err != nil {
		verdict := Ineffective
		if state.Kind == KindKindDef || state.Kind == KindFoldActivation {
			verdict = Uninterpretable
		}
		return Decision{Event: record.record.ID, Verdict: verdict, Reason: err.Error()}
	}
	if state.Kind == KindKindDef {
		declared, err := decodeKindDefinition(state, record.record.ID)
		if err != nil {
			return Decision{Event: record.record.ID, Verdict: Uninterpretable, Reason: "uninterpretable kind definition: " + err.Error()}
		}
		for _, constraint := range declared.Basis {
			for _, kind := range constraint.Kinds {
				if _, defined := f.definitions[kind]; !defined {
					return Decision{Event: record.record.ID, Verdict: Uninterpretable, Reason: fmt.Sprintf("uninterpretable kind definition: basis kind %q is undefined", kind)}
				}
			}
		}
		record.declared = &declared
	}
	if record.index == 0 {
		if state.Kind != KindRoster || state.Body["actor"] != record.record.Actor || state.Body["role"] != "operator" {
			return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: "first event must self-seed the operator roster"}
		}
		return Decision{Event: record.record.ID, Verdict: Effective, Reason: "operator roster seed"}
	}
	if err := f.validateBasis(record, definition); err != nil {
		return *err
	}
	if definition.Lifecycle == LifecycleRequest && !f.hasActor(state.Body["to"]) {
		return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: "requested performer is not in the live roster"}
	}
	if definition.Lifecycle == LifecyclePromise {
		requests := f.basesOfLifecycle(record.record.RestsOn, LifecycleRequest)
		if len(requests) != 1 {
			verdict := Ineffective
			if len(requests) > 1 {
				verdict = Disputed
			}
			return Decision{Event: record.record.ID, Verdict: verdict, Reason: fmt.Sprintf("promise lifecycle basis count is %d, want exactly one request", len(requests))}
		}
		request := requests[0].body.(*State)
		if request.Body["to"] != record.record.Actor {
			return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: "promise actor is not the requested performer"}
		}
	}
	if definition.Lifecycle == LifecycleReport {
		promises := f.basesOfLifecycle(record.record.RestsOn, LifecyclePromise)
		if len(promises) != 1 {
			verdict := Ineffective
			if len(promises) > 1 {
				verdict = Disputed
			}
			return Decision{Event: record.record.ID, Verdict: verdict, Reason: fmt.Sprintf("report lifecycle basis count is %d, want exactly one promise", len(promises))}
		}
		if promises[0].record.Actor != record.record.Actor {
			return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: "only the promisor may report completion"}
		}
	}
	if state.Kind == KindRoster && state.Body["role"] == "participant" && state.Body["kind"] == "" {
		return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: "participant roster state requires body.kind"}
	}
	return decision
}

func (f *foldState) validateBasis(record *parsedRecord, definition KindDefinition) *Decision {
	for _, constraint := range definition.Basis {
		allowed := make(map[Kind]bool, len(constraint.Kinds))
		for _, kind := range constraint.Kinds {
			allowed[kind] = true
		}
		count := 0
		for _, reference := range record.record.RestsOn {
			basis := f.byID[reference]
			if basis == nil || f.decisions[reference].Verdict != Effective {
				continue
			}
			statement, ok := basis.body.(*State)
			if ok && allowed[statement.Kind] {
				count++
			}
		}
		if count >= constraint.Min && count <= constraint.Max {
			continue
		}
		verdict := Ineffective
		if count > constraint.Max {
			verdict = Disputed
		}
		reason := fmt.Sprintf("%s basis count is %d, want %d..%d of [%s]", definition.Name, count, constraint.Min, constraint.Max, joinKinds(constraint.Kinds))
		switch definition.Lifecycle {
		case LifecyclePromise:
			if count == 0 {
				reason = "dangling promise has no request"
			} else {
				reason = "promise rests on multiple requests"
			}
		case LifecycleReport:
			if count == 0 {
				reason = "report has no promise"
			} else {
				reason = "report rests on multiple promises"
			}
		}
		return &Decision{Event: record.record.ID, Verdict: verdict, Reason: reason}
	}
	return nil
}

func joinKinds(kinds []Kind) string {
	values := make([]string, len(kinds))
	for index, kind := range kinds {
		values[index] = string(kind)
	}
	return strings.Join(values, ",")
}

// refreshDefinition recomputes which declaration of a kind governs from here
// on. Force arrives at ratification, so among the surviving ratified versions
// the governing one is whichever holds the live ratification standing latest in
// the total order — not whichever was declared last. Restoring a retired
// version therefore cannot displace a version ratified after it. Nothing here
// revisits an emitted decision: the refresh only chooses the definition that
// later positions will be judged against.
func (f *foldState) refreshDefinition(name Kind) {
	if starter, exists := starterCatalog()[name]; exists {
		f.definitions[name] = starter
	} else {
		delete(f.definitions, name)
	}
	var governing *parsedRecord
	var ratifiedBy string
	governingAt := -1
	for _, record := range f.definitionVersions[name] {
		if record.decision.Verdict != Effective || f.retired(record.record.ID) {
			continue
		}
		ratification := f.activeRatification(record.record.ID)
		if ratification == "" || f.byID[ratification].index < governingAt {
			continue
		}
		governing, ratifiedBy, governingAt = record, ratification, f.byID[ratification].index
	}
	if governing != nil {
		definition := *governing.declared
		definition.RatifiedBy = ratifiedBy
		f.definitions[name] = definition
	}
}

func (f *foldState) refreshDefinitionsAffectedBy(events []string) {
	names := make(map[Kind]bool)
	for _, event := range events {
		record := f.byID[event]
		if record == nil {
			continue
		}
		if record.declared != nil {
			names[record.declared.Name] = true
		}
		if ratification, ok := record.body.(*Ratify); ok {
			if target := f.byID[ratification.Target]; target != nil && target.declared != nil {
				names[target.declared.Name] = true
			}
		}
	}
	for name := range names {
		f.refreshDefinition(name)
	}
}

func (f *foldState) activeRatification(target string) string {
	for _, event := range f.ratifications[target] {
		if !f.retired(event) && f.decisions[event].Verdict == Effective {
			return event
		}
	}
	return ""
}

func (f *foldState) decideRatify(record *parsedRecord, ratify Ratify) Decision {
	target := f.byID[ratify.Target]
	if target == nil {
		return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: "ratify target is unknown"}
	}
	if len(record.record.RestsOn) != 1 || record.record.RestsOn[0] != ratify.Target {
		return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: "ratify must rest on exactly its target"}
	}
	_, ok := target.body.(*State)
	if !ok {
		return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: "only statements may be ratified"}
	}
	if target.decision.Verdict != Effective {
		return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: "ratify target is not effective"}
	}
	if f.retired(ratify.Target) {
		return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: "retired statement cannot be ratified"}
	}
	// An effective statement always carries the definition it was judged
	// against, so the satisfier below is always present.
	if target.declared != nil {
		if active, exists := f.definitions[target.declared.Name]; exists && active.Source != "starter" && active.Source != target.record.ID {
			return Decision{Event: record.record.ID, Verdict: Disputed, Reason: "kind definition has a live predecessor; supersede it before ratifying a replacement"}
		}
	}
	satisfier := target.definition.Satisfier
	if satisfier == SatisfierOriginatingRequester {
		request := f.originatingRequest(target)
		if request == nil {
			return Decision{Event: record.record.ID, Verdict: Disputed, Reason: "report has no unique originating requester"}
		}
		if request.record.Actor != record.record.Actor {
			return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: "only the requester may declare satisfaction"}
		}
		return Decision{Event: record.record.ID, Verdict: Effective, Reason: "requester declared satisfaction"}
	}
	if strings.HasPrefix(satisfier, "role:") {
		role := strings.TrimPrefix(satisfier, "role:")
		if !f.hasRole(record.record.Actor, role) {
			return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: fmt.Sprintf("actor lacks %s role", role)}
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
	if f.retired(grant.statement) || f.decisions[grant.statement].Verdict != Effective {
		return false
	}
	if grant.ratification == "" {
		return true
	}
	return !f.retired(grant.ratification) && f.decisions[grant.ratification].Verdict == Effective
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

func (f *foldState) originatingRequest(report *parsedRecord) *parsedRecord {
	promises := f.basesOfLifecycle(report.record.RestsOn, LifecyclePromise)
	if len(promises) != 1 {
		return nil
	}
	requests := f.basesOfLifecycle(promises[0].record.RestsOn, LifecycleRequest)
	if len(requests) != 1 {
		return nil
	}
	return requests[0]
}

// changeRetirement maintains the live supersession projection as each
// effective supersession lands. A target changes state only when its count of
// live superseders crosses zero. If the target is itself a supersession, that
// transition removes or restores its effect and continues down the chain.
func (f *foldState) changeRetirement(target string, delta int) []string {
	var changed []string
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
			return changed
		}
		changed = append(changed, target)
		next, supersession := f.effectiveSup[target]
		if !supersession {
			return changed
		}
		if isRetired {
			delta = -1
		} else {
			delta = 1
		}
		target = next
	}
	return changed
}

// retired reads the live supersession counter directly. The counter is the
// projection of retirement, so nothing needs to materialise the whole retired
// set to answer one question about one event.
func (f *foldState) retired(event string) bool {
	return f.retirementCauses[event] != 0
}

// staleness returns transitive staleness and, narrowing it, the records whose
// staleness traces back to a retired artifact. A record is world-stale when a
// retired artifact is the basis that made it stale, or when it rests on
// something already world-stale, so the distinction survives any number of
// hops. Records are visited in sequence order and a basis is always cited
// before its dependent, so one pass settles both maps. Which bases can carry
// staleness at all is the governing definition's business: an exempt kind
// neither catches staleness nor passes it on, and a terminal one catches it
// without passing it on.
func (f *foldState) staleness() (map[string]bool, map[string]bool) {
	stale := make(map[string]bool)
	world := make(map[string]bool)
	for _, record := range f.records {
		if record.decision.Verdict != Effective {
			continue
		}
		if record.definition != nil && record.definition.Staleness == StalenessExempt {
			continue
		}
		for _, basis := range record.record.RestsOn {
			if target, ok := f.effectiveSup[record.record.ID]; ok && target == basis {
				continue
			}
			basisRecord := f.byID[basis]
			mode := StalenessPropagates
			if basisRecord != nil && basisRecord.definition != nil {
				mode = basisRecord.definition.Staleness
			}
			retiredBasis := f.retired(basis) && mode != StalenessExempt
			staleBasis := stale[basis] && mode == StalenessPropagates
			if !retiredBasis && !staleBasis {
				continue
			}
			stale[record.record.ID] = true
			if world[basis] || (retiredBasis && f.isArtifact(basis)) {
				world[record.record.ID] = true
				break
			}
		}
	}
	return stale, world
}

// isArtifact reports whether an event is an effective artifact statement. It
// is the whole basis of the world-staleness distinction: what the fold can
// tell about a dead ancestor is how its governing definition renders it.
func (f *foldState) isArtifact(event string) bool {
	record, ok := f.byID[event]
	if !ok || record.decision.Verdict != Effective || record.definition == nil {
		return false
	}
	_, ok = record.body.(*State)
	return ok && record.definition.Render == RenderArtifact
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

func (f *foldState) ratified(target string) bool {
	return f.activeRatification(target) != ""
}

func (f *foldState) project() Projection {
	stale, world := f.staleness()
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
			Text: state.Text, Body: cloneStringMap(state.Body), Ratified: f.ratified(record.record.ID),
			Retired: f.retired(record.record.ID), Stale: stale[record.record.ID],
			DescribesSupersededWorld: world[record.record.ID],
		})
		if record.decision.Verdict == UndefinedKind {
			projection.OpaqueKinds[string(state.Kind)] = append(projection.OpaqueKinds[string(state.Kind)], record.record.ID)
		}
		// Only an effective statement points at anything. A refused one keeps
		// its decision and its statement row; it does not become a live
		// artifact carrying whatever fields it did manage to fill.
		if record.decision.Verdict == Effective && record.definition != nil && record.definition.Render == RenderArtifact {
			path := state.Body["path"]
			live := 0
			for _, predecessor := range seenByPath[path] {
				if !f.retired(predecessor) {
					live++
				}
			}
			projection.Artifacts = append(projection.Artifacts, Artifact{
				Event: record.record.ID, Path: path, Commit: state.Body["commit"],
				Stale:                    f.retired(record.record.ID) || stale[record.record.ID],
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
			if f.retired(events[i]) {
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
	projection.Commitments = f.projectCommitments(stale)
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

func (f *foldState) vocabulary() Vocabulary {
	binding := FoldBinding{Status: "unbound", Reason: "no ratified seed or prefix binding", Transitions: []FoldTransition{}}
	if len(f.transitions) != 0 {
		binding.Transitions = append(binding.Transitions, f.transitions...)
		// A prefix binding governs from genesis to its transition, so a record
		// that reaches no further than the transition is wholly bound. Only
		// beyond the seam would the named fold have to run, and running it is
		// what this fold cannot do.
		binding.Status, binding.Reason = "bound", ""
		if f.beyondSeam {
			binding.Status = "uninterpretable"
			binding.Reason = "activated interpreter execution is not held"
		}
	}
	return Vocabulary{Definitions: sortedDefinitions(f.definitions), Binding: binding}
}

func addActorRole(actor *ActorState, role, source string) {
	actor.Roles = appendUnique(actor.Roles, role)
	if source != "" {
		actor.RoleSources[role] = appendUnique(actor.RoleSources[role], source)
	}
}

func (f *foldState) projectCommitments(stale map[string]bool) []Commitment {
	var commitments []Commitment
	for _, requestRecord := range f.records {
		request, ok := requestRecord.body.(*State)
		if !ok || requestRecord.definition == nil || requestRecord.definition.Lifecycle != LifecycleRequest || requestRecord.decision.Verdict != Effective {
			continue
		}
		promises := f.directDependents(requestRecord.record.ID, LifecyclePromise)
		if len(promises) == 0 {
			status := "open"
			if f.retired(requestRecord.record.ID) {
				status = "withdrawn"
			} else if stale[requestRecord.record.ID] {
				status = "stale"
			}
			commitments = append(commitments, Commitment{Request: requestRecord.record.ID, Requester: requestRecord.record.Actor, AddressedTo: request.Body["to"], Status: status})
			continue
		}
		for _, promiseRecord := range promises {
			entry := Commitment{Request: requestRecord.record.ID, Requester: requestRecord.record.Actor, Performer: promiseRecord.record.Actor, Promise: promiseRecord.record.ID, Status: "promised", WaitingOn: promiseRecord.record.Actor}
			switch {
			case f.retired(requestRecord.record.ID):
				entry.Status = "cancelled"
				entry.WaitingOn = ""
			case f.retired(promiseRecord.record.ID):
				entry.Status = "reneged"
				entry.WaitingOn = ""
			case stale[requestRecord.record.ID] || stale[promiseRecord.record.ID]:
				entry.Status = "stale"
				entry.WaitingOn = ""
			default:
				reports := f.directDependents(promiseRecord.record.ID, LifecycleReport)
				for index := len(reports) - 1; index >= 0; index-- {
					report := reports[index]
					if f.retired(report.record.ID) {
						continue
					}
					entry.Report = report.record.ID
					entry.Status = "reported"
					entry.WaitingOn = requestRecord.record.Actor
					if stale[report.record.ID] {
						entry.Status = "stale"
						entry.WaitingOn = ""
					} else if f.ratified(report.record.ID) {
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

func (f *foldState) directDependents(target string, lifecycle Lifecycle) []*parsedRecord {
	return f.dependents[dependentKey{basis: target, lifecycle: lifecycle}]
}

func (f *foldState) basesOfLifecycle(refs []string, lifecycle Lifecycle) []*parsedRecord {
	var found []*parsedRecord
	for _, ref := range refs {
		record := f.byID[ref]
		if record == nil || f.decisions[ref].Verdict != Effective || record.definition == nil {
			continue
		}
		if record.definition.Lifecycle == lifecycle {
			found = append(found, record)
		}
	}
	return found
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
