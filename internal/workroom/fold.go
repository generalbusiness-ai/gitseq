package workroom

import (
	"encoding/json"
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
	Event    string  `json:"event"`
	Sequence int     `json:"sequence"`
	Verdict  Verdict `json:"verdict"`
	Reason   string  `json:"reason"`
}

type Statement struct {
	Event string `json:"event"`
	// Sequence is this event's position in the log, counting the founding seed
	// as 1. It is derived from the fold's own per-record index rather than
	// assigned, so re-folding the same log yields the same number for every
	// reader — which is the whole point of naming an event by it.
	//
	// It means something only within one genesis. Two workrooms both have a
	// #17, and they are unrelated. Anything crossing that boundary needs the
	// canonical identifier, and the fold still resolves citations only by that
	// identifier: this is a name for reading and saying, not a second name the
	// log accepts.
	Sequence  int               `json:"sequence"`
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
	Stale       bool   `json:"stale,omitempty"`
	WaitingOn   string `json:"waiting_on,omitempty"`
}

type Artifact struct {
	Event  string `json:"event"`
	Path   string `json:"path"`
	Commit string `json:"commit"`
	// Retired records that this artifact statement was itself superseded. The
	// pointer has been withdrawn and nothing may rest on it again.
	Retired bool `json:"retired,omitempty"`
	// Succeeded narrows Retired: the act that withdrew this pointer also rested
	// on an artifact covering the same path, so it says where the behaviour
	// went. A retirement that names nothing is a condemnation, and the two
	// mean opposite things to whoever stood on the artifact — one is answered,
	// the other has to go and look.
	Succeeded bool `json:"succeeded,omitempty"`
	// Stale records that a basis under this artifact was retired. It is a
	// different fact from Retired, and carrying both in one boolean cost every
	// reader the ability to tell a withdrawn pointer from a moved world. The
	// commit named here is immutable either way: staleness is a reason to
	// re-check the reasoning, not evidence that the pointer stopped pointing.
	Stale bool `json:"stale"`
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
	Name               string              `json:"name"`
	Kind               string              `json:"kind,omitempty"`
	Roles              []string            `json:"roles"`
	MembershipEvent    string              `json:"membership_event,omitempty"`
	RoleSources        map[string][]string `json:"role_sources"`
	DormantRoleSources map[string][]string `json:"dormant_role_sources"`
	RetiredRoleSources map[string][]string `json:"retired_role_sources"`
	// Retired records a principal whose every membership grant has been
	// superseded. It holds no roles and may no longer be addressed or ratify,
	// but it stays in the roster because it signed events that are permanent:
	// dropping it would leave those signatures attributed to nothing. A reader
	// must never confuse it with a live actor, so the flag is the distinction
	// and the empty role list is its consequence.
	Retired bool `json:"retired,omitempty"`
}

// Independence values for Review. A review whose implementer cannot be
// identified is unresolved rather than independent: the record does not know,
// and saying so is the whole point of the field.
const (
	IndependenceIndependent = "independent"
	IndependenceSelfReview  = "self-review"
	IndependenceUnresolved  = "unresolved"
)

// Review projects a report carrying a verdict against an implementation head,
// and answers the one question the chain shape alone cannot: whether the actor
// who signed the verdict is the actor who made the thing reviewed. Reviewer and
// implementer are fingerprints, so the answer does not depend on remembering
// who did the work or on two agents happening to be configured under different
// names.
type Review struct {
	Report    string `json:"report"`
	Timestamp int64  `json:"timestamp,omitempty"`
	Reviewer  string `json:"reviewer"`
	Verdict   string `json:"verdict"`
	Head      string `json:"head,omitempty"`
	Artifact  string `json:"artifact,omitempty"`
	// Implementer is the actor who signed the artifact statement for the
	// reviewed head, empty when no artifact could be identified.
	Implementer string `json:"implementer,omitempty"`
	// ResolvedBy names how the artifact was found — named, basis, or head —
	// so a reader can see which inference the verdict on independence rests on
	// rather than taking it on trust.
	ResolvedBy   string `json:"resolved_by,omitempty"`
	Independence string `json:"independence"`
	Ratified     bool   `json:"ratified,omitempty"`
	Retired      bool   `json:"retired,omitempty"`
	Stale        bool   `json:"stale,omitempty"`
}

type Projection struct {
	Decisions   []Decision            `json:"decisions"`
	Acts        []Act                 `json:"acts"`
	Statements  []Statement           `json:"statements"`
	Commitments []Commitment          `json:"commitments"`
	Reviews     []Review              `json:"reviews"`
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

// sequence is this record's position as a reader says it: the founding seed is
// #1, not #0. Counting from one is not cosmetic — an off-by-one in a name
// people type at each other is a defect that never stops costing.
func (r *parsedRecord) sequence() int { return r.index + 1 }

type parsedRecord struct {
	record     Record
	body       any
	index      int
	decision   Decision
	definition *KindDefinition
	declared   *KindDefinition
	mergePlan  map[string]string
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
	strings            map[string]string
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
		strings:            make(map[string]string),
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
	record.ID = f.intern(record.ID)
	record.Actor = f.intern(record.Actor)
	record.Schema = f.intern(record.Schema)
	for index := range record.RestsOn {
		record.RestsOn[index] = f.intern(record.RestsOn[index])
	}
	if _, exists := f.byID[record.ID]; exists {
		decision.Verdict = Disputed
		decision.Reason = "duplicate event id"
		f.addDecision(record, nil, index, decision)
		return
	}
	body, err := decode(record.Schema, record.Payload, f.strings)
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
	f.internBody(body)
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
	if state, ok := body.(*State); ok && state.Kind == KindAssert {
		parsed.mergePlan = f.validateMergeReceiptNow(parsed)
		f.records[len(f.records)-1].mergePlan = parsed.mergePlan
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

// intern lets the resident retain one backing string for durable values that
// recur throughout the log. Event identifiers are especially repetitive:
// every identifier is also cited by later records, indexed by the fold, and
// projected for readers. Keeping a distinct decoded copy at each occurrence
// made full-rebuild memory scale with references rather than with information.
func (f *foldState) intern(value string) string {
	if value == "" {
		return ""
	}
	if existing, ok := f.strings[value]; ok {
		return existing
	}
	f.strings[value] = value
	return value
}

func (f *foldState) internBody(body any) {
	switch value := body.(type) {
	case *State:
		value.Kind = Kind(f.intern(string(value.Kind)))
		value.Text = f.intern(value.Text)
		if len(value.Body) != 0 {
			pooled := make(map[string]string, len(value.Body))
			for key, field := range value.Body {
				pooled[f.intern(key)] = f.intern(field)
			}
			value.Body = pooled
		}
	case *Ratify:
		value.Target = f.intern(value.Target)
	case *Supersede:
		value.Target = f.intern(value.Target)
		value.Text = f.intern(value.Text)
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
	if !exists && state.Kind == KindFoldActivation && (record.record.Schema == SchemaStateLegacy || record.record.Schema == SchemaStateV1) {
		definition, exists = legacyFoldActivationDefinition(), true
	}
	if !exists {
		return Decision{Event: record.record.ID, Verdict: UndefinedKind, Reason: fmt.Sprintf("undefined kind %q", state.Kind)}
	}
	record.definition = &definition
	// state@1 closes the two path shapes which cannot participate in merge
	// succession. Keeping the rule off state@0 is deliberate: old
	// records are append-only history, and changing their old decisions during
	// a refold would erase effective artifacts from provenance.
	if record.record.Schema != SchemaStateLegacy && definition.Render == RenderArtifact {
		path := state.Body["path"]
		if path == "." {
			return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: "artifact path must not be the whole-repository path ."}
		}
		if strings.Contains(path, ",") {
			return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: "artifact path must name one path, not a comma-joined pseudo-path"}
		}
	}
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

// activeRatification returns the surviving effective ratification of a target
// that stands latest in the total order. A statement may be ratified more than
// once — retire the ratification, restore the statement, ratify it again — and
// the governing-version selector compares positions, so it must be handed the
// latest position and not merely the first one still standing. Ratifications
// are appended in sequence order, so the last survivor in the list is it.
func (f *foldState) activeRatification(target string) string {
	events := f.ratifications[target]
	for index := len(events) - 1; index >= 0; index-- {
		event := events[index]
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
	if state := target.body.(*State); state.Kind == KindFoldActivation && record.record.Schema != SchemaRatifyLegacy {
		return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: "fold activation moved to the host binding"}
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
	if state := target.body.(*State); state.Kind == KindRoster {
		normalized := normalizeRosterState(state.Body["kind"], state.Body["role"])
		if normalized.authorityRole != "" {
			beneficiary := state.Body["actor"]
			if beneficiary == target.record.Actor || beneficiary == record.record.Actor {
				return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: "authority grant cannot be authored or ratified by its beneficiary"}
			}
			if normalized.authorityRole == "operator" && !f.hasRole(record.record.Actor, "operator") {
				return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: "operator standing is required to ratify an operator grant"}
			}
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
	if governance, restoring, ok := f.governanceTarget(supersede.Target); ok {
		if governance.index == 0 {
			return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: "founding operator seed cannot be retired"}
		}
		state := governance.body.(*State)
		normalized := normalizeRosterState(state.Body["kind"], state.Body["role"])
		role := normalized.authorityRole
		operatorMembership := normalized.membership && f.membershipCarriesOperator(state.Body["actor"], governance.record.ID)
		if role == "operator" || operatorMembership {
			reason := "operator standing is required to change an operator's membership"
			if role == "operator" && restoring {
				reason = "operator standing is required to restore an operator grant"
			} else if role == "operator" {
				reason = "operator standing is required to change an operator grant"
			} else if restoring {
				reason = "operator standing is required to restore operator-bearing membership"
			}
			if !f.hasRole(record.record.Actor, "operator") {
				return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: reason}
			}
		} else if !f.hasRole(record.record.Actor, "ratifier") {
			return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: "ratifier standing is required to change roster governance"}
		}
		return Decision{Event: record.record.ID, Verdict: Effective, Reason: "authorized governance supersession"}
	}
	if target.record.Actor == record.record.Actor || f.hasRole(record.record.Actor, "ratifier") {
		return Decision{Event: record.record.ID, Verdict: Effective, Reason: "authorized supersession"}
	}
	if f.isArtifact(supersede.Target) && f.hasAuthorizedMergeReceipt(record, supersede.Target) {
		return Decision{Event: record.record.ID, Verdict: Effective, Reason: "merge approval authorized artifact succession"}
	}
	return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: "actor may not supersede target"}
}

// hasAuthorizedMergeReceipt admits the one cross-author supersession which is
// not a free-standing act. The signer must cite its own merge receipt, that
// receipt must carry the complete ratified, independent exact-head approval
// chain, and it must be signed by the actor whose implementation the approval
// covers. A bare approval citation is intentionally insufficient: the authority
// is exercised by the merge, not made into a reusable retirement capability.
//
// The authority is also bounded by what the merge publishes. Every target must
// carry a successor path in the signed plan, that path must cover the target's
// own path, and the supersession must cite the successor artifact published at
// it. So the reach of a receipt is exactly the tree its merge republished:
// there is no way to name a stranger's artifact somewhere else and have the
// receipt carry it. A retirement with no successor — a deleted path — takes no
// authority from a merge and stays with the target's own author or a ratifier,
// because nothing the merge published stands over it to bound the claim.
func (f *foldState) hasAuthorizedMergeReceipt(record *parsedRecord, target string) bool {
	targetRecord := f.byID[target]
	if targetRecord == nil {
		return false
	}
	targetPath := ""
	if artifact, ok := targetRecord.body.(*State); ok {
		targetPath = artifact.Body["path"]
	}
	if targetPath == "" {
		return false
	}
	for _, basis := range record.record.RestsOn[1:] {
		receipt := f.byID[basis]
		if receipt == nil || receipt.decision.Verdict != Effective || f.retired(basis) || receipt.record.Actor != record.record.Actor {
			continue
		}
		successorPath, planned := f.mergeReceiptPlan(receipt)[target]
		if !planned || !pathCovers(successorPath, targetPath) {
			continue
		}
		if f.citesMergeSuccessor(record, receipt, successorPath) {
			return true
		}
	}
	return false
}

// pathCovers reports whether a successor at one path stands over a predecessor
// at another: the same string, or a directory containing it. Comparison is
// exact-string and slash-delimited, the same reading the projection gives every
// other artifact path.
func pathCovers(successor, predecessor string) bool {
	if successor == "" || predecessor == "" {
		return false
	}
	return successor == predecessor ||
		strings.HasPrefix(predecessor, strings.TrimSuffix(successor, "/")+"/")
}

func (f *foldState) citesMergeSuccessor(record, receipt *parsedRecord, path string) bool {
	state := receipt.body.(*State)
	for _, basis := range record.record.RestsOn[1:] {
		candidate := f.byID[basis]
		if candidate == nil || candidate.decision.Verdict != Effective || f.retired(basis) || candidate.definition == nil || candidate.definition.Render != RenderArtifact {
			continue
		}
		artifact, ok := candidate.body.(*State)
		if ok && artifact.Body["path"] == path && artifact.Body["commit"] == state.Body["merge_head"] && contains(candidate.record.RestsOn, receipt.record.ID) {
			return true
		}
	}
	return false
}

// mergeReceiptPlan returns the exact predecessor set authorized by a valid
// receipt. The plan is signed into the receipt so its authority cannot be
// borrowed to retire an unrelated artifact. Both the indexed record and the
// appended copy are written when the receipt is judged, so one lookup answers
// for either caller.
func (f *foldState) mergeReceiptPlan(receipt *parsedRecord) map[string]string {
	if receipt == nil || f.retired(receipt.record.ID) {
		return nil
	}
	return receipt.mergePlan
}

// validateMergeReceiptNow judges a merge receipt from the log alone and returns
// the retirement plan it is allowed to authorize, or nil.
//
// A receipt is otherwise entirely signer-written text, so what it may reach has
// to come from records its signer did not write. The approval must be
// effective, ratified, and approve the exact candidate. The implementation
// artifact it names must be cited by that approval, stand at that candidate,
// and be authored by someone other than the approver, so the approval is
// independent. The receipt must be signed by the author of that implementation
// artifact, because the merger of an approved head is the actor whose work it
// is. And the plan is then cut down to the targets standing on the same path
// lineage as that approved artifact.
//
// The last cut is what makes the authority real rather than declared. Every
// other field of a receipt — the merge head, the retirement plan, the successor
// list — is written by the same actor asking for the authority, and this fold
// is pure over records: it holds no repository, so it cannot open a merge
// commit or read a diff to check any of them. Without the cut, the author of
// one approved implementation could invent a merge head, name a stranger's
// artifact anywhere in the log, publish a successor at that path themselves,
// and retire it: an approval for one tree minted authority over every tree. The
// reviewer is the one party who did not write the receipt, so the reviewer's
// own signed choice of artifact is what bounds it.
//
// The cost is stated plainly rather than worked around: a merge touching
// several areas carries cross-author authority only within the approved
// artifact's tree. Everything else stays where it was before merge succession
// existed, with the target's author or an actor holding ratifier.
func (f *foldState) validateMergeReceiptNow(receipt *parsedRecord) map[string]string {
	if receipt == nil || receipt.decision.Verdict != Effective || f.retired(receipt.record.ID) {
		return nil
	}
	state, ok := receipt.body.(*State)
	if !ok || state.Kind != KindAssert || state.Body["merge_head"] == "" || state.Body["merge_target_pre_head"] == "" || state.Body["merge_candidate"] == "" || state.Body["merge_approval"] == "" {
		return nil
	}
	approvalID := state.Body["merge_approval"]
	if !contains(receipt.record.RestsOn, approvalID) {
		return nil
	}
	approval := f.byID[approvalID]
	if approval == nil || approval.decision.Verdict != Effective || f.retired(approvalID) || !f.ratified(approvalID) {
		return nil
	}
	verdict, ok := approval.body.(*State)
	if !ok || verdict.Kind != KindReport || verdict.Body["verdict"] != "approved" || verdict.Body["head"] != state.Body["merge_candidate"] {
		return nil
	}
	artifactID := verdict.Body["artifact"]
	artifact := f.byID[artifactID]
	if artifact == nil || artifact.decision.Verdict != Effective || f.retired(artifactID) || !contains(approval.record.RestsOn, artifactID) {
		return nil
	}
	implementation, ok := artifact.body.(*State)
	if !ok || artifact.definition == nil || artifact.definition.Render != RenderArtifact ||
		implementation.Body["commit"] != state.Body["merge_candidate"] || artifact.record.Actor == approval.record.Actor {
		return nil
	}
	implementer := artifact.record.Actor
	if receipt.record.Actor != implementer {
		return nil
	}
	if implementation.Body["path"] == "" {
		return nil
	}
	reviewed := f.reviewedPaths(approval, implementer, state.Body["merge_candidate"])
	if len(reviewed) == 0 {
		return nil
	}
	var plan map[string]string
	if err := json.Unmarshal([]byte(state.Body["merge_retirements"]), &plan); err != nil || plan == nil {
		return nil
	}
	reached := make(map[string]string, len(plan))
	for target, successor := range plan {
		path, isArtifact := f.artifactPath(target)
		if !isArtifact {
			continue
		}
		for _, within := range reviewed {
			if sameTreeLineage(within, path) {
				reached[target] = successor
				break
			}
		}
	}
	return reached
}

// reviewedPaths is what an approval puts within reach of the receipt that
// carries it: the artifacts the reviewer cited, and nothing else.
//
// One reviewed head is one body of work, and a body of work spans the paths it
// changes, so reading only the single artifact a verdict names left a head
// touching four maintained trees able to succeed the pointer in one of them.
// The fix is not to infer the rest. Anything inferred from what the implementer
// published is written by the actor asking for the authority: publishing a
// candidate at an unrelated path before requesting review, and then obtaining
// an approval that cites only the legitimate one, would have reached that path
// too. Ordering the claims before the verdict closes minting afterwards and
// does nothing about seeding beforehand.
//
// So the set is the approval's own direct bases. The reviewer chooses them and
// signs them, and a record's bases are fixed when it is signed, so nothing can
// be added afterwards. Each is still held to the facts the fold can check —
// effective, not withdrawn, standing at the exact head the verdict names, and
// the implementer's own — so a citation cannot smuggle in a pointer belonging
// to someone else or describing another commit.
//
// An approval citing one artifact reaches one path, which is what every
// approval written before this rule existed does.
func (f *foldState) reviewedPaths(approval *parsedRecord, implementer, head string) []string {
	var paths []string
	seen := make(map[string]bool)
	for _, basis := range approval.record.RestsOn {
		cited := f.byID[basis]
		if cited == nil || cited.decision.Verdict != Effective || f.retired(basis) {
			continue
		}
		if cited.record.Actor != implementer || cited.definition == nil || cited.definition.Render != RenderArtifact {
			continue
		}
		body, ok := cited.body.(*State)
		if !ok || body.Body["commit"] != head {
			continue
		}
		if path := body.Body["path"]; path != "" && !seen[path] {
			seen[path] = true
			paths = append(paths, path)
		}
	}
	return paths
}

// artifactPath answers where an event stands, and whether it is an artifact at
// all. A plan entry naming anything else reaches nothing.
func (f *foldState) artifactPath(event string) (string, bool) {
	record := f.byID[event]
	if record == nil || record.definition == nil || record.definition.Render != RenderArtifact {
		return "", false
	}
	state, ok := record.body.(*State)
	if !ok {
		return "", false
	}
	path := state.Body["path"]
	return path, path != ""
}

// sameTreeLineage reports whether two artifact paths name one tree: the same
// string, or one containing the other. The wider direction is deliberate — the
// documented rule is that a directory artifact wins over one inside it, so a
// merge must be able to retire the covering pointer as well as the covered one.
func sameTreeLineage(one, other string) bool {
	return pathCovers(one, other) || pathCovers(other, one)
}

// governanceTarget follows an act back to the roster statement whose live
// force it changes. Superseding a supersession reverses that change, so the
// returned flag alternates along the chain and identifies restoration of a
// dormant grant.
func (f *foldState) governanceTarget(event string) (*parsedRecord, bool, bool) {
	restoring := false
	for event != "" {
		record := f.byID[event]
		if record == nil {
			return nil, false, false
		}
		switch value := record.body.(type) {
		case *State:
			if value.Kind != KindRoster {
				return nil, false, false
			}
			return record, restoring, true
		case *Ratify:
			event = value.Target
		case *Supersede:
			restoring = !restoring
			event = value.Target
		default:
			return nil, false, false
		}
	}
	return nil, false, false
}

type normalizedRosterState struct {
	membership    bool
	kind          string
	authorityRole string
}

// normalizeRosterState is the one compatibility boundary for the legacy
// roster encoding, where role carried either descriptive kind or authority.
// Modern records carry kind independently. All fold interpretation and the
// application precondition vocabulary derive from this result.
func normalizeRosterState(kind, role string) normalizedRosterState {
	normalized := normalizedRosterState{
		membership:    kind == "" || role == "participant",
		kind:          kind,
		authorityRole: role,
	}
	if kind == "" {
		switch role {
		case "agent", "human", "service":
			normalized.kind = role
			normalized.authorityRole = ""
		case "operator":
			normalized.kind = "human"
		default:
			normalized.kind = "unspecified"
		}
	}
	if role == "participant" {
		normalized.authorityRole = ""
	}
	return normalized
}

// IsActorKind reports whether a word is one of the descriptive actor kinds the
// roster vocabulary defines. It answers the one question both callers ask, so
// it is named for the vocabulary rather than for either of them: the fold's
// authority precondition asks it to reject a kind word offered as an authority,
// and the application asks it to accept a kind on a new actor.
//
// It delegates to the compatibility normalizer rather than carrying a list, so
// the vocabulary lives in exactly one place. Both clauses are load-bearing: the
// normalizer derives kind "unspecified" for any word it does not recognise, so
// comparing kind alone would admit "unspecified" as a kind. Requiring an empty
// authority is what separates a word that names a kind from one the normalizer
// merely failed to classify.
func IsActorKind(word string) bool {
	normalized := normalizeRosterState("", word)
	return normalized.kind == word && normalized.authorityRole == ""
}

// membershipCarriesOperator includes dormant grants: restoring their retired
// membership basis would make them live again in the same act.
func (f *foldState) membershipCarriesOperator(actor, statement string) bool {
	if f.hasRole(actor, "operator") {
		return true
	}
	for _, grant := range f.roleGrantsByRole[actorRole{actor: actor, role: "operator"}] {
		if grant.membershipBasis == statement && f.roleGrantDirectActive(grant) {
			return true
		}
	}
	return false
}

func (f *foldState) addRoleGrant(actor, name, kind, role, statement, ratification string, restsOn []string) {
	if actor == "" || role == "" {
		return
	}
	normalized := normalizeRosterState(kind, role)
	grantRole := normalized.authorityRole
	if grantRole == "" {
		grantRole = "participant"
	}
	grant := roleGrant{
		actor: actor, name: name, kind: normalized.kind, role: grantRole,
		statement: statement, ratification: ratification,
		membership: normalized.membership || ratification == "",
	}
	// The only unratified grant is the genesis operator seed, which is
	// necessarily both membership and authority. Other modern authority grants
	// stay bound to their actor's membership statement.
	if !grant.membership && len(restsOn) != 0 {
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
// artifact provenance describes a retired implementation. Ordinary staleness
// crosses every governed reasoning edge. World staleness crosses a direct
// retirement edge from an artifact, and then only artifact-to-artifact edges:
// a request, promise, report, or other reasoning statement records that its
// argument moved without claiming that every later answer describes the old
// world. Records are visited in sequence order and a basis is always cited
// before its dependent, so one pass settles both maps. Which bases can carry
// staleness at all is the governing definition's business: an exempt kind
// neither catches staleness nor passes it on, and a terminal one catches it
// without passing it on.
func (f *foldState) staleness(successors map[string]string) (map[string]bool, map[string]bool) {
	stale := make(map[string]bool)
	world := make(map[string]bool)
	for _, record := range f.records {
		if record.decision.Verdict != Effective {
			continue
		}
		if record.definition != nil && record.definition.Staleness == StalenessExempt {
			continue
		}
		// A merge ignores only the predecessor retirements it signed. Later
		// retirement of its approval, request, or any other basis still
		// propagates normally. The plan is a property of the record, not of the
		// basis being examined, so it is read once.
		plan := f.withoutCondemnedSuccessions(f.mergeSuccessionPlan(&record), successors)
		for _, basis := range record.record.RestsOn {
			if plan != nil {
				if _, intended := plan[basis]; intended && f.retired(basis) {
					continue
				}
			}
			if target, ok := f.effectiveSup[record.record.ID]; ok && target == basis {
				continue
			}
			basisRecord := f.byID[basis]
			mode := StalenessPropagates
			if basisRecord != nil && basisRecord.definition != nil {
				mode = basisRecord.definition.Staleness
			}
			retiredBasis := f.retired(basis) && mode != StalenessExempt
			artifactProvenance := f.isArtifact(basis) && f.isArtifact(record.record.ID)
			// A succeeded retirement moved a pointer; it did not condemn one.
			// The reasoning that stood on the artifact — the request, the
			// promise, the report, the approval — is answered by that move, so
			// it is not news to them and they do not flare. A page above it is
			// a different matter: the behaviour it describes has changed, and
			// artifact provenance is exactly the edge that says so.
			//
			// The move is followed to its end. If the successor, or any later
			// successor in the chain, is itself retired with no successor, the
			// behaviour was condemned after all, and the reasoning that stood
			// on the predecessor is told exactly as if its own basis had been.
			if retiredBasis && !artifactProvenance {
				if _, moved := successors[basis]; moved && !f.successionCondemned(basis, successors) {
					retiredBasis = false
				}
			}
			staleBasis := stale[basis] && mode == StalenessPropagates
			if plan != nil && staleBasis && f.stalenessCoveredByMergePlan(basis, plan, stale, make(map[string]bool)) {
				continue
			}
			if !retiredBasis && !staleBasis {
				continue
			}
			stale[record.record.ID] = true
			if (world[basis] && artifactProvenance) || (retiredBasis && f.isArtifact(basis)) {
				world[record.record.ID] = true
				break
			}
		}
	}
	return stale, world
}

// stalenessCoveredByMergePlan reports whether every live cause below event is
// an artifact retirement explicitly named by this receipt. It is deliberately
// false for a mixed cause, so a later request or approval withdrawal still
// flares the receipt and its successors.
// mergeSuccessionPlan is the retirement set a record may read as its own act
// rather than as news: the plan it signed if it is a receipt, and the plan of a
// receipt whose successor it is.
//
// The second half is what makes ordinary succession possible. Work stands on
// what it replaces — a successor artifact cites the predecessor it grew out of
// — and the merge that publishes the successor is the same act that withdraws
// that predecessor. Reading the withdrawal as staleness marked the successor
// stale at the moment it was born, and `gs merge` then refused a stale
// successor, so the merge refused its own result. The only escape was to
// retire the causal basis first, which is to break the record of where the
// work came from in order to be allowed to record where it went.
//
// Only a genuine successor may borrow the plan, and only its own. Citing a
// receipt is not enough: the signed plan says which retirement is hidden but
// says nothing about who may hide it, so any later record could have cited a
// receipt and one of its planned predecessors and quietly suppressed a
// freshness signal it had no part in. A beneficiary must be an artifact that
// merge actually published — resting on the receipt, standing at its merge
// head, at one of its declared successor paths. Everything else inherits
// staleness from a current successor in the ordinary way.
func (f *foldState) mergeSuccessionPlan(record *parsedRecord) map[string]string {
	own := f.mergeReceiptPlan(record)
	var carried []map[string]string
	for _, basis := range record.record.RestsOn {
		receipt := f.byID[basis]
		plan := f.mergeReceiptPlan(receipt)
		if len(plan) == 0 || !f.publishedByMerge(record, receipt) {
			continue
		}
		carried = append(carried, plan)
	}
	if len(carried) == 0 {
		return own
	}
	combined := make(map[string]string, len(own))
	for target, successor := range own {
		combined[target] = successor
	}
	for _, plan := range carried {
		for target, successor := range plan {
			combined[target] = successor
		}
	}
	return combined
}

// publishedByMerge reports whether a record is one of the artifacts a receipt's
// own merge stood up: at that merge's head, at a path the receipt declared it
// would publish. Both facts come from the receipt, which is signed, and from
// the artifact itself, so neither can be claimed by a bystander.
func (f *foldState) publishedByMerge(record, receipt *parsedRecord) bool {
	if record.definition == nil || record.definition.Render != RenderArtifact {
		return false
	}
	published, ok := record.body.(*State)
	if !ok {
		return false
	}
	signed, ok := receipt.body.(*State)
	if !ok || signed.Body["merge_head"] == "" || published.Body["commit"] != signed.Body["merge_head"] {
		return false
	}
	var successors []string
	if err := json.Unmarshal([]byte(signed.Body["merge_successors"]), &successors); err != nil {
		return false
	}
	for _, path := range successors {
		if path != "" && path == published.Body["path"] {
			return true
		}
	}
	return false
}

func (f *foldState) stalenessCoveredByMergePlan(event string, plan map[string]string, stale map[string]bool, visiting map[string]bool) bool {
	if visiting[event] {
		return true
	}
	visiting[event] = true
	defer delete(visiting, event)
	covered := false
	if f.retired(event) {
		if _, ok := plan[event]; !ok {
			return false
		}
		covered = true
	}
	record := f.byID[event]
	if record == nil {
		return covered
	}
	for _, basis := range record.record.RestsOn {
		mode := StalenessPropagates
		if basisRecord := f.byID[basis]; basisRecord != nil && basisRecord.definition != nil {
			mode = basisRecord.definition.Staleness
		}
		if mode == StalenessExempt || (!f.retired(basis) && !(stale[basis] && mode == StalenessPropagates)) {
			continue
		}
		if !f.stalenessCoveredByMergePlan(basis, plan, stale, visiting) {
			return false
		}
		covered = true
	}
	return covered
}

// succeededRetirements lists the retired artifacts whose retirement says where
// the behaviour went: the supersession that withdrew the pointer also rests on
// an artifact standing at the same path, or at a directory covering it.
//
// Succession is a fact the retiring act states, and only that act can state
// it. The tempting shortcut is structural — treat a retirement as succeeded
// whenever some later live artifact happens to stand at the same path — and it
// is wrong in the one case that matters: an artifact retired because its claim
// was never true would be quietly rescued by the next unrelated publication in
// that tree, and everything resting on the false claim would stop flaring. The
// signed basis cannot be supplied by a bystander.
//
// The map carries the link itself, predecessor to successor, so that staleness
// can follow the chain: a successor retired later with no successor of its own
// condemns everything the chain once answered for.
func (f *foldState) succeededRetirements() map[string]string {
	succeeded := make(map[string]string)
	for _, record := range f.records {
		if record.decision.Verdict != Effective {
			continue
		}
		supersede, isSupersession := record.body.(*Supersede)
		if !isSupersession {
			continue
		}
		retiredPath, targetIsArtifact := f.artifactPath(supersede.Target)
		if _, already := succeeded[supersede.Target]; !targetIsArtifact || !f.isArtifact(supersede.Target) || already {
			continue
		}
		for _, basis := range record.record.RestsOn {
			if basis == supersede.Target || !f.isArtifact(basis) {
				continue
			}
			if successorPath, ok := f.artifactPath(basis); ok && pathCovers(successorPath, retiredPath) {
				succeeded[supersede.Target] = basis
				break
			}
		}
	}
	return succeeded
}

// withoutCondemnedSuccessions narrows a receipt's plan to the retirements it
// may still read as its own act. A planned retirement whose successor chain
// was later condemned is news to the receipt, exactly as it is to the approval
// it rests on, so it is removed and the receipt flares with everything else.
func (f *foldState) withoutCondemnedSuccessions(plan map[string]string, successors map[string]string) map[string]string {
	if plan == nil {
		return nil
	}
	narrowed := make(map[string]string, len(plan))
	for artifact, path := range plan {
		if _, moved := successors[artifact]; moved && f.successionCondemned(artifact, successors) {
			continue
		}
		narrowed[artifact] = path
	}
	return narrowed
}

// successionCondemned follows the successor chain from a succeeded artifact
// and reports whether it ends in a retirement that names no successor. A
// chain that ends in a live artifact, or one not yet retired, still answers
// for the predecessor. A cycle cannot arise from an append-only log, but the
// walk is bounded by the visited set all the same.
func (f *foldState) successionCondemned(artifact string, successors map[string]string) bool {
	visited := map[string]bool{artifact: true}
	current := artifact
	for {
		next, moved := successors[current]
		if !moved {
			return f.retired(current)
		}
		if visited[next] {
			return false
		}
		visited[next] = true
		current = next
	}
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
	succeeded := f.succeededRetirements()
	stale, world := f.staleness(succeeded)
	// Every artifact seen at a path, in order, not only the most recent.
	// Tracking just the immediate predecessor let a retirement hide a live
	// ancestor: with A, B and C at one path, retiring B cleared C's warning
	// while A stayed live.
	seenByPath := make(map[string][]string)
	// Review independence needs the author of the artifact for the head judged,
	// and the commit each artifact stands at, so a verdict cannot be paired
	// with an artifact for some other head. All three indexes are filled by the
	// same pass that projects the artifacts.
	implementers := make(map[string]string)
	artifactCommits := make(map[string]string)
	artifactsByCommit := make(map[string][]string)
	var reviewBases []reviewBasis
	projection := Projection{
		Decisions: []Decision{}, Acts: []Act{}, Statements: []Statement{}, Commitments: []Commitment{},
		Reviews: []Review{}, Artifacts: []Artifact{},
		Actors: make(map[string]ActorState), Provenance: make(map[string][]string),
		OpaqueKinds: make(map[string][]string),
	}
	for _, record := range f.records {
		decision := record.decision
		decision.Sequence = record.sequence()
		projection.Decisions = append(projection.Decisions, decision)
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
			Event: record.record.ID, Sequence: record.sequence(),
			Timestamp: record.record.Timestamp, Actor: record.record.Actor, Kind: state.Kind,
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
				Retired:                  f.retired(record.record.ID),
				Succeeded:                f.retired(record.record.ID) && succeeded[record.record.ID] != "",
				Stale:                    stale[record.record.ID],
				DescribesSupersededWorld: world[record.record.ID],
				UnableToFlare:            f.unableToFlare(record.record.RestsOn),
				SuccessionUnrecorded:     live > 0,
				LivePredecessors:         live,
			})
			seenByPath[path] = append(seenByPath[path], record.record.ID)
			if record.decision.Verdict == Effective {
				implementers[record.record.ID] = record.record.Actor
				if commit := state.Body["commit"]; commit != "" {
					artifactCommits[record.record.ID] = commit
					artifactsByCommit[commit] = append(artifactsByCommit[commit], record.record.ID)
				}
			}
		}
		// A report carrying a verdict is a review. Which artifact it judges is
		// settled after the loop, because a resolution by reviewed commit may
		// depend on an artifact that has not been read yet.
		if state.Kind == KindReport && state.Body["verdict"] != "" && record.decision.Verdict == Effective {
			head := state.Body["head"]
			if head == "" {
				head = state.Body["commit"]
			}
			projection.Reviews = append(projection.Reviews, Review{
				Report: record.record.ID, Timestamp: record.record.Timestamp, Reviewer: record.record.Actor,
				Verdict: state.Body["verdict"], Head: head, Independence: IndependenceUnresolved,
				Ratified: f.ratified(record.record.ID),
				Retired:  f.retired(record.record.ID), Stale: stale[record.record.ID],
			})
			reviewBases = append(reviewBases, reviewBasis{named: state.Body["artifact"], restsOn: record.record.RestsOn})
		}
	}
	resolveReviews(projection.Reviews, reviewBases, implementers, artifactCommits, artifactsByCommit)
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
		if actor.DormantRoleSources == nil {
			actor.DormantRoleSources = make(map[string][]string)
		}
		if actor.RetiredRoleSources == nil {
			actor.RetiredRoleSources = make(map[string][]string)
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
	// A principal whose membership has been retired keeps a roster entry with
	// no roles. Live grants are applied first, so an actor readmitted after a
	// retirement is already present and is left alone; only principals with
	// nothing live left fall through, and the latest retired membership
	// supplies the name they signed under.
	live := make(map[string]bool, len(projection.Actors))
	for fingerprint := range projection.Actors {
		live[fingerprint] = true
	}
	for _, grant := range f.roleGrants {
		if !grant.membership || live[grant.actor] || f.roleGrantActive(grant) {
			continue
		}
		projection.Actors[grant.actor] = ActorState{
			Name: grant.name, Kind: grant.kind, Roles: []string{},
			MembershipEvent: grant.statement, RoleSources: map[string][]string{},
			DormantRoleSources: map[string][]string{}, RetiredRoleSources: map[string][]string{},
			Retired: true,
		}
	}
	for _, grant := range f.roleGrants {
		actor, exists := projection.Actors[grant.actor]
		if !exists || f.roleGrantActive(grant) {
			continue
		}
		if actor.DormantRoleSources == nil {
			actor.DormantRoleSources = make(map[string][]string)
		}
		if actor.RetiredRoleSources == nil {
			actor.RetiredRoleSources = make(map[string][]string)
		}
		if f.roleGrantDirectActive(grant) {
			actor.DormantRoleSources[grant.role] = appendUnique(actor.DormantRoleSources[grant.role], grant.statement)
		} else {
			actor.RetiredRoleSources[grant.role] = appendUnique(actor.RetiredRoleSources[grant.role], grant.statement)
		}
		projection.Actors[grant.actor] = actor
	}
	for fingerprint, actor := range projection.Actors {
		sort.Strings(actor.Roles)
		for role := range actor.RoleSources {
			sort.Strings(actor.RoleSources[role])
		}
		for role := range actor.DormantRoleSources {
			sort.Strings(actor.DormantRoleSources[role])
		}
		for role := range actor.RetiredRoleSources {
			sort.Strings(actor.RetiredRoleSources[role])
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
	mergedArtifacts := f.mergedArtifacts()
	for _, requestRecord := range f.records {
		request, ok := requestRecord.body.(*State)
		if !ok || requestRecord.definition == nil || requestRecord.definition.Lifecycle != LifecycleRequest || requestRecord.decision.Verdict != Effective {
			continue
		}
		promises := f.directDependents(requestRecord.record.ID, LifecyclePromise)
		if len(promises) == 0 {
			status := "open"
			isStale := stale[requestRecord.record.ID]
			if f.retired(requestRecord.record.ID) {
				status = "withdrawn"
			} else if stale[requestRecord.record.ID] {
				status = "stale"
				isStale = true
			}
			commitments = append(commitments, Commitment{Request: requestRecord.record.ID, Requester: requestRecord.record.Actor, AddressedTo: request.Body["to"], Status: status, Stale: isStale})
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
			default:
				completion := f.latestCompletion(promiseRecord, mergedArtifacts)
				if completion != nil {
					entry.Report = completion.record.ID
					entry.Status = "reported"
					entry.WaitingOn = requestRecord.record.Actor
					entry.Stale = stale[requestRecord.record.ID] || stale[promiseRecord.record.ID] || stale[completion.record.ID]
					if completion.definition.Lifecycle == LifecycleReport && f.ratified(completion.record.ID) {
						entry.Status = "satisfied"
						entry.WaitingOn = ""
					} else if receipt := mergedArtifacts[completion.record.ID]; receipt != nil {
						entry.Status = "satisfied"
						entry.WaitingOn = ""
						entry.Stale = entry.Stale || stale[receipt.record.ID]
					}
				}
				if entry.Report == "" && (stale[requestRecord.record.ID] || stale[promiseRecord.record.ID]) {
					entry.Status = "stale"
					entry.Stale = true
				}
			}
			commitments = append(commitments, entry)
		}
	}
	return commitments
}

// latestCompletion returns the promise's live completion record. A sealed
// merge is terminal. Otherwise an explicit report keeps the authority it had
// before artifacts could report implementation work; an artifact is the
// report when the promise has no live explicit report.
// A lifecycle report remains the general form: it can close work that never
// reaches Git, once its requester ratifies it. For implementing work, the
// promisor's artifact already carries the exact head and the promise it
// fulfils, so filing a second report would duplicate the same assertion.
func (f *foldState) latestCompletion(promise *parsedRecord, mergedArtifacts map[string]*parsedRecord) *parsedRecord {
	var report, artifact, merged *parsedRecord
	candidates := append([]*parsedRecord(nil), f.directDependents(promise.record.ID, LifecycleReport)...)
	candidates = append(candidates, f.directDependents(promise.record.ID, LifecycleNone)...)
	for _, record := range candidates {
		state, ok := record.body.(*State)
		if !ok || record.definition == nil {
			continue
		}
		isReport := record.definition.Lifecycle == LifecycleReport
		promiseBases := f.basesOfLifecycle(record.record.RestsOn, LifecyclePromise)
		isArtifactReport := record.definition.Render == RenderArtifact && state.Body["commit"] != "" && record.record.Actor == promise.record.Actor &&
			len(promiseBases) == 1 && promiseBases[0].record.ID == promise.record.ID
		if !isReport && !isArtifactReport {
			continue
		}
		// A normal retirement withdraws a completion claim. The candidate
		// artifact is the one exception: merge-driven succession retires it as
		// it publishes the main-line successor, and that planned retirement
		// must not erase the merge which satisfied the promise.
		if f.retired(record.record.ID) && (!isArtifactReport || mergedArtifacts[record.record.ID] == nil) {
			continue
		}
		if receipt := mergedArtifacts[record.record.ID]; receipt != nil {
			if merged == nil || receipt.index > mergedArtifacts[merged.record.ID].index ||
				(receipt.index == mergedArtifacts[merged.record.ID].index && record.index > merged.index) {
				merged = record
			}
			continue
		}
		if isReport && (report == nil || record.index > report.index) {
			report = record
		}
		if isArtifactReport && (artifact == nil || record.index > artifact.index) {
			artifact = record
		}
	}
	if merged != nil {
		return merged
	}
	if report != nil {
		return report
	}
	return artifact
}

// mergedArtifacts indexes every reporting artifact in a live sealed receipt's
// approved retirement plan. validateMergeReceiptNow sealed the full ratified
// approval chain when the receipt landed; keeping review ratification explicit
// makes that chain the authority for automatic commitment closure. Reading the
// whole reviewed plan matters for a multi-path implementation: the artifact a
// verdict names is only its primary pointer, not the only artifact it signs.
func (f *foldState) mergedArtifacts() map[string]*parsedRecord {
	merged := make(map[string]*parsedRecord)
	for index := range f.records {
		receipt := &f.records[index]
		if receipt.mergePlan == nil || f.retired(receipt.record.ID) {
			continue
		}
		state, ok := receipt.body.(*State)
		if !ok {
			continue
		}
		approval := f.byID[state.Body["merge_approval"]]
		if approval == nil {
			continue
		}
		if _, ok := approval.body.(*State); !ok {
			continue
		}
		candidate := state.Body["merge_candidate"]
		for _, artifactID := range approval.record.RestsOn {
			if _, planned := receipt.mergePlan[artifactID]; !planned {
				continue
			}
			artifact := f.byID[artifactID]
			if artifact == nil || artifact.record.Actor != receipt.record.Actor || artifact.definition == nil || artifact.definition.Render != RenderArtifact {
				continue
			}
			implementation, ok := artifact.body.(*State)
			if ok && implementation.Body["commit"] == candidate {
				merged[artifactID] = receipt
			}
		}
	}
	return merged
}

// reviewBasis carries what a report said about the artifact it judges, kept
// beside the projected review until every artifact has been read.
type reviewBasis struct {
	named   string
	restsOn []string
}

// resolveReviews pairs each review with the artifact statement for the head it
// judges, and so with the fingerprint that implemented it. A review names its
// artifact when it was written through the exact-head guard; older and
// hand-written verdicts often do not, so two further resolutions follow, each
// recorded in ResolvedBy rather than presented as the same kind of fact:
// exactly one artifact among the report's direct bases, or — failing that —
// artifacts at the reviewed commit that all share one author, which is the
// ordinary shape of one implementer filing several path artifacts at one head.
// Anything less certain stays unresolved. Nothing here judges the report; an
// unratified or self-signed review is still an effective statement, and this
// projection only lets a reader see which it is.
func resolveReviews(reviews []Review, bases []reviewBasis, implementers, artifactCommits map[string]string, artifactsByCommit map[string][]string) {
	for index := range reviews {
		artifact, resolvedBy := resolveReviewed(bases[index], reviews[index].Head, implementers, artifactCommits, artifactsByCommit)
		if artifact == "" {
			continue
		}
		reviews[index].Artifact = artifact
		reviews[index].ResolvedBy = resolvedBy
		reviews[index].Implementer = implementers[artifact]
		reviews[index].Independence = IndependenceIndependent
		if implementers[artifact] == reviews[index].Reviewer {
			reviews[index].Independence = IndependenceSelfReview
		}
	}
}

// resolveReviewed answers which artifact a verdict judges. Two conditions hold
// for every answer it gives. The report must rest on the artifact, because a
// name in the body is a label the reviewer typed and only the citation makes it
// a link the log can follow. And the artifact must stand at the head the
// verdict claims, because a review is of a head, not of a name. Trusting the
// label on its own let an effective verdict claim one head, name an artifact
// for a different one, and still project as an independent review — the record
// asserting a fact about work nobody reviewed.
func resolveReviewed(basis reviewBasis, head string, implementers, artifactCommits map[string]string, artifactsByCommit map[string][]string) (string, string) {
	// A verdict with no head states none, so there is nothing to match against
	// and the citation carries the whole weight.
	judgesTheClaimedHead := func(artifact string) bool {
		if _, known := implementers[artifact]; !known {
			return false
		}
		return head == "" || artifactCommits[artifact] == head
	}
	if judgesTheClaimedHead(basis.named) && contains(basis.restsOn, basis.named) {
		return basis.named, "named"
	}
	var cited []string
	for _, reference := range basis.restsOn {
		if _, known := implementers[reference]; known {
			cited = appendUnique(cited, reference)
		}
	}
	if len(cited) == 1 && judgesTheClaimedHead(cited[0]) {
		return cited[0], "basis"
	}
	if len(cited) != 0 || head == "" {
		return "", ""
	}
	candidates := artifactsByCommit[head]
	if len(candidates) == 0 {
		return "", ""
	}
	for _, candidate := range candidates {
		if implementers[candidate] != implementers[candidates[0]] {
			return "", ""
		}
	}
	return candidates[0], "head"
}

// Review returns the projected review for a report event.
func (p Projection) Review(report string) (Review, bool) {
	for _, review := range p.Reviews {
		if review.Report == report {
			return review, true
		}
	}
	return Review{}, false
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
