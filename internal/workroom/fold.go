package workroom

import (
	"encoding/json"
	"fmt"
	"math"
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
	Sequence  int    `json:"sequence"`
	Timestamp int64  `json:"timestamp,omitempty"`
	Actor     string `json:"actor"`
	Kind      Kind   `json:"kind"`
	// Lifecycle is the commitment role this record was decided under: the
	// definition in force at its own position, not whichever definition of
	// that kind stands now. A reader classifying a historical record by the
	// current vocabulary gets a different answer than the fold did, and the
	// two then disagree about what a record is — which at a write boundary
	// means signing something the fold will refuse.
	Lifecycle Lifecycle `json:"lifecycle,omitempty"`
	// Satisfier is who may ratify this statement, read from the same captured
	// definition Lifecycle is read from: the one in force at this record's own
	// position. decideRatify judges against exactly that, so a reader who
	// instead looks the kind up in the current vocabulary gets a different
	// answer than the fold will give whenever a kind's satisfier has been
	// revised since — narrower now, and the reader hides an act the fold would
	// accept; wider now, and the reader offers an act the fold refuses, which
	// in an append-only log is a permanent row saying somebody tried something
	// they were never allowed to do.
	//
	// It is projected for the same reason RatifiedBy is: the rule is not
	// recoverable from what a reader is given. The captured definition is not
	// in the projection at all, and reconstructing it would mean replaying
	// every kind definition and its ratifications in reading order — the
	// fold's authority rule, rebuilt in a layer that has no business holding
	// one.
	//
	// Empty means the fold bound no definition to this record, which is what
	// an undefined kind gets. Nothing may be ratified on an empty satisfier.
	Satisfier string            `json:"satisfier,omitempty"`
	Text      string            `json:"text"`
	Body      map[string]string `json:"body,omitempty"`
	Ratified  bool              `json:"ratified,omitempty"`
	// RatifiedBy names the act that ratifies this statement now: the latest
	// ratification of it that is neither retired nor ineffective. Ratified is
	// this field being non-empty, and both are read from one call, so they
	// cannot disagree.
	//
	// It is projected rather than left to be worked out by a reader because
	// the rule is not recoverable from what a reader is given. Acts carry no
	// retirement, so picking the first effective ratification of a target
	// answers a retired one, and picking the last answers a retired one just
	// as wrongly when the newest ratification is the withdrawn one. The only
	// way to tell those apart outside the fold is to rebuild retirement from
	// the supersede acts, restore included — a second implementation of
	// authority, in a layer that has no business holding one.
	RatifiedBy string `json:"ratified_by,omitempty"`
	Retired    bool   `json:"retired,omitempty"`
	Stale      bool   `json:"stale,omitempty"`
	// DescribesSupersededWorld narrows Stale: the retired ancestor that made
	// this statement stale is itself an artifact, so what moved is the world
	// the statement describes rather than the argument it stands on. Both are
	// staleness; only this one means go and re-read the code.
	DescribesSupersededWorld bool `json:"describes_superseded_world,omitempty"`
	// WorldSupersededAt carries the same dating as on Artifact.
	WorldSupersededAt int `json:"world_superseded_at,omitempty"`
	// MergeLeftLive is the fold's receipt-time verification of prospective
	// merge_left_live testimony. It lives on the receipt statement even when a
	// malformed or dangling entry cannot be associated with an artifact path.
	MergeLeftLive []LeftLiveAccounting `json:"merge_left_live,omitempty"`
	// StaleBecause names the nearest retired basis that actually caused this
	// row's staleness under the governed propagation rules. StaleBecausePath is
	// present when that cause is an artifact. Truncated says the bounded cause
	// walk ended before it could name a retired basis.
	StaleBecause          string `json:"stale_because,omitempty"`
	StaleBecausePath      string `json:"stale_because_path,omitempty"`
	StaleBecauseTruncated bool   `json:"stale_because_truncated,omitempty"`
}

type Commitment struct {
	Request          string `json:"request"`
	Requester        string `json:"requester"`
	AddressedTo      string `json:"addressed_to,omitempty"`
	Performer        string `json:"performer,omitempty"`
	Promise          string `json:"promise,omitempty"`
	Report           string `json:"report,omitempty"`
	Status           string `json:"status"`
	SuccessorRequest string `json:"successor_request,omitempty"`
	Stale            bool   `json:"stale,omitempty"`
	WaitingOn        string `json:"waiting_on,omitempty"`
	// The landing obligation. TargetRepo and TargetRef name where this
	// commitment owes its result; Legacy says the fold read that from the
	// commitment's own history rather than from a stated choice. Empty means
	// the request owes no Git artifact, which is a different fact from owing
	// one to somewhere nobody wrote down.
	TargetRepo string `json:"target_repo,omitempty"`
	TargetRef  string `json:"target_ref,omitempty"`
	Legacy     bool   `json:"legacy,omitempty"`
	// HoldOwner is the one actor who may release a held landing, and Release
	// names the authorization that did. Approval and Candidate name the
	// ratified approval and the exact head it approved, whatever record
	// happens to be the completion.
	HoldOwner string `json:"hold_owner,omitempty"`
	Release   string `json:"release,omitempty"`
	Approval  string `json:"approval,omitempty"`
	Candidate string `json:"candidate,omitempty"`
	// LatestResolution is nonterminal evidence beside the commitment: a report
	// that says where the work stands without closing it. It never changes
	// Status or WaitingOn, which is the whole reason it can be admitted.
	LatestResolution string `json:"latest_resolution,omitempty"`
	// Terminal says how a closed commitment closed: landed, reported, or
	// abandoned. Empty while it is still open.
	Terminal string `json:"terminal,omitempty"`
	// ApprovedNotLanded is the audit fact, measured against the target rather
	// than against main: an approval names a head and no sealed receipt put
	// that head where the request said it owed it.
	ApprovedNotLanded bool `json:"approved_not_landed,omitempty"`
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
	// WorldSupersededAt dates DescribesSupersededWorld: the log position of the
	// earliest retirement still accounting for it, taken across every basis
	// rather than the first one that carries the flag, so the order a signer
	// wrote its citations in cannot decide the date. Zero means the fold found
	// no active cause, which for a record that describes a superseded world is
	// a fact to fail closed on rather than read as permission.
	WorldSupersededAt int `json:"world_superseded_at,omitempty"`
	// StaleBecause, StaleBecausePath and StaleBecauseTruncated carry the same
	// bounded causal answer as on Statement.
	StaleBecause          string `json:"stale_because,omitempty"`
	StaleBecausePath      string `json:"stale_because_path,omitempty"`
	StaleBecauseTruncated bool   `json:"stale_because_truncated,omitempty"`
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
	// MergeLeftLive records the predecessors a merge receipt deliberately left
	// standing at this path. Verification is sealed when the receipt lands, so
	// later settlement or retirement cannot rewrite what the merge accounted
	// for. Unverified testimony remains visible but accounts for nothing.
	MergeLeftLive []LeftLiveAccounting `json:"merge_left_live,omitempty"`
}

// LeftLiveAccounting is one merge receipt's prospective testimony about an
// artifact it did not retire. Class is carried, sibling, or abandoned. A
// carried artifact is a wider current pointer in the target world; a sibling
// names the unsettled commitment which protected an outside-world candidate;
// abandoned asserts that no such candidate protection existed. Reason is
// present only when the fold could not verify the testimony.
type LeftLiveAccounting struct {
	Artifact   string `json:"artifact"`
	Class      string `json:"class"`
	Commitment string `json:"commitment,omitempty"`
	Verified   bool   `json:"verified"`
	Reason     string `json:"reason,omitempty"`
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
	// A receipt becomes prospective only when both fields are present and the
	// changed-path list is canonical. Truly legacy receipts carry neither and
	// retain the old fold-time succession projection exactly. Present but
	// invalid metadata stays visible as unverified testimony and gains no
	// accounting effect.
	mergeAccountingPresent   bool
	mergeLeftLivePresent     bool
	mergeChangedPathsPresent bool
	mergeChangedPathsValid   bool
	mergeChangedPaths        []string
	mergeLeftLive            []leftLiveAccounting
	mergeUnaccounted         map[string]int
	// unclaimedExpectation is retained separately from body after the two
	// guarded schemas are lowered to the ordinary request and supersede shapes
	// used by the rest of the fold. That keeps one projection path while making
	// the signed precondition available to the position-aware decision.
	unclaimedExpectation *UnclaimedExpectation
	guardedRetirement    bool
	guardedReplacement   bool
	// linkedSuccessorRequest seals the successor-transfer facts that stood when
	// this supersession landed. Later retirement or failure of the child must
	// change the child's row, not rewrite the old commitment's historical
	// transfer.
	linkedSuccessorRequest string
	// result is the section-1 choice a state@3 request stated, decided at its
	// own position. Nil for every request admitted under an older schema,
	// which is what keeps the same body names powerless there.
	result *requestResult
	// supersedeBody carries a supersede@1 body past the lowering to Supersede.
	// carriedSuccessorRequest and abandonedSuccession seal how a supersession
	// disposed of an approved head, for the same reason the transfer above is
	// sealed. They are kept apart from linkedSuccessorRequest because the
	// transfer-staleness exception is keyed to that relation and to no other.
	supersedeBody           map[string]string
	carriedSuccessorRequest string
	abandonedSuccession     bool
}

type leftLiveClaim struct {
	Class      string `json:"class"`
	Commitment string `json:"commitment,omitempty"`
}

type leftLiveAccounting struct {
	LeftLiveAccounting
	path      string
	successor string
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
	records      []parsedRecord
	byID         map[string]*parsedRecord
	decisions    map[string]Decision
	strings      map[string]string
	effectiveSup map[string]string
	// supersessions holds the effective supersession records in sequence
	// order. effectiveSup already indexes them by id, but map order is not
	// log order, and succession gives the earliest qualifying supersession
	// the say — so readers that need "first wins" walk this slice instead of
	// rescanning every record.
	supersessions    []*parsedRecord
	retirementCauses map[string]int
	roleGrants       []roleGrant
	roleGrantsByRole map[actorRole][]roleGrant
	membershipGrants map[actorStatement][]roleGrant
	ratifications    map[string][]string
	// The claim each report was admitted against, decided once when the record
	// was folded. Recomputing it later reverses an immutable decision as the
	// world moves: withdrawing a promise turned its own report into a second
	// direct completion, and adding a promise made an already-admitted direct
	// completion vanish. A record's shape is a fact about when it landed.
	admittedClaims map[string]reportClaim
	// authorizations indexes every effective statement carrying an
	// authorizes_request by the request it names, so resolving the release in
	// force for a held landing costs one lookup rather than a log scan per
	// commitment row.
	authorizations map[string][]*parsedRecord
	// abandonments indexes every supersession that declared a request's
	// approved head abandoned, by that request. Whether the declaration still
	// stands is read at projection time; finding it is not.
	abandonments       map[string][]*parsedRecord
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
		admittedClaims:     make(map[string]reportClaim),
		authorizations:     make(map[string][]*parsedRecord),
		abandonments:       make(map[string][]*parsedRecord),
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
	parsed := &parsedRecord{record: record, index: index}
	switch value := body.(type) {
	case *RetireIfUnclaimed:
		expectation := value.Expectation
		parsed.unclaimedExpectation = &expectation
		parsed.guardedRetirement = true
		body = &Supersede{Target: value.Target, Text: value.Text}
	case *ReassignIfUnclaimed:
		expectation := value.Expectation
		parsed.unclaimedExpectation = &expectation
		parsed.guardedReplacement = true
		body = &State{Kind: KindRequest, Text: value.Text, Body: value.Body}
	case *SupersedeV1:
		parsed.supersedeBody = value.Body
		body = &Supersede{Target: value.Target, Text: value.Text}
	}
	f.internBody(body)
	if len(parsed.supersedeBody) != 0 {
		pooled := make(map[string]string, len(parsed.supersedeBody))
		for key, field := range parsed.supersedeBody {
			pooled[f.intern(key)] = f.intern(field)
		}
		parsed.supersedeBody = pooled
	}
	if parsed.unclaimedExpectation != nil {
		parsed.unclaimedExpectation.Request = f.intern(parsed.unclaimedExpectation.Request)
		parsed.unclaimedExpectation.Retirement = f.intern(parsed.unclaimedExpectation.Retirement)
		parsed.unclaimedExpectation.Promise = f.intern(parsed.unclaimedExpectation.Promise)
		parsed.unclaimedExpectation.Completion = f.intern(parsed.unclaimedExpectation.Completion)
	}
	parsed.body = body
	if len(f.transitions) != 0 {
		f.beyondSeam = true
		decision.Verdict = Uninterpretable
		decision.Reason = "uninterpretable: activated interpreter execution is not held"
		f.addDecision(record, parsed, index, decision)
		return
	}
	switch value := body.(type) {
	case *State:
		if parsed.guardedReplacement {
			decision = f.decideReassignIfUnclaimed(parsed, *value)
		} else {
			decision = f.decideState(parsed, *value)
		}
	case *Ratify:
		decision = f.decideRatify(parsed, *value)
	case *Supersede:
		if parsed.guardedRetirement {
			decision = f.decideRetireIfUnclaimed(parsed, *value)
		} else {
			decision = f.decideSupersede(parsed, *value)
		}
	}
	if decision.Verdict == Effective {
		if supersede, ok := body.(*Supersede); ok {
			parsed.linkedSuccessorRequest = f.qualifyingRequestSuccessor(parsed, *supersede)
			// Only a request that stated a landing can have its approved head
			// carried or abandoned. Reading the disposition on anything else
			// would give an old supersession a successor it never declared.
			if target := f.byID[supersede.Target]; target != nil && target.result != nil && target.result.landing {
				parsed.carriedSuccessorRequest, parsed.abandonedSuccession =
					f.carriedOrAbandoned(parsed, *supersede, f.approvedHeldHead(target))
				if parsed.abandonedSuccession {
					f.abandonments[supersede.Target] = append(f.abandonments[supersede.Target], parsed)
				}
			}
		}
	}
	f.addDecision(record, parsed, index, decision)
	if decision.Verdict != Effective {
		return
	}
	if state, ok := body.(*State); ok && state.Kind == KindAssert {
		parsed.mergePlan = f.validateMergeReceiptNow(parsed)
		stored := &f.records[len(f.records)-1]
		stored.mergePlan = parsed.mergePlan
		// A valid retirement plan remains the sole source of merge authority.
		// Left-live testimony is parsed only after that authority is sealed and
		// can neither widen nor invalidate it.
		if parsed.mergePlan != nil {
			leftRaw, leftPresent := state.Body["merge_left_live"]
			changedRaw, changedPresent := state.Body["merge_changed_paths"]
			parsed.mergeAccountingPresent = leftPresent || changedPresent
			parsed.mergeLeftLivePresent = leftPresent
			parsed.mergeChangedPathsPresent = changedPresent
			if parsed.mergeAccountingPresent {
				parsed.mergeChangedPaths, parsed.mergeChangedPathsValid = parseMergeChangedPaths(changedRaw, changedPresent)
				parsed.mergeLeftLive, parsed.mergeUnaccounted = f.validateMergeLeftLiveNow(parsed, leftRaw, leftPresent)
			}
		}
		stored.mergeAccountingPresent = parsed.mergeAccountingPresent
		stored.mergeLeftLivePresent = parsed.mergeLeftLivePresent
		stored.mergeChangedPathsPresent = parsed.mergeChangedPathsPresent
		stored.mergeChangedPathsValid = parsed.mergeChangedPathsValid
		stored.mergeChangedPaths = parsed.mergeChangedPaths
		stored.mergeLeftLive = parsed.mergeLeftLive
		stored.mergeUnaccounted = parsed.mergeUnaccounted
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
	if state, ok := body.(*State); ok {
		if request := state.Body["authorizes_request"]; request != "" {
			f.authorizations[request] = append(f.authorizations[request], parsed)
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
		f.supersessions = append(f.supersessions, parsed)
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
	// Live membership is checked before kind or lifecycle semantics, so the
	// boundary is one rule rather than a condition repeated per kind — a kind
	// added later inherits it without anyone remembering to. The genesis seed
	// is the single exception: it is the record that creates the first
	// participant, so it cannot be judged against a roster that does not yet
	// exist.
	//
	// What this closes is the gap between holding a key and holding authority.
	// Removal from the roster was previously advisory against anyone who kept
	// a key or another clone: the fold read the signature, found it valid, and
	// admitted the record. Custody of a key is evidence of who signed; it is
	// not a standing permission to keep speaking.
	if record.index > 0 && !f.hasActor(record.record.Actor) {
		return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: departedAuthorReason}
	}
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
	// Every state@3 request states what it owes. The choice is decided here,
	// once, at the request's own position: what a request owes cannot change
	// because the log moved under it.
	if definition.Lifecycle == LifecycleRequest && record.record.Schema == SchemaStateV3 {
		result, reason := f.decideRequestResult(record, state)
		if reason != "" {
			return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: reason}
		}
		record.result = result
	}
	if reason := f.releaseAuthorshipRefusal(record, state); reason != "" {
		return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: reason}
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
		claim, refusal := f.reportClaim(record)
		if refusal != nil {
			return *refusal
		}
		if reason := f.landingReportRefusal(claim, state); reason != "" {
			return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: reason}
		}
		f.admittedClaims[record.record.ID] = claim
	}
	// An artifact carrying a commit acts as the implementation report, so it
	// needs the same fact recorded about it: which claim it completed, decided
	// when it landed. It is not admitted or refused on that basis — an artifact
	// answering nothing is still a perfectly good artifact — so this only
	// remembers, and only when the shape is unambiguous.
	if definition.Render == RenderArtifact && state.Body["commit"] != "" {
		if claim, refusal := f.reportClaim(record); refusal == nil && claim.claim.record.Actor == record.record.Actor {
			f.admittedClaims[record.record.ID] = claim
		} else if claim.direct && claim.request.body.(*State).Body["to"] == record.record.Actor {
			f.admittedClaims[record.record.ID] = claim
		}
	}
	if state.Kind == KindRoster && state.Body["role"] == "participant" && state.Body["kind"] == "" {
		return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: "participant roster state requires body.kind"}
	}
	return decision
}

// reportClaim decides which commitment a report answers, once. Admission,
// ratifiability and the board all read this answer rather than counting bases
// for themselves, because three places counting independently is how they came
// to disagree: one accepted a direct report after a withdrawn claim while
// another still showed the withdrawal and dropped the report.
//
// There are two shapes. The promised shape rests on the promise that claimed
// the work; a request cited beside it is provenance, which is what gs review
// has always written, and it must be that promise's own request or it is false
// provenance. The direct shape rests on the request itself, for an addressee
// who did the work without claiming it first.
type reportClaim struct {
	claim   *parsedRecord
	request *parsedRecord
	direct  bool
}

func (f *foldState) reportClaim(record *parsedRecord) (reportClaim, *Decision) {
	refuse := func(verdict Verdict, reason string) (reportClaim, *Decision) {
		return reportClaim{}, &Decision{Event: record.record.ID, Verdict: verdict, Reason: reason}
	}
	promises := f.basesOfLifecycle(record.record.RestsOn, LifecyclePromise)
	requests := f.basesOfLifecycle(record.record.RestsOn, LifecycleRequest)
	switch {
	case len(promises) > 1:
		return refuse(Disputed, "report rests on multiple promises")
	case len(promises) == 1:
		promise := promises[0]
		if promise.record.Actor != record.record.Actor {
			return refuse(Ineffective, "only the promisor may report completion")
		}
		governing := f.basesOfLifecycle(promise.record.RestsOn, LifecycleRequest)
		if len(governing) != 1 {
			return refuse(Ineffective, "report rests on a promise with no unique request")
		}
		// A request beside the promise is provenance, so it has to be the
		// provenance it claims: any other request would attach this report to a
		// commitment it never answered and carry that one's staleness with it.
		for _, cited := range requests {
			if cited.record.ID != governing[0].record.ID {
				return refuse(Ineffective, "report cites a request other than the one its promise answers")
			}
		}
		return reportClaim{claim: promise, request: governing[0]}, nil
	case len(requests) > 1:
		return refuse(Disputed, "report rests on multiple requests")
	case len(requests) == 1:
		request := requests[0]
		state := request.body.(*State)
		if state.Body["to"] != record.record.Actor {
			return refuse(Ineffective, "only the requested performer may report directly on a request")
		}
		// One commitment, one closure. A live promise already claimed this
		// request, so the report belongs on it: two answers to one request
		// would leave the promise open forever with nothing able to close it.
		// The reason names the promise so the reporter can refile against it.
		for _, promise := range f.liveClaims(request.record.ID) {
			if promise.record.Actor != record.record.Actor {
				continue
			}
			return refuse(Ineffective, fmt.Sprintf("report rests on the request while promise %s is live; report on the promise", promise.record.ID))
		}
		return reportClaim{claim: request, request: request, direct: true}, nil
	default:
		return refuse(Ineffective, "report lifecycle basis count is 0, want exactly one promise or request")
	}
}

// liveClaims are the promises on a request that still stand. A withdrawn claim
// is history, not a commitment: it neither blocks a direct report nor owns the
// completion that follows one.
func (f *foldState) liveClaims(request string) []*parsedRecord {
	var live []*parsedRecord
	for _, promise := range f.directDependents(request, LifecyclePromise) {
		if f.retired(promise.record.ID) || f.decisions[promise.record.ID].Verdict != Effective {
			continue
		}
		live = append(live, promise)
	}
	return live
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
				reason = "report has no promise or request"
			} else {
				reason = "report rests on more than one promise or request"
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
		// Being the originating requester is not enough on its own. A departed
		// requester conferring force is the case that reaches furthest: a
		// ratified review approval is what gs merge consumes, so a removed
		// actor able to ratify could still put a head into main long after it
		// stopped being a participant.
		if !f.hasActor(record.record.Actor) {
			return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: departedRequesterReason}
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
	if reason := f.carriedOrAbandonedRefusal(record, supersede); reason != "" {
		return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: reason}
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
	// Past the own-authored branch above, every remaining path is authority
	// over somebody else's record, and a departed actor holds none of it. The
	// ratifier branch already refuses one, because a role grant is active only
	// while the membership it rests on is. This one tested nothing, so a
	// retained key kept cross-author reach after removal — the boundary this
	// whole guard exists to close, left open while the documentation said it
	// was shut.
	if f.isArtifact(supersede.Target) && f.hasAuthorizedMergeReceipt(record, supersede.Target) {
		if !f.hasActor(record.record.Actor) {
			return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: departedReceiptReason}
		}
		return Decision{Event: record.record.ID, Verdict: Effective, Reason: "merge approval authorized artifact succession"}
	}
	return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: "actor may not supersede target"}
}

// qualifyingRequestSuccessor recognizes the one supersession shape that
// transfers a rejected implementation round to a repair child. It records the
// answer on the supersession at admission time: later retirement or failure of
// the child belongs to the child's commitment and must not rewrite this
// historical transfer.
//
// The supersession itself keeps the ordinary authority rule above. Failure to
// qualify here therefore preserves the existing withdrawn or cancelled
// projection rather than turning a malformed successor claim into new force.
func (f *foldState) qualifyingRequestSuccessor(record *parsedRecord, supersede Supersede) string {
	target := f.byID[supersede.Target]
	if target == nil || target.decision.Verdict != Effective || lifecycleOf(target) != LifecycleRequest || f.retired(target.record.ID) {
		return ""
	}
	if countExact(record.record.RestsOn, target.record.ID) != 1 || len(record.record.RestsOn) < 2 || record.record.RestsOn[0] != target.record.ID {
		return ""
	}

	var successor *parsedRecord
	for _, basis := range record.record.RestsOn[1:] {
		candidate := f.byID[basis]
		if candidate == nil || candidate.decision.Verdict != Effective || lifecycleOf(candidate) != LifecycleRequest {
			continue
		}
		if successor != nil {
			return ""
		}
		successor = candidate
	}
	if successor == nil || successor.index <= target.index || successor.record.Actor != target.record.Actor ||
		countExact(successor.record.RestsOn, target.record.ID) == 0 {
		return ""
	}
	if !f.requestHasRatifiedChangesRequestedArtifact(target) {
		return ""
	}
	return successor.record.ID
}

// requestHasRatifiedChangesRequestedArtifact proves the rejected-round side of
// a linked transfer without scanning unrelated history. A reporting artifact
// is indexed under its admitted request or promise, and a verdict that names it
// is indexed under the artifact. The explicit artifact field and exact head
// match are both required: a verdict about another artifact or another commit
// cannot move this commitment.
func (f *foldState) requestHasRatifiedChangesRequestedArtifact(request *parsedRecord) bool {
	claims := []*parsedRecord{request}
	claims = append(claims, f.directDependents(request.record.ID, LifecyclePromise)...)
	for _, claim := range claims {
		for _, artifact := range f.directDependents(claim.record.ID, LifecycleNone) {
			state, ok := artifact.body.(*State)
			admitted, reported := f.admittedClaims[artifact.record.ID]
			if !ok || artifact.definition == nil || artifact.definition.Render != RenderArtifact ||
				state.Body["commit"] == "" || !reported || admitted.request.record.ID != request.record.ID ||
				f.retired(artifact.record.ID) {
				continue
			}
			for _, verdict := range f.directDependents(artifact.record.ID, LifecycleReport) {
				report, ok := verdict.body.(*State)
				if !ok || report.Kind != KindReport || report.Body["verdict"] != "changes-requested" ||
					report.Body["artifact"] != artifact.record.ID || f.retired(verdict.record.ID) || !f.ratified(verdict.record.ID) {
					continue
				}
				head := report.Body["head"]
				if head == "" {
					head = report.Body["commit"]
				}
				if head == state.Body["commit"] {
					return true
				}
			}
		}
	}
	return false
}

// linkedRequestSuccessor returns the first surviving linked transfer for a
// request. Retiring the supersession restores the ordinary commitment state;
// retiring the child does not, because the transfer itself still happened.
func (f *foldState) linkedRequestSuccessor(request string) string {
	for _, supersession := range f.supersessions {
		value, ok := supersession.body.(*Supersede)
		if !ok || value.Target != request || f.retired(supersession.record.ID) {
			continue
		}
		// The rejected-round transfer and the carried approved head are two
		// different relations that both leave a successor request. The board
		// shows either as superseded; only the first exempts its own edge from
		// staleness, so they are read from separate fields.
		if supersession.linkedSuccessorRequest != "" {
			return supersession.linkedSuccessorRequest
		}
		if supersession.carriedSuccessorRequest != "" {
			return supersession.carriedSuccessorRequest
		}
	}
	return ""
}

func (f *foldState) decideRetireIfUnclaimed(record *parsedRecord, supersede Supersede) Decision {
	if reason := f.unclaimedExpectationReason(record, false); reason != "" {
		return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: reason}
	}
	return f.decideSupersede(record, supersede)
}

func (f *foldState) decideReassignIfUnclaimed(record *parsedRecord, state State) Decision {
	if reason := f.unclaimedExpectationReason(record, true); reason != "" {
		return Decision{Event: record.record.ID, Verdict: Ineffective, Reason: reason}
	}
	return f.decideState(record, state)
}

// unclaimedExpectationReason evaluates the signed, request-local compare and
// swap. It intentionally does not read Commitment.Status: a retired request
// projects as withdrawn even when a late direct completion exists. The fold's
// admitted dependency and completion facts are the authority.
func (f *foldState) unclaimedExpectationReason(record *parsedRecord, replacement bool) string {
	expectation := record.unclaimedExpectation
	if expectation == nil {
		return "reassign-if-unclaimed expectation is missing"
	}
	request := f.byID[expectation.Request]
	if request == nil || request.decision.Verdict != Effective || lifecycleOf(request) != LifecycleRequest {
		return "reassign-if-unclaimed request is not one effective request-lifecycle statement"
	}
	state, ok := request.body.(*State)
	if !ok || state.Kind == "" {
		return "reassign-if-unclaimed request shape is ambiguous"
	}
	if expectation.Promise != CommitmentAbsent || expectation.Completion != CommitmentAbsent {
		return "reassign-if-unclaimed expectation must explicitly require absent promise and completion"
	}
	// Staleness is not a precondition here. The guard protects one statement —
	// nobody has claimed or completed this request — and a basis moving under
	// the request leaves that statement exactly as true as it was. A stale
	// unclaimed request is precisely the one most likely to need a new owner,
	// and refusing it left it with no way to be reassigned at all.
	if promises := f.directDependents(expectation.Request, LifecyclePromise); len(promises) != 0 {
		return fmt.Sprintf("reassign-if-unclaimed request has %d admitted promise(s); re-read before changing its assignment", len(promises))
	}
	completions := 0
	for _, claim := range f.admittedClaims {
		if claim.direct && claim.request != nil && claim.request.record.ID == expectation.Request {
			completions++
		}
	}
	if completions != 0 {
		return fmt.Sprintf("reassign-if-unclaimed request has %d admitted direct completion(s); re-read before changing its assignment", completions)
	}
	if !replacement {
		if expectation.Retirement != "" {
			return "guarded retirement does not name exactly its expected request"
		}
		supersede, ok := record.body.(*Supersede)
		if !ok || supersede.Target != expectation.Request {
			return "guarded retirement does not name exactly its expected request"
		}
		if f.retired(expectation.Request) {
			return "reassign-if-unclaimed request is not live"
		}
		if countExact(record.record.RestsOn, expectation.Request) != 1 || len(record.record.RestsOn) == 0 || record.record.RestsOn[0] != expectation.Request {
			return "guarded retirement must rest first and exactly once on its expected request"
		}
		return ""
	}
	if expectation.Retirement == "" {
		return "guarded replacement does not name a retirement"
	}
	retirement := f.byID[expectation.Retirement]
	if retirement == nil || retirement.decision.Verdict != Effective || !retirement.guardedRetirement {
		return "guarded replacement retirement is not one effective guarded retirement"
	}
	retired, ok := retirement.body.(*Supersede)
	if !ok || retired.Target != expectation.Request || retirement.unclaimedExpectation == nil || retirement.unclaimedExpectation.Request != expectation.Request {
		return "guarded replacement retirement does not retire its expected request"
	}
	if retirement.record.Actor != record.record.Actor {
		return "guarded replacement must be signed by the guarded retirement author"
	}
	if f.retired(expectation.Retirement) || !f.retired(expectation.Request) || f.retirementCauses[expectation.Request] != 1 {
		return "guarded replacement retirement is no longer the one live retirement of its request"
	}
	if countExact(record.record.RestsOn, expectation.Retirement) != 1 || len(record.record.RestsOn) == 0 || record.record.RestsOn[0] != expectation.Retirement {
		return "guarded replacement must rest first and exactly once on its named retirement"
	}
	return ""
}

func countExact(values []string, wanted string) int {
	count := 0
	for _, value := range values {
		if value == wanted {
			count++
		}
	}
	return count
}

// AdmitRetireIfUnclaimed applies the same guard as the authoritative fold to
// the exact pre-sequence prefix held by application admission. It evaluates
// only the precondition; ordinary supersession authority remains the fold's
// decision, as it is for SchemaSupersede.
func (f *Folder) AdmitRetireIfUnclaimed(actor string, payload RetireIfUnclaimed, restsOn []string) error {
	expectation := payload.Expectation
	record := &parsedRecord{
		record:               Record{Actor: actor, RestsOn: append([]string(nil), restsOn...)},
		body:                 &Supersede{Target: payload.Target, Text: payload.Text},
		unclaimedExpectation: &expectation,
		guardedRetirement:    true,
	}
	if reason := f.state.unclaimedExpectationReason(record, false); reason != "" {
		return fmt.Errorf("reassign-if-unclaimed refused: %s", reason)
	}
	return nil
}

// AdmitReassignIfUnclaimed is the replacement half of the same exact-prefix
// check. The named retirement is already in the prefix by construction.
func (f *Folder) AdmitReassignIfUnclaimed(actor string, payload ReassignIfUnclaimed, restsOn []string) error {
	expectation := payload.Expectation
	record := &parsedRecord{
		record:               Record{Actor: actor, RestsOn: append([]string(nil), restsOn...)},
		body:                 &State{Kind: KindRequest, Text: payload.Text, Body: payload.Body},
		unclaimedExpectation: &expectation,
		guardedReplacement:   true,
	}
	if reason := f.state.unclaimedExpectationReason(record, true); reason != "" {
		return fmt.Errorf("reassign-if-unclaimed refused: %s", reason)
	}
	return nil
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

// receiptPlan is mergeReceiptPlan judged by the scope: a receipt withdrawn by
// a cause the scope can see keeps no plan, while one withdrawn only after the
// scope's position still holds the plan that was in force there.
func (s *stalenessScope) receiptPlan(receipt *parsedRecord) map[string]string {
	if receipt == nil || s.retired(receipt.record.ID) {
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
	// The temporal world rule, enforced where authority actually lives. A
	// receipt appended straight to the log never passes through `gs merge`, so
	// a check that lives only in the CLI defends nothing: the same approval
	// would mint cross-author retirement authority through the other door.
	// A world the reviewer had already been shown refuses; one that moved after
	// they signed does not, and a flagged artifact whose causes cannot be dated
	// refuses too.
	//
	// The question is asked of the one staleness computation the projection
	// publishes from, scoped to the verdict's position, so this boundary
	// honours exactly the graph rules that decide the published flag —
	// staleness modes and the merge-plan exemption included. A second walker
	// with its own edge rules diverged three reviews running, and a divergence
	// here either refuses a sound verdict or admits an unsound one.
	scope := f.stalenessAsOf(approval.sequence())
	result := scope.stalenessOf(approval.record.RestsOn, scope.succeededRetirements())
	if result.world[artifactID] {
		return nil
	}
	reviewed := f.reviewedPathsWith(approval, implementer, state.Body["merge_candidate"], result.world)
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

// validateMergeLeftLiveNow verifies non-authoritative receipt testimony and
// freezes the succession question at the receipt's own position. It runs only
// for a receipt whose ordinary merge plan has already passed the independent
// exact-head checks above. Its result is projection evidence, never retirement
// authority: validateMergeReceiptNow does not read it and hasAuthorizedMergeReceipt
// continues to consult mergePlan alone.
func (f *foldState) validateMergeLeftLiveNow(receipt *parsedRecord, raw string, present bool) ([]leftLiveAccounting, map[string]int) {
	if !present {
		accounting := []leftLiveAccounting{{LeftLiveAccounting: LeftLiveAccounting{
			Verified: false, Reason: "merge_left_live is absent",
		}}}
		return append(accounting, f.missingLeftLiveAtReceipt(receipt, nil)...), f.unaccountedAtReceipt(receipt, nil)
	}
	var claims map[string]leftLiveClaim
	if err := json.Unmarshal([]byte(raw), &claims); err != nil || claims == nil {
		accounting := []leftLiveAccounting{{LeftLiveAccounting: LeftLiveAccounting{
			Verified: false, Reason: "merge_left_live is not valid JSON testimony",
		}}}
		return append(accounting, f.missingLeftLiveAtReceipt(receipt, nil)...), f.unaccountedAtReceipt(receipt, nil)
	}
	if !receipt.mergeChangedPathsValid && len(claims) == 0 {
		return []leftLiveAccounting{{LeftLiveAccounting: LeftLiveAccounting{
			Verified: false, Reason: "merge_changed_paths is absent or invalid",
		}}}, f.unaccountedAtReceipt(receipt, nil)
	}

	unsettled := f.unsettledCommitmentEvents()
	ids := make([]string, 0, len(claims))
	for id := range claims {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	accounting := make([]leftLiveAccounting, 0, len(ids))
	verified := make(map[string]bool, len(ids))
	classified := make(map[string]bool, len(ids))
	for _, id := range ids {
		classified[id] = true
		claim := claims[id]
		entry := leftLiveAccounting{LeftLiveAccounting: LeftLiveAccounting{
			Artifact: f.intern(id), Class: f.intern(claim.Class), Commitment: f.intern(claim.Commitment),
		}}
		artifact := f.byID[id]
		path, artifactOK := f.artifactPath(id)
		switch {
		case !artifactOK || artifact.decision.Verdict != Effective || artifact.index >= receipt.index:
			entry.Reason = "artifact is unresolved at the receipt position"
		case f.retired(id):
			entry.path = path
			entry.Reason = "artifact is not live at the receipt position"
		case hasKey(receipt.mergePlan, id):
			entry.path = path
			entry.Reason = "artifact is also classified for retirement"
		case !receipt.mergeChangedPathsValid:
			entry.path = path
			entry.Reason = "merge_changed_paths is absent or invalid"
		case !artifactCoversChangedPath(path, receipt.mergeChangedPaths):
			entry.path = path
			entry.Reason = "artifact does not cover a declared changed path"
		default:
			entry.path = path
			entry.successor = f.leftLiveSuccessor(receipt, path)
			switch claim.Class {
			case "carried":
				if claim.Commitment != "" {
					entry.Reason = "carried testimony must not name a commitment"
				} else if entry.successor != "" && (path == entry.successor || !pathCovers(path, entry.successor)) {
					entry.Reason = "carried artifact is not wider than its landed successor"
				} else {
					entry.Verified = true
				}
			case "sibling":
				if claim.Commitment == "" {
					entry.Reason = "sibling testimony names no commitment"
				} else if !unsettled[claim.Commitment] {
					entry.Reason = "commitment is unresolved or settled at the receipt position"
				} else if !f.commitmentProtectsArtifact(claim.Commitment, artifact) {
					entry.Reason = "commitment does not name or reach the artifact"
				} else {
					entry.Verified = true
				}
			case "abandoned":
				if claim.Commitment != "" {
					entry.Reason = "abandoned testimony must not name a commitment"
				} else if protector := f.liveProtector(artifact, unsettled); protector != "" {
					entry.Reason = "artifact is protected by live commitment " + protector
				} else {
					entry.Verified = true
				}
			default:
				entry.Reason = "class must be carried, sibling, or abandoned"
			}
		}
		if entry.Verified {
			verified[id] = true
		}
		accounting = append(accounting, entry)
	}
	accounting = append(accounting, f.missingLeftLiveAtReceipt(receipt, classified)...)
	return accounting, f.unaccountedAtReceipt(receipt, verified)
}

func (f *foldState) missingLeftLiveAtReceipt(receipt *parsedRecord, classified map[string]bool) []leftLiveAccounting {
	if !receipt.mergeChangedPathsValid {
		return nil
	}
	var missing []leftLiveAccounting
	for index := range f.records {
		record := &f.records[index]
		if record.index >= receipt.index || record.decision.Verdict != Effective || record.definition == nil || record.definition.Render != RenderArtifact || f.retired(record.record.ID) || classified[record.record.ID] || hasKey(receipt.mergePlan, record.record.ID) {
			continue
		}
		state, ok := record.body.(*State)
		if !ok || !artifactCoversChangedPath(state.Body["path"], receipt.mergeChangedPaths) {
			continue
		}
		missing = append(missing, leftLiveAccounting{
			LeftLiveAccounting: LeftLiveAccounting{Artifact: record.record.ID, Verified: false, Reason: "not classified by receipt"},
			path:               state.Body["path"], successor: f.leftLiveSuccessor(receipt, state.Body["path"]),
		})
	}
	return missing
}

// parseMergeChangedPaths accepts only the canonical representation the merge
// client signs: a JSON array of sorted, deduplicated, non-empty exact paths.
// Canonical bytes matter because this is durable receipt testimony, not a
// convenience input the fold may silently normalize.
func parseMergeChangedPaths(raw string, present bool) ([]string, bool) {
	if !present {
		return nil, false
	}
	var paths []string
	if err := json.Unmarshal([]byte(raw), &paths); err != nil || paths == nil {
		return nil, false
	}
	for index, path := range paths {
		if path == "" || (index > 0 && paths[index-1] >= path) {
			return nil, false
		}
	}
	canonical, err := json.Marshal(paths)
	if err != nil || string(canonical) != raw {
		return nil, false
	}
	return paths, true
}

func artifactCoversChangedPath(artifact string, changed []string) bool {
	for _, path := range changed {
		if pathCovers(artifact, path) {
			return true
		}
	}
	return false
}

// unaccountedAtReceipt counts live artifacts at each declared successor path
// exactly when the receipt lands. Planned retirements and verified left-live
// testimony account for a predecessor. Later retirement cannot change this
// stored answer.
func (f *foldState) unaccountedAtReceipt(receipt *parsedRecord, verified map[string]bool) map[string]int {
	if !receipt.mergeChangedPathsValid {
		unaccounted := make(map[string]int)
		for _, successor := range f.mergeSuccessorPaths(receipt) {
			if successor != "" {
				unaccounted[successor] = 1
			}
		}
		if len(unaccounted) == 0 {
			unaccounted[""] = 1
		}
		return unaccounted
	}
	paths := f.mergeSuccessorPaths(receipt)
	if len(paths) == 0 {
		return nil
	}
	unaccounted := make(map[string]int, len(paths))
	for index := range f.records {
		record := &f.records[index]
		if record.index >= receipt.index || record.decision.Verdict != Effective || record.definition == nil || record.definition.Render != RenderArtifact || f.retired(record.record.ID) {
			continue
		}
		state, ok := record.body.(*State)
		if !ok || !artifactCoversChangedPath(state.Body["path"], receipt.mergeChangedPaths) {
			continue
		}
		if _, planned := receipt.mergePlan[record.record.ID]; planned || verified[record.record.ID] {
			continue
		}
		successor := closestCoveringPath(paths, state.Body["path"])
		unaccounted[successor]++
	}
	return unaccounted
}

func hasKey(values map[string]string, key string) bool {
	_, ok := values[key]
	return ok
}

func (f *foldState) leftLiveSuccessor(receipt *parsedRecord, predecessor string) string {
	return closestCoveringPath(f.mergeSuccessorPaths(receipt), predecessor)
}

func closestCoveringPath(successors []string, predecessor string) string {
	winner := ""
	for _, successor := range successors {
		if !pathCovers(successor, predecessor) {
			continue
		}
		// Attach to the closest declared successor. If docs and docs/how-to
		// both cover one predecessor, docs/how-to is the actionable scope.
		if winner == "" || (successor != winner && pathCovers(winner, successor)) {
			winner = successor
		}
	}
	return winner
}

func (f *foldState) mergeSuccessorPaths(receipt *parsedRecord) []string {
	state, ok := receipt.body.(*State)
	if !ok {
		return nil
	}
	var paths []string
	if err := json.Unmarshal([]byte(state.Body["merge_successors"]), &paths); err != nil {
		return nil
	}
	return paths
}

// unsettledCommitmentEvents indexes each request, promise, and report event in
// a commitment row which still expects an actor to move. The projection is
// evaluated while the receipt is appended, so this answer is already scoped
// to the receipt position and is then stored rather than recomputed.
func (f *foldState) unsettledCommitmentEvents() map[string]bool {
	succeeded := f.succeededRetirements()
	result := f.stalenessNow().staleness(succeeded)
	active := make(map[string]bool)
	for _, commitment := range f.projectCommitments(result.stale) {
		switch commitment.Status {
		case "open", "promised", "reported", "awaiting-review", "awaiting-authorization", "awaiting-landing", "stale":
			active[commitment.Request] = true
			if commitment.Promise != "" {
				active[commitment.Promise] = true
			}
			if commitment.Report != "" {
				active[commitment.Report] = true
			}
		}
	}
	return active
}

func (f *foldState) liveProtector(artifact *parsedRecord, unsettled map[string]bool) string {
	ids := make([]string, 0, len(unsettled))
	for id := range unsettled {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if f.commitmentProtectsArtifact(id, artifact) {
			return id
		}
	}
	return ""
}

func (f *foldState) commitmentProtectsArtifact(commitment string, artifact *parsedRecord) bool {
	record := f.byID[commitment]
	if record == nil || record.decision.Verdict != Effective || record.definition == nil {
		return false
	}
	switch record.definition.Lifecycle {
	case LifecycleRequest, LifecyclePromise, LifecycleReport:
	default:
		return false
	}
	state, ok := record.body.(*State)
	implementation, artifactOK := artifact.body.(*State)
	if !ok || !artifactOK {
		return false
	}
	commit := implementation.Body["commit"]
	if commit != "" && (state.Body["head"] == commit || state.Body["commit"] == commit) {
		return true
	}
	// Requests and reports can name or cite an already-published artifact.
	// The ordinary implementation shape points the other way: an artifact
	// published later rests on the live promise it fulfils. Both directions
	// prove that this exact durable lane stands behind this exact artifact;
	// merely sharing an actor, branch, or path does not.
	return f.effectiveProvenanceReaches(commitment, artifact.record.ID) ||
		f.effectiveProvenanceReaches(artifact.record.ID, commitment)
}

func (f *foldState) effectiveProvenanceReaches(from, target string) bool {
	seen := make(map[string]bool)
	var walk func(string) bool
	walk = func(current string) bool {
		if current == target {
			return true
		}
		if seen[current] {
			return false
		}
		seen[current] = true
		record := f.byID[current]
		if record == nil || record.decision.Verdict != Effective {
			return false
		}
		for _, basis := range record.record.RestsOn {
			if walk(basis) {
				return true
			}
		}
		return false
	}
	return walk(from)
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
//
// reviewedPathsWith takes the verdict-scoped world map the caller already
// computed, so the co-signed members are judged by the same pass, at the same
// position, as the primary artifact. A member the reviewer signed still widens
// the receipt's reach when the world moved after they signed; one that had
// already moved does not widen it at all, and neither does one whose causes
// cannot be dated.
func (f *foldState) reviewedPathsWith(approval *parsedRecord, implementer, head string, world map[string]bool) []string {
	var paths []string
	seen := make(map[string]bool)
	for _, basis := range approval.record.RestsOn {
		cited := f.byID[basis]
		if cited == nil || cited.decision.Verdict != Effective || f.retired(basis) {
			continue
		}
		if world[basis] {
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

// The three refusals the live-membership boundary can give. They are named
// because the tests assert on them: a guard that refused for some other reason
// — a missing field, an unknown kind — would satisfy a verdict check while
// leaving the boundary open, which is the shape of the defect this closes.
const (
	departedAuthorReason    = "statement author is not a live participant"
	departedRequesterReason = "requester is not a live participant"
	departedReceiptReason   = "merge receipt authority requires live participation"
)

func (f *foldState) hasActor(actor string) bool {
	return f.hasRole(actor, "participant")
}

// originatingRequest finds who may ratify a report: the actor who asked for it.
// It reads the same normalised claim admission used, so a report that was
// admitted is ratifiable by exactly the requester that admitted it.
func (f *foldState) originatingRequest(report *parsedRecord) *parsedRecord {
	claim, admitted := f.admittedClaims[report.record.ID]
	if !admitted {
		return nil
	}
	return claim.request
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

// stalenessScope binds the one staleness computation to the position its
// retirement causes are judged against. The projection asks "as of now" and
// uses an unbounded scope; the merge-receipt boundary asks "as of the verdict"
// and uses the approval's sequence. Both run the same pass over the same
// graph, so both honour the governing definitions' staleness modes and the
// merge-plan exemption — a second walker with its own edge rules is exactly
// the divergence this type exists to make impossible.
//
// Whether a retirement counts as a cause is judged with everything the fold
// now knows — a supersession that has itself been withdrawn is not a cause at
// any position — and each surviving cause is then dated against asOf. That is
// the committed rule: date the moved world, and judge it as of the verdict.
type stalenessScope struct {
	f *foldState
	// asOf is the position causes are dated against. A cause that landed
	// after it did not exist for the question being asked.
	asOf int
	// active dates every retired event by the earliest supersession still
	// accounting for it. Built once per scope: rescanning per edge would be
	// quadratic, and the answer cannot change inside one pass.
	active map[string]int
	// transfers maps each retired request to the successor a surviving
	// rejected-round transfer named for it, at or before this scope's
	// position. Built once for the same reason active is.
	transfers map[string]string
}

// stalenessCauseEdge is one rests-on edge that the authoritative staleness
// pass actually used. Direct is true only when retirement of Basis itself was
// a cause on this edge. Keeping that bit matters when a successfully replaced
// basis is both retired and stale: its retirement is settled, while staleness
// from a different cause may still propagate through it.
type stalenessCauseEdge struct {
	Basis  string
	Direct bool
}

type stalenessResult struct {
	stale    map[string]bool
	world    map[string]bool
	causedAt map[string]int
	causes   map[string][]stalenessCauseEdge
}

// stalenessNow answers with end-of-log knowledge: every active cause counts,
// whenever it landed. math.MaxInt rather than the record count, so the scope
// cannot quietly become position-bound if records fold after it is built.
func (f *foldState) stalenessNow() *stalenessScope {
	return f.stalenessAsOf(math.MaxInt)
}

func (f *foldState) stalenessAsOf(asOf int) *stalenessScope {
	return &stalenessScope{f: f, asOf: asOf, active: f.activeRetirements(), transfers: f.transferSuccessors(asOf)}
}

// transferSuccessors indexes the rejected-round transfers in force: for each
// retired request, the successor named by the first surviving supersession
// that transferred it. A supersession that landed after the scope's position
// did not exist for the question being asked, exactly as in
// succeededRetirements.
func (f *foldState) transferSuccessors(asOf int) map[string]string {
	transfers := make(map[string]string)
	for _, supersession := range f.supersessions {
		if supersession.sequence() > asOf || supersession.linkedSuccessorRequest == "" || f.retired(supersession.record.ID) {
			continue
		}
		value, ok := supersession.body.(*Supersede)
		if !ok {
			continue
		}
		if _, already := transfers[value.Target]; already {
			continue
		}
		transfers[value.Target] = supersession.linkedSuccessorRequest
	}
	return transfers
}

// succeededRetirements is the scope's own successor topology. A supersession
// that landed after the scope's position did not exist for the question being
// asked, so the successor edge it declares must not reach back and change
// whether an earlier retirement condemned or merely moved a pointer. Taking
// this from the fold instead let a post-verdict edge decide plan condemnation
// and receipt authority at a verdict that could not have seen it -- the same
// end-of-log fact leaking into a positioned computation that every other defect
// in this area has been. The basis is held to the same position as the record:
// filtering one half of the fact and resolving the other with end-of-log
// knowledge filters nothing.
func (s *stalenessScope) succeededRetirements() map[string]string {
	succeeded := make(map[string]string)
	for _, record := range s.f.supersessions {
		if record.sequence() > s.asOf {
			continue
		}
		supersede := record.body.(*Supersede)
		retiredPath, targetIsArtifact := s.f.artifactPath(supersede.Target)
		if _, already := succeeded[supersede.Target]; !targetIsArtifact || !s.f.isArtifact(supersede.Target) || already {
			continue
		}
		for _, basis := range record.record.RestsOn {
			if basis == supersede.Target || !s.f.isArtifact(basis) {
				continue
			}
			// The supersession's own position is only half the fact. Its basis
			// is an ID the signer chose, and an ID can be pre-signed: a
			// supersession filed before the scope's position can name an
			// artifact that materialises only after it. Resolving that basis
			// with end-of-log knowledge would add the successor edge
			// retroactively — the same leak as a late supersession, through
			// the other field — so the basis record must itself have landed at
			// or before the position to serve as a successor here.
			if successor := s.f.byID[basis]; successor == nil || successor.sequence() > s.asOf {
				continue
			}
			if successorPath, ok := s.f.artifactPath(basis); ok && pathCovers(successorPath, retiredPath) {
				succeeded[supersede.Target] = basis
				break
			}
		}
	}
	return succeeded
}

// retired reports whether an event counts as retired for this scope: retired
// with everything the fold now knows, by a cause that had landed at or before
// asOf. An active retirement the scope cannot date fails closed and counts at
// every position — an undated cause is not permission.
func (s *stalenessScope) retired(event string) bool {
	if !s.f.retired(event) {
		return false
	}
	at := s.active[event]
	return at == 0 || at <= s.asOf
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
func (s *stalenessScope) staleness(successors map[string]string) stalenessResult {
	return s.stalenessOf(nil, successors)
}

func (s *stalenessScope) stalenessWithCauses(successors map[string]string) stalenessResult {
	return s.computeStaleness(nil, successors, true)
}

// stalenessOf is the same pass restricted to the ancestor closure of the given
// events; nil means every record. The restriction is sound because staleness
// only ever flows from a cited basis to a later dependent, so a record outside
// a target's ancestry cannot change the target's row, and everything the pass
// reads beside its own maps — retirement counters, cause dates, succession
// links, sealed receipt plans — is indexed fold state, not a product of the
// iteration. It exists because the receipt boundary asks about a handful of
// cited artifacts per receipt, and paying a whole-log pass for each receipt
// measured at four times the cost of the fold itself.
func (s *stalenessScope) stalenessOf(targets []string, successors map[string]string) stalenessResult {
	return s.computeStaleness(targets, successors, false)
}

func (s *stalenessScope) computeStaleness(targets []string, successors map[string]string, explain bool) stalenessResult {
	f := s.f
	stale := make(map[string]bool)
	world := make(map[string]bool)
	var causes map[string][]stalenessCauseEdge
	if explain {
		causes = make(map[string][]stalenessCauseEdge)
	}
	// causedAt dates each world-stale record by the earliest retirement still
	// accounting for it. A reader deciding whether a judgement predates the move
	// needs that position, and cannot recover it from the acts, which say a
	// supersession happened but not whether its own supersession withdrew it.
	causedAt := make(map[string]int)
	for _, index := range s.closure(targets) {
		record := &f.records[index]
		// A record past the scope's position cannot carry a fact the question
		// is about, and propagation only ever flows from a basis to a later
		// dependent, so stopping here changes no earlier answer. For the
		// unbounded scope this cuts nothing.
		if record.sequence() > s.asOf {
			break
		}
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
		plan := s.withoutCondemnedSuccessions(s.mergeSuccessionPlan(record), successors)
		for _, basis := range record.record.RestsOn {
			if plan != nil {
				if _, intended := plan[basis]; intended && s.retired(basis) {
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
			retiredBasis := s.retired(basis) && mode != StalenessExempt
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
				if _, moved := successors[basis]; moved && !s.successionCondemned(basis, successors) {
					retiredBasis = false
				}
			}
			// The rejected-round transfer, and nothing else. A repair child
			// must rest on the request it repairs, and the transfer that makes
			// it the successor retires that request in the same act, so the
			// child was stale the moment it became the answer. The exception is
			// keyed to that one edge and that one relation: staleness reaching
			// the successor by any other edge still lands, a successor that
			// merely rests on a retired request without being named as its
			// transfer successor still stales, and a carried or abandoned
			// succession gets nothing from here.
			if retiredBasis && s.transfers[basis] == record.record.ID {
				retiredBasis = false
			}
			staleBasis := stale[basis] && mode == StalenessPropagates
			if plan != nil && staleBasis && s.stalenessCoveredByMergePlan(basis, plan, stale, make(map[string]bool)) {
				continue
			}
			// The receipt checkpoint, on this one edge and no other. A merge
			// settles the reasoning it was published under, so a successor that
			// merge stood up does not begin life carrying its receipt's own
			// historical staleness. The receipt keeps that staleness; only the
			// successor starts the new current epoch.
			//
			// It is confined to this basis so that describes_superseded_world is
			// untouched. A receipt is an assert, never an artifact, so this edge
			// could not have carried the world flag in either direction; every
			// other basis of the same successor is examined exactly as before.
			if staleBasis && s.receiptCheckpointSettles(record, basisRecord, stale, successors) {
				continue
			}
			if !retiredBasis && !staleBasis {
				continue
			}
			stale[record.record.ID] = true
			if explain {
				causes[record.record.ID] = append(causes[record.record.ID], stalenessCauseEdge{
					Basis: basis, Direct: retiredBasis,
				})
			}
			if (world[basis] && artifactProvenance) || (retiredBasis && f.isArtifact(basis)) {
				world[record.record.ID] = true
				// Every basis is examined. Stopping at the first world-bearing
				// one would let the order a signer wrote its Rests-On in decide
				// the date, hiding an older cause behind a newer one, and the
				// date gates an irreversible merge.
				at := s.active[basis]
				// A Terminal basis catches staleness without passing it on,
				// and that has to govern the date as well as the flag. Its own
				// causedAt records a cause it stopped; inheriting that here
				// would date this record by something the vocabulary says never
				// reached it, and the date could then predate a verdict the
				// scope holds was not looking at a moved world at all.
				if inherited, ok := causedAt[basis]; ok && artifactProvenance && mode != StalenessTerminal &&
					(at == 0 || (inherited != 0 && inherited < at)) {
					at = inherited
				}
				if at != 0 && (causedAt[record.record.ID] == 0 || at < causedAt[record.record.ID]) {
					causedAt[record.record.ID] = at
				}
			}
		}
	}
	return stalenessResult{stale: stale, world: world, causedAt: causedAt, causes: causes}
}

// closure returns the record indexes the pass visits, in sequence order:
// every record when targets is nil, otherwise the targets and their
// transitive bases. Members whose records the log does not contain are
// skipped; the pass could only have skipped them too.
func (s *stalenessScope) closure(targets []string) []int {
	if targets == nil {
		indices := make([]int, len(s.f.records))
		for index := range indices {
			indices[index] = index
		}
		return indices
	}
	seen := make(map[string]bool, len(targets))
	var indices []int
	var queue []*parsedRecord
	add := func(event string) {
		if seen[event] {
			return
		}
		seen[event] = true
		if record := s.f.byID[event]; record != nil {
			indices = append(indices, record.index)
			queue = append(queue, record)
		}
	}
	for _, target := range targets {
		add(target)
	}
	for len(queue) > 0 {
		record := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		for _, basis := range record.record.RestsOn {
			add(basis)
		}
	}
	sort.Ints(indices)
	return indices
}

// activeRetirements dates every retired event by the earliest supersession
// still accounting for it. A supersession that has itself been superseded is
// not a cause: the fold already withdrew its effect from the retirement
// counter, and a date taken from the bare act would outlive that withdrawal and
// describe a world that had come back. Built once per scope, in one pass.
func (f *foldState) activeRetirements() map[string]int {
	earliest := make(map[string]int)
	for id, target := range f.effectiveSup {
		if f.retired(id) {
			continue
		}
		// f.effectiveSup is only written for a record the fold ruled effective,
		// so no verdict check belongs here: one would read as a guard while
		// being unreachable, which is worse than its absence.
		cause := f.byID[id]
		if cause == nil {
			continue
		}
		at := cause.sequence()
		if known, seen := earliest[target]; !seen || at < known {
			earliest[target] = at
		}
	}
	return earliest
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
func (s *stalenessScope) mergeSuccessionPlan(record *parsedRecord) map[string]string {
	own := s.receiptPlan(record)
	var carried []map[string]string
	for _, basis := range record.record.RestsOn {
		receipt := s.f.byID[basis]
		plan := s.receiptPlan(receipt)
		if len(plan) == 0 || !s.f.publishedByMerge(record, receipt) {
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

func (s *stalenessScope) stalenessCoveredByMergePlan(event string, plan map[string]string, stale map[string]bool, visiting map[string]bool) bool {
	if visiting[event] {
		return true
	}
	visiting[event] = true
	defer delete(visiting, event)
	covered := false
	if s.retired(event) {
		if _, ok := plan[event]; !ok {
			return false
		}
		covered = true
	}
	record := s.f.byID[event]
	if record == nil {
		return covered
	}
	for _, basis := range record.record.RestsOn {
		mode := StalenessPropagates
		if basisRecord := s.f.byID[basis]; basisRecord != nil && basisRecord.definition != nil {
			mode = basisRecord.definition.Staleness
		}
		if mode == StalenessExempt || (!s.retired(basis) && !(stale[basis] && mode == StalenessPropagates)) {
			continue
		}
		if !s.stalenessCoveredByMergePlan(basis, plan, stale, visiting) {
			return false
		}
		covered = true
	}
	return covered
}

// receiptCheckpointSettles reports whether a sealed merge receipt is a
// freshness checkpoint for one successor it published: whether the ordinary
// reasoning that made the receipt stale was already accounted for when the
// merge published this artifact, so the artifact is not born stale.
//
// Every condition below is a fact the beneficiary could not write for itself.
// The receipt must be prospective — merge_left_live present and the
// merge_changed_paths list canonical — because that pair is the only version
// seam the fold has for saying the receipt was written under this contract. A
// historical receipt carrying neither field, and a malformed receipt carrying
// one half or an uncanonical list, gain nothing and keep their existing
// projection exactly. It must hold a validated retirement plan, which is the
// independent approval chain already checked at seal time. The successor must
// be signed by the receipt's own author, stand at the receipt's exact merge
// head, and stand at a path the receipt declared it would publish. A record
// that merely cites a receipt is a bystander: the checkpoint is not a signal
// any passer-by may borrow.
//
// Whether individual left-live testimony verifies is deliberately not read.
// That testimony is snapshot accounting about other people's candidates, and
// letting an unverified sibling claim decide this artifact's freshness would
// give one actor's unchecked prose authority over another lane's projection.
// Only the presence of the pair and the canonical form of the frontier matter.
func (s *stalenessScope) receiptCheckpointSettles(successor, receipt *parsedRecord, stale map[string]bool, successors map[string]string) bool {
	if receipt == nil {
		return false
	}
	// mergeChangedPathsValid is false whenever the field is absent, so it is
	// both halves of the frontier test: present, and canonical.
	if !receipt.mergeLeftLivePresent || !receipt.mergeChangedPathsValid {
		return false
	}
	// The sealed plan, and no separate verdict test: a plan is only ever written
	// for a receipt the fold ruled effective. Nor is there a separate test for
	// the receipt being retired, which the ruling says must still flare the
	// successor. The walk below starts at the receipt itself and its own
	// retirement is the first cause it weighs, so that rule falls out of the
	// dated computation instead of being restated beside it where neither copy
	// could be shown to be doing the work.
	plan := receipt.mergePlan
	if plan == nil {
		return false
	}
	if successor.record.Actor != receipt.record.Actor {
		return false
	}
	if !s.f.publishedByMerge(successor, receipt) {
		return false
	}
	return s.causesSettledAtReceipt(receipt.record.ID, receipt.sequence(),
		s.withoutCondemnedSuccessions(plan, successors), stale, make(map[string]bool))
}

// causesSettledAtReceipt walks the live staleness causes at and under a sealed
// receipt and reports whether the merge had already accounted for every one of
// them. A cause counts as settled when the receipt's own narrowed plan names it
// — the merge performed that retirement itself — or when the supersession that
// caused it landed at or before the receipt's own position, so the merge was
// published with that news in hand.
//
// The walk begins at the receipt rather than at its bases, which is what makes
// the withdrawal of the receipt itself flare its successors: a receipt cannot
// name itself in its own plan, since the plan is cut to artifacts and a receipt
// is an assert, and its retirement can only ever be dated after it.
//
// The walk is per cause and dated. Asking instead whether the receipt was
// already stale as of its own position is not the same question and is wrong
// where it matters: with one old cause and one new one, the receipt was stale
// then and is stale now, and the cheap comparison would settle the new cause
// along with the old one.
//
// A cause the fold cannot date fails closed. activeRetirements leaves the date
// zero for a retirement whose act the fold does not hold, and an undated cause
// is not permission to call anything fresh: unknown means no.
func (s *stalenessScope) causesSettledAtReceipt(event string, at int, plan map[string]string, stale map[string]bool, visited map[string]bool) bool {
	// Both the cycle guard and the memo. Only a settled answer is ever recorded,
	// because the first unsettled cause abandons the whole walk.
	if visited[event] {
		return true
	}
	visited[event] = true
	if s.retired(event) {
		if _, planned := plan[event]; !planned {
			when := s.active[event]
			if when == 0 || when > at {
				return false
			}
		}
	}
	record := s.f.byID[event]
	if record == nil {
		return true
	}
	for _, basis := range record.record.RestsOn {
		mode := StalenessPropagates
		if basisRecord := s.f.byID[basis]; basisRecord != nil && basisRecord.definition != nil {
			mode = basisRecord.definition.Staleness
		}
		if mode == StalenessExempt || (!s.retired(basis) && !(stale[basis] && mode == StalenessPropagates)) {
			continue
		}
		if !s.causesSettledAtReceipt(basis, at, plan, stale, visited) {
			return false
		}
	}
	return true
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
	for _, record := range f.supersessions {
		supersede := record.body.(*Supersede)
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
func (s *stalenessScope) withoutCondemnedSuccessions(plan map[string]string, successors map[string]string) map[string]string {
	if plan == nil {
		return nil
	}
	narrowed := make(map[string]string, len(plan))
	for artifact, path := range plan {
		if _, moved := successors[artifact]; moved && s.successionCondemned(artifact, successors) {
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
func (s *stalenessScope) successionCondemned(artifact string, successors map[string]string) bool {
	visited := map[string]bool{artifact: true}
	current := artifact
	for {
		next, moved := successors[current]
		if !moved {
			return s.retired(current)
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

// lifecycleOf reports the commitment role a record was decided under. It reads
// the definition the fold bound to that record, so a kind later redefined does
// not retroactively change what its earlier records were.
func lifecycleOf(record *parsedRecord) Lifecycle {
	if record.definition == nil {
		return LifecycleNone
	}
	return record.definition.Lifecycle
}

// satisfierOf reports who may ratify this record, from the definition the fold
// bound to it at admission. decideRatify reads the same field off the same
// captured definition, so what a reader is shown and what the fold will decide
// cannot drift apart when the kind is redefined afterwards.
func satisfierOf(record *parsedRecord) string {
	if record.definition == nil {
		return ""
	}
	return record.definition.Satisfier
}

// staleCauseHopLimit bounds the explanation attached to one projection row.
// It does not bound staleness: the authoritative pass above remains unbounded.
const staleCauseHopLimit = 4

// nearestRetiredCause walks only edges the authoritative staleness pass used.
// Breadth-first order chooses the fewest hops, and cause slices preserve the
// signer's citation order as the deterministic tie-break. The visited set is
// defensive: malformed or future-addressed provenance must not turn a read
// model into an unbounded walk.
func (s *stalenessScope) nearestRetiredCause(event string, causes map[string][]stalenessCauseEdge) (because, path string, truncated bool) {
	visited := map[string]bool{event: true}
	frontier := []string{event}
	for hop := 0; hop < staleCauseHopLimit && len(frontier) != 0; hop++ {
		var next []string
		for _, current := range frontier {
			for _, edge := range causes[current] {
				if edge.Direct {
					path, _ := s.f.artifactPath(edge.Basis)
					return edge.Basis, path, false
				}
				if visited[edge.Basis] {
					continue
				}
				visited[edge.Basis] = true
				next = append(next, edge.Basis)
			}
		}
		frontier = next
	}
	return "", "", len(frontier) != 0
}

func (f *foldState) project() Projection {
	succeeded := f.succeededRetirements()
	staleness := f.stalenessNow()
	result := staleness.stalenessWithCauses(succeeded)
	// How many artifacts seen at each path are still live. project() runs over
	// a fully folded log, so retired() is final here and one running count per
	// path answers both questions this projection asks about succession, at
	// constant cost per artifact.
	//
	// It is not the shortcut an earlier head warned about, which kept only the
	// immediate predecessor and so let a retirement hide a live ancestor: with
	// A, B and C at one path, retiring B cleared C's warning while A stayed
	// live. A count keeps every ancestor.
	liveByPath := make(map[string]int)
	liveArtifactsByPath := make(map[string][]string)
	protectedSiblings := make(map[string]bool)
	receiptDebts := make(map[string]bool)
	var currentUnsettled map[string]bool
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
		// One call, two fields. f.ratified is exactly this being non-empty, so
		// asking twice would be two implementations of one rule waiting to
		// drift.
		ratification := f.activeRatification(record.record.ID)
		statement := Statement{
			Event: record.record.ID, Sequence: record.sequence(),
			Timestamp: record.record.Timestamp, Actor: record.record.Actor, Kind: state.Kind,
			Lifecycle: lifecycleOf(&record), Satisfier: satisfierOf(&record),
			Text: state.Text, Body: cloneStringMap(state.Body),
			Ratified: ratification != "", RatifiedBy: ratification,
			Retired: f.retired(record.record.ID), Stale: result.stale[record.record.ID],
			DescribesSupersededWorld: result.world[record.record.ID],
			WorldSupersededAt:        result.causedAt[record.record.ID],
			MergeLeftLive:            projectLeftLive(record.mergeLeftLive, ""),
		}
		if statement.Stale {
			statement.StaleBecause, statement.StaleBecausePath, statement.StaleBecauseTruncated =
				staleness.nearestRetiredCause(record.record.ID, result.causes)
		}
		projection.Statements = append(projection.Statements, statement)
		if record.decision.Verdict == UndefinedKind {
			projection.OpaqueKinds[string(state.Kind)] = append(projection.OpaqueKinds[string(state.Kind)], record.record.ID)
		}
		// Only an effective statement points at anything. A refused one keeps
		// its decision and its statement row; it does not become a live
		// artifact carrying whatever fields it did manage to fill.
		if record.decision.Verdict == Effective && record.definition != nil && record.definition.Render == RenderArtifact {
			path := state.Body["path"]
			live := liveByPath[path]
			var leftLive []LeftLiveAccounting
			if receipt := f.leftLiveReceiptFor(&record); receipt != nil {
				// The receipt snapshot is immutable. Artifacts after its frontier
				// remain unaccounted even if a later retirement would have removed
				// them from the old end-of-fold count.
				postCount, postLive := f.postReceiptAccounting(receipt, &record, path)
				live = receipt.mergeUnaccounted[path] + postCount
				leftLive = projectLeftLive(receipt.mergeLeftLive, path)
				if !f.retired(record.record.ID) {
					for _, artifact := range postLive {
						receiptDebts[artifact] = true
					}
					for _, entry := range receipt.mergeLeftLive {
						if !entry.Verified || entry.successor != path || f.retired(entry.Artifact) {
							continue
						}
						switch entry.Class {
						case "sibling":
							if currentUnsettled == nil {
								currentUnsettled = f.unsettledCommitmentEvents()
							}
							artifact := f.byID[entry.Artifact]
							if currentUnsettled[entry.Commitment] && artifact != nil && f.commitmentProtectsArtifact(entry.Commitment, artifact) && !f.hasPostReceiptLiveArtifact(receipt, artifact, &record) {
								protectedSiblings[entry.Artifact] = true
							} else {
								receiptDebts[entry.Artifact] = true
							}
						case "abandoned":
							receiptDebts[entry.Artifact] = true
						}
					}
				}
			}
			projection.Artifacts = append(projection.Artifacts, Artifact{
				Event: record.record.ID, Path: path, Commit: state.Body["commit"],
				Retired:                  f.retired(record.record.ID),
				Succeeded:                f.retired(record.record.ID) && succeeded[record.record.ID] != "",
				Stale:                    result.stale[record.record.ID],
				DescribesSupersededWorld: result.world[record.record.ID],
				WorldSupersededAt:        result.causedAt[record.record.ID],
				StaleBecause:             statement.StaleBecause,
				StaleBecausePath:         statement.StaleBecausePath,
				StaleBecauseTruncated:    statement.StaleBecauseTruncated,
				UnableToFlare:            f.unableToFlare(record.record.RestsOn),
				SuccessionUnrecorded:     live > 0,
				LivePredecessors:         live,
				MergeLeftLive:            leftLive,
			})
			if !f.retired(record.record.ID) {
				liveByPath[path]++
				liveArtifactsByPath[path] = append(liveArtifactsByPath[path], record.record.ID)
			}
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
				Retired:  f.retired(record.record.ID), Stale: result.stale[record.record.ID],
			})
			reviewBases = append(reviewBases, reviewBasis{named: state.Body["artifact"], restsOn: record.record.RestsOn})
		}
	}
	resolveReviews(projection.Reviews, reviewBases, implementers, artifactCommits, artifactsByCommit)
	// A live artifact owes its retirement only while a live artifact later at
	// the same path stands in its place. A successor that was itself withdrawn
	// asks for nothing: acting on that warning would retire the current
	// artifact because of a replacement that no longer exists. So at each path
	// every live artifact except the newest owes one, which is the live count
	// less one, and nothing less than the count is needed to say it. This is
	// not summed from LivePredecessors, which counts a shared ancestor once
	// per successor and would multiply the same debt.
	owed := make(map[string]bool)
	for _, artifacts := range liveArtifactsByPath {
		if len(artifacts) < 2 {
			continue
		}
		for _, artifact := range artifacts[:len(artifacts)-1] {
			owed[artifact] = true
		}
	}
	for artifact := range protectedSiblings {
		delete(owed, artifact)
	}
	for artifact := range receiptDebts {
		owed[artifact] = true
	}
	projection.OmittedSupersessions = len(owed)
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
	projection.Commitments = f.projectCommitments(result.stale)
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

func projectLeftLive(input []leftLiveAccounting, path string) []LeftLiveAccounting {
	var output []LeftLiveAccounting
	for _, entry := range input {
		if path != "" && entry.successor != path {
			continue
		}
		output = append(output, entry.LeftLiveAccounting)
	}
	return output
}

// leftLiveReceiptFor finds the prospective receipt that published artifact.
// Historical receipts have mergeLeftLivePresent false and therefore retain
// the old end-of-fold succession behavior byte for byte.
func (f *foldState) leftLiveReceiptFor(artifact *parsedRecord) *parsedRecord {
	var latest *parsedRecord
	for _, basis := range artifact.record.RestsOn {
		receipt := f.byID[basis]
		if receipt == nil || !receipt.mergeAccountingPresent || receipt.mergePlan == nil || !f.publishedByMerge(artifact, receipt) {
			continue
		}
		if latest == nil || receipt.index > latest.index {
			latest = receipt
		}
	}
	return latest
}

// postReceiptAccounting scans the receipt-to-successor interval once for both
// projections of the same fact: the frozen count records every covered
// post-frontier artifact, while live IDs are the subset which still owe
// retirement now.
func (f *foldState) postReceiptAccounting(receipt, successor *parsedRecord, path string) (int, []string) {
	count := 0
	var live []string
	successors := f.mergeSuccessorPaths(receipt)
	for index := receipt.index + 1; index < successor.index; index++ {
		record := &f.records[index]
		if record.decision.Verdict != Effective || record.definition == nil || record.definition.Render != RenderArtifact {
			continue
		}
		state, ok := record.body.(*State)
		if ok && artifactCoversChangedPath(state.Body["path"], receipt.mergeChangedPaths) && closestCoveringPath(successors, state.Body["path"]) == path {
			count++
			if !f.retired(record.record.ID) {
				live = append(live, record.record.ID)
			}
		}
	}
	return count, live
}

// hasPostReceiptLiveArtifact keeps a receipt's protection from suppressing a
// different succession debt created after its frontier. The receipt-published
// successor itself is the accounted replacement and is excluded; any other
// later live artifact at the sibling's exact path is new, unsealed work.
func (f *foldState) hasPostReceiptLiveArtifact(receipt, artifact, successor *parsedRecord) bool {
	artifactState, ok := artifact.body.(*State)
	if !ok {
		return false
	}
	path := artifactState.Body["path"]
	for index := receipt.index + 1; index < len(f.records); index++ {
		record := &f.records[index]
		if record.record.ID == successor.record.ID || f.retired(record.record.ID) || record.decision.Verdict != Effective || record.definition == nil || record.definition.Render != RenderArtifact {
			continue
		}
		state, ok := record.body.(*State)
		if ok && state.Body["path"] == path {
			return true
		}
	}
	return false
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
		successorRequest := f.linkedRequestSuccessor(requestRecord.record.ID)
		abandoned := f.abandonedRequest(requestRecord.record.ID)
		// A direct completion is projected because it was admitted as one, not
		// because of how many claims stand now. Gating on the current count
		// reversed decisions the fold had already made: a later promise hid an
		// admitted direct completion, and a withdrawn promise turned its own
		// report into a second one.
		{
			claim := &requestRecord
			performer := request.Body["to"]
			entry := Commitment{Request: requestRecord.record.ID, Requester: requestRecord.record.Actor, AddressedTo: performer, Status: "open", Stale: stale[requestRecord.record.ID]}
			result, approved := f.landingFields(&entry, &requestRecord, claim, performer)
			completion := f.latestCompletion(claim, performer, mergedArtifacts, result.landing && !result.legacy)
			// With promises present but none live and nothing reported since,
			// the request has no row of its own: the reneged rows below are the
			// whole story, and an "open" row beside them would invite a second
			// claim the withdrawal already answered.
			if completion != nil || len(promises) == 0 {
				// Claim and complete in one act. The addressee answered the
				// request directly: an explicit report waits on requester
				// ratification, while an artifact waits on review and then on
				// the landing. Reading either as open would show work already
				// finished as work nobody took.
				switch {
				// Abandonment is judged first. A supersession may both qualify
				// as a rejected-round transfer and declare an approved head
				// dropped; the declaration is the explicit one, and reading it
				// second would show the head as carried into a successor that
				// was never given it.
				case abandoned:
					entry.Status = "abandoned"
					entry.Terminal = "abandoned"
					entry.WaitingOn = ""
					if completion != nil {
						entry.Performer = completion.record.Actor
						entry.Report = completion.record.ID
						entry.Stale = entry.Stale || stale[completion.record.ID]
					}
				case successorRequest != "":
					entry.Status = "superseded"
					entry.SuccessorRequest = successorRequest
					entry.WaitingOn = ""
					if completion != nil {
						entry.Performer = completion.record.Actor
						entry.Report = completion.record.ID
						entry.Stale = entry.Stale || stale[completion.record.ID]
					}
				case f.retired(requestRecord.record.ID):
					entry.Status = "withdrawn"
				case completion != nil:
					entry.Performer = completion.record.Actor
					entry.Report = completion.record.ID
					f.completionStatus(&entry, result, completion, requestRecord.record.Actor, completion.record.Actor)
					entry.Stale = stale[requestRecord.record.ID] || stale[completion.record.ID]
					if receipt := mergedArtifacts[completion.record.ID]; receipt != nil && entry.Status != "satisfied" {
						entry.Status, entry.Terminal, entry.WaitingOn = "satisfied", "landed", ""
					}
				case stale[requestRecord.record.ID]:
					entry.Status = "stale"
				}
				f.markApprovedNotLanded(&entry, result, approved, mergedArtifacts)
				commitments = append(commitments, entry)
			}
		}
		if len(promises) == 0 {
			continue
		}
		for _, promiseRecord := range promises {
			performer := promiseRecord.record.Actor
			entry := Commitment{Request: requestRecord.record.ID, Requester: requestRecord.record.Actor, Performer: performer, Promise: promiseRecord.record.ID, Status: "promised", WaitingOn: performer}
			result, approved := f.landingFields(&entry, &requestRecord, promiseRecord, performer)
			completion := f.latestCompletion(promiseRecord, performer, mergedArtifacts, result.landing && !result.legacy)
			switch {
			// See the direct-completion branch: an explicit abandonment
			// outranks a transfer that also qualifies.
			case abandoned:
				entry.Status = "abandoned"
				entry.Terminal = "abandoned"
				entry.WaitingOn = ""
				if completion != nil {
					entry.Report = completion.record.ID
					entry.Stale = stale[requestRecord.record.ID] || stale[promiseRecord.record.ID] || stale[completion.record.ID]
				}
			case successorRequest != "":
				entry.Status = "superseded"
				entry.SuccessorRequest = successorRequest
				entry.WaitingOn = ""
				if completion != nil {
					entry.Report = completion.record.ID
					entry.Stale = stale[requestRecord.record.ID] || stale[promiseRecord.record.ID] || stale[completion.record.ID]
				}
			case f.retired(requestRecord.record.ID):
				entry.Status = "cancelled"
				entry.WaitingOn = ""
			case f.retired(promiseRecord.record.ID):
				entry.Status = "reneged"
				entry.WaitingOn = ""
			default:
				if completion != nil {
					entry.Report = completion.record.ID
					f.completionStatus(&entry, result, completion, requestRecord.record.Actor, performer)
					entry.Stale = stale[requestRecord.record.ID] || stale[promiseRecord.record.ID] || stale[completion.record.ID]
					if receipt := mergedArtifacts[completion.record.ID]; receipt != nil && entry.Status != "satisfied" {
						entry.Status, entry.Terminal, entry.WaitingOn = "satisfied", "landed", ""
						entry.Stale = entry.Stale || stale[receipt.record.ID]
					}
				}
				if entry.Report == "" && (stale[requestRecord.record.ID] || stale[promiseRecord.record.ID]) {
					entry.Status = "stale"
					entry.Stale = true
				}
			}
			f.markApprovedNotLanded(&entry, result, approved, mergedArtifacts)
			commitments = append(commitments, entry)
		}
	}
	return commitments
}

// landingFields fills the row's target, hold, approval and evidence fields and
// returns the result in force for it alongside the newest reporting artifact at
// an approved head. Those two answers are what every landing rule
// below reads; computing them once per row keeps the status word, the waiting
// party and the audit fact from being derived three different ways.
func (f *foldState) landingFields(entry *Commitment, request, claim *parsedRecord, performer string) (requestResult, *parsedRecord) {
	result := f.commitmentResult(request, claim, performer)
	entry.TargetRepo, entry.TargetRef, entry.Legacy = result.targetRepo, result.targetRef, result.legacy
	entry.HoldOwner = result.holdOwner
	if resolution := f.latestResolution(claim); resolution != nil {
		entry.LatestResolution = resolution.record.ID
	}
	approved, approval := f.approvedReportingArtifact(claim, performer)
	if approved == nil {
		return result, nil
	}
	entry.Approval = approval.record.ID
	entry.Candidate = approved.body.(*State).Body["commit"]
	if result.held {
		if release := f.releaseFor(request.record.ID, entry.Candidate, entry.Approval); release != nil {
			entry.Release = release.record.ID
		}
	}
	return result, approved
}

// completionStatus is the section-4 state table. An explicit report still
// waits on its requester and closes on ratification, which is the only way a
// no-artifact commitment ever closed. An artifact waits on review, then either
// on the hold owner's release or on the performer's landing. The single word
// this table replaced covered all of those at once and so named none of them.
func (f *foldState) completionStatus(entry *Commitment, result requestResult, completion *parsedRecord, requester, performer string) {
	if completion.definition.Lifecycle == LifecycleReport {
		entry.Status, entry.WaitingOn = "reported", requester
		if f.ratified(completion.record.ID) {
			entry.Status, entry.Terminal, entry.WaitingOn = "satisfied", "reported", ""
		}
		return
	}
	// An artifact cannot be ratified: its declared satisfier is none, so the
	// requester is not the waiting party. An independent approval has to name
	// the exact head, and then the performer signs the merge, so both moves
	// after this one are theirs unless a hold names somebody else.
	if f.ratifiedApproval(completion) == nil {
		entry.Status, entry.WaitingOn = "awaiting-review", performer
		return
	}
	if result.held && entry.Release == "" {
		entry.Status, entry.WaitingOn = "awaiting-authorization", result.holdOwner
		return
	}
	entry.Status, entry.WaitingOn = "awaiting-landing", performer
}

// markApprovedNotLanded records the audit fact relative to the destination the
// request named. It never means "absent from main": a receipt into some other
// ref is a real landing that discharged nothing here, and stays counted.
func (f *foldState) markApprovedNotLanded(entry *Commitment, result requestResult, approved *parsedRecord, mergedArtifacts map[string]*parsedRecord) {
	if approved == nil || !result.landing || entry.Status == "abandoned" {
		return
	}
	entry.ApprovedNotLanded = !f.dischargedBy(mergedArtifacts[approved.record.ID], result)
}

// latestCompletion returns the promise's live completion record. A sealed
// merge is terminal. Otherwise an explicit report keeps the authority it had
// before artifacts could report implementation work; an artifact is the
// report when the promise has no live explicit report.
// A lifecycle report remains the general form: it can close work that never
// reaches Git, once its requester ratifies it. For implementing work, the
// promisor's artifact already carries the exact head and the promise it
// fulfils, so filing a second report would duplicate the same assertion.
// latestCompletion finds what closes a claim. The claim is the promise that
// accepted the work, or — when the addressee reported directly without one —
// the request itself. Both are read the same way: a report resting on it, or
// that actor's artifact resting on it with a commit, which serves as the
// implementation report. Passing the claim rather than always a promise is
// what keeps the two shapes one rule instead of two that drift.
func (f *foldState) latestCompletion(claim *parsedRecord, performer string, mergedArtifacts map[string]*parsedRecord, landing bool) *parsedRecord {
	var report, artifact, approved, merged *parsedRecord
	candidates := append([]*parsedRecord(nil), f.directDependents(claim.record.ID, LifecycleReport)...)
	candidates = append(candidates, f.directDependents(claim.record.ID, LifecycleNone)...)
	for _, record := range candidates {
		state, ok := record.body.(*State)
		if !ok || record.definition == nil {
			continue
		}
		// A report belongs to the claim it was admitted against, not to whatever
		// the bases look like now. Reading it any other way lets a later
		// withdrawal or a later promise move a completion between commitments
		// after the fold has already decided where it lives.
		isReport := false
		if record.definition.Lifecycle == LifecycleReport {
			admitted, ok := f.admittedClaims[record.record.ID]
			isReport = ok && admitted.claim.record.ID == claim.record.ID
		}
		isArtifactReport := false
		if record.definition.Render == RenderArtifact && state.Body["commit"] != "" && record.record.Actor == performer {
			admitted, ok := f.admittedClaims[record.record.ID]
			isArtifactReport = ok && admitted.claim.record.ID == claim.record.ID
		}
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
		// Only a landing request reads this rung, and resolving an approval
		// means walking the artifact's own dependents, so it is not paid for
		// on every other commitment in the log.
		if landing && isArtifactReport && (approved == nil || record.index > approved.index) && f.ratifiedApproval(record) != nil {
			approved = record
		}
	}
	if merged != nil {
		return merged
	}
	// Section 5. On a request that owes a landing there is no report in this
	// list at all: a plain or verdict-shaped report was refused at admission
	// and a resolution report is evidence beside the commitment, so an
	// artifact can only be overtaken by a newer artifact or sealed by a
	// receipt. An approved head outranks a later unapproved one, because the
	// approval is what the landing obligation is about.
	if landing {
		if approved != nil {
			return approved
		}
		return artifact
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
