package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/generalbusiness-ai/gitseq/internal/intent"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/reviewguard"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// ReservedBodyFields are body field names an ordinary state write may never
// supply. They record how admission judged an act, so letting a caller write
// them directly would let the act describe its own admission. The guarded
// review path stamps three of them onto every verdict it builds;
// dead_basis_override is stamped by this builder when the author asks for the
// dead-basis escape.
var ReservedBodyFields = []string{"dead_basis_override", "review_path", "head_news_acknowledged", "review_frontier"}

// stateAdmission is one state write offered to Workroom admission: what it
// says, what it rests on, and whether the author asked for the explicit
// escapes. The guarded-review marker travels inside Body as the reserved
// review_path field, because that is where every reader can still see it.
type stateAdmission struct {
	Kind           workroom.Kind
	Body           map[string]string
	RestsOn        []string
	AllowDeadBasis bool
	// InternalStaleness says this body's stale and staleness fields were built
	// by a validated in-process path that sees more than these bases. It is
	// process-local, like the guarded-review marker, and never caller input.
	InternalStaleness bool
}

// AdmitState evaluates one state write against the verified workroom and
// returns the body to sign, amended only by the escapes admission itself
// records. Every surface that files a state passes through here once before
// signing, and once more at sequencing against the exact world the event
// would join. The signing path reads the same private verified world the
// sequencing path does — never the reader-side snapshot, whose checkpoint
// cadence and rollback witness are reader business — and a workroom this
// process cannot verify right now is skipped rather than refused: the
// judgement that gates the append is the one made at sequencing.
func (w *Workspace) AdmitState(ctx context.Context, admission stateAdmission) (map[string]string, error) {
	head, err := w.Store.Head(ctx, kernel.Ref(w.config.Genesis))
	if err != nil {
		return cloneBody(admission.Body), nil
	}
	snapshot, err := w.admissionWorld(ctx, head)
	if err != nil {
		return cloneBody(admission.Body), nil
	}
	return w.admitState(snapshot, admission, head)
}

// admitState is the one Workroom admission evaluation, over a projection the
// caller has already read. frontierCommit names the tip that projection was
// read at, which is what a guarded verdict's recorded frontier must equal.
func (w *Workspace) admitState(snapshot Snapshot, admission stateAdmission, frontierCommit string) (map[string]string, error) {
	body := cloneBody(admission.Body)
	if body == nil {
		body = make(map[string]string)
	}
	guardedReview := admission.Kind == workroom.KindReport && body["review_path"] == reviewguard.ReviewPath
	if err := refuseReservedFields(body, guardedReview); err != nil {
		return nil, err
	}
	if err := refuseUndefinedKind(snapshot.Vocabulary, admission.Kind); err != nil {
		return nil, err
	}
	if verdictShaped(admission.Kind, body) {
		if !guardedReview {
			return nil, errors.New("a report whose body.verdict or body.status is approved or changes-requested is a review verdict; file it with gs review or the MCP review tool")
		}
		if err := reviewguard.EvaluateVerdict(snapshot.Projection, body, admission.RestsOn, w.EventID(frontierCommit)); err != nil {
			return nil, err
		}
	}
	if err := w.judgeDeadBases(snapshot, admission, body, guardedReview); err != nil {
		return nil, err
	}
	return body, nil
}

// verdictShaped reports whether this state is a review verdict by shape: a
// report whose verdict or status field carries one of the two review words
// exactly.
func verdictShaped(kind workroom.Kind, body map[string]string) bool {
	return kind == workroom.KindReport &&
		(reviewguard.IsVerdictWord(body["verdict"]) || reviewguard.IsVerdictWord(body["status"]))
}

// refuseClientReservedFields rejects reserved admission fields supplied as
// caller input on an unguarded surface. The guarded review path stamps its own
// fields onto the body it builds, so this check runs only for acts that do not
// carry that marker.
func refuseClientReservedFields(body map[string]string) error {
	for _, field := range ReservedBodyFields {
		if value, present := body[field]; present {
			return fmt.Errorf("body.%s=%q is a reserved admission field and cannot be supplied by this write", field, value)
		}
	}
	return nil
}

// refuseReservedFields rejects a body that tries to speak for admission. A
// write outside the guarded review path may supply none of the review fields
// at all; a guarded review carries the two its path stamps and nothing else.
// dead_basis_override is deliberately absent here: it is honoured wherever it
// is found, because it records an escape this builder itself writes and
// grants nothing on its own.
func refuseReservedFields(body map[string]string, guardedReview bool) error {
	for _, field := range ReservedBodyFields {
		value, present := body[field]
		if !present || field == "dead_basis_override" {
			continue
		}
		switch {
		case field == "review_path":
			if value != reviewguard.ReviewPath {
				return fmt.Errorf("body.review_path %q names no known review guard", value)
			}
			if !guardedReview {
				return errors.New("body.review_path marks a guarded review; only a report carrying a review verdict may claim it")
			}
		case guardedReview:
			continue
		default:
			return fmt.Errorf("body.%s=%q is a reserved admission field and cannot be supplied by this write", field, value)
		}
	}
	return nil
}

// refuseUndefinedKind closes the door the fold left open on purpose. An
// undefined kind used to append silently and project as undefined-kind, and
// authors were left believing commitments had been made that no rule could
// see. Now the mistake is refused before signing, with no override, and the
// error points at the route that does exist: a command-shaped kind names its
// dedicated command or tool, and any other absent kind lists the live
// vocabulary and the ratified kind-def that would establish it. Declared
// custom kinds remain exactly as valid as they were.
func refuseUndefinedKind(vocabulary workroom.Vocabulary, kind workroom.Kind) error {
	for _, definition := range vocabulary.Definitions {
		if definition.Name == kind {
			return nil
		}
	}
	switch kind {
	case "ratify", "supersede":
		return fmt.Errorf("state kind %q is not defined here and no override exists; ratifying and retiring use gs ratify and gs supersede, or the ratify and supersede tools", kind)
	case "review":
		return fmt.Errorf("state kind %q is not defined here and no override exists; filing a verdict uses gs review, or the review tool", kind)
	}
	names := make([]string, 0, len(vocabulary.Definitions))
	for _, definition := range vocabulary.Definitions {
		names = append(names, string(definition.Name))
	}
	defined := "no kinds are defined here"
	if len(names) != 0 {
		defined = "kinds defined here: " + strings.Join(names, ", ")
	}
	return fmt.Errorf("state kind %q is not defined in this workroom and no override exists. %s. A ratified kind-def must establish a new kind before state can use it", kind, defined)
}

// judgeDeadBases separates the two facts the projection keeps about a basis an
// act rests on. Retired means the cited record was itself withdrawn and
// nothing stands there to rest on, so the write is refused. Stale means the
// record still stands and something underneath it moved, which is a reason to
// re-read rather than a reason nothing may be said: the write is admitted and
// the staleness is recorded on the row it lands as.
//
// That is where gs merge already draws the line — it refuses a retired
// approval and writes the staleness of a merely stale one into the receipt it
// lands — and the write boundary now draws it in the same place and records it
// in the same shape.
//
// The recorded note belongs to admission, not to the caller. Two in-process
// paths build their own and keep it — a guarded review and an act carrying the
// internal-staleness marker — and every other write has the two fields written
// from what admission itself found.
//
// An effective supersession stays advisory: citing the retirement itself can
// be intentional. The escape for a retired basis is explicit and recorded,
// never silent: asking for it signs body.dead_basis_override=true, and an act
// arriving with that signature already on it honours it. Neither the refusal,
// the override, nor the recorded note removes staleness or grants authority;
// existing standing and staleness judgements continue unchanged.
func (w *Workspace) judgeDeadBases(snapshot Snapshot, admission stateAdmission, body map[string]string, guardedReview bool) error {
	var retired, stale []string
	for basis, class := range workroom.DeadBases(snapshot.Projection, admission.RestsOn) {
		switch class {
		case workroom.DeadBasisRetired:
			retired = append(retired, basis)
		case workroom.DeadBasisStale:
			stale = append(stale, basis)
		}
	}
	if len(retired) != 0 && body["dead_basis_override"] != "true" {
		sort.Strings(retired)
		if !admission.AllowDeadBasis {
			return fmt.Errorf("this state rests on %d already-retired basis(es): %s; rerun with --allow-dead-basis, or allow_dead_basis=true, to sign body.dead_basis_override=true recording that you saw them",
				len(retired), strings.Join(retired, ", "))
		}
		body["dead_basis_override"] = "true"
	}
	if guardedReview || admission.InternalStaleness {
		// Both notes are built in process by a path that has already been
		// validated and can see further than these bases: a verdict covers the
		// artifact, promise and request it stands on, and a merge receipt
		// covers the reviewed artifact, which is not a basis of the receipt at
		// all. Recomputing here would replace an accurate account with a
		// narrower one.
		return nil
	}
	if declaresStalenessFields(snapshot.Vocabulary, admission.Kind) {
		// The kind's own schema claims these names for something else. A
		// kind-def carries `staleness` to declare how staleness propagates
		// through the kind it defines, and taking that word away from it would
		// make every vocabulary declaration malformed. The check reads the
		// room's live vocabulary rather than naming kind-def, so a room that
		// declares its own kind using either word keeps it too.
		return nil
	}
	recordStaleness(snapshot.Projection, stale, body)
	return nil
}

// declaresStalenessFields reports whether this kind's own definition claims
// `stale` or `staleness` as a body field of its schema. Where it does, those
// names belong to the kind and admission must not speak over them.
func declaresStalenessFields(vocabulary workroom.Vocabulary, kind workroom.Kind) bool {
	for _, definition := range vocabulary.Definitions {
		if definition.Name != kind {
			continue
		}
		for _, field := range definition.Fields {
			if field.Name == "stale" || field.Name == "staleness" {
				return true
			}
		}
	}
	return false
}

// recordStaleness makes stale and staleness admission's own words about an
// ordinary state write. It first removes whatever the caller sent under those
// two names, then, if any admitted basis had already gone stale, writes
// body.stale=true and a one-line body.staleness note naming the stale bases,
// whether what moved was the world they describe, and the retired acts
// underneath them. It is the canonical note reviewguard builds for a verdict
// and gs merge builds for a receipt, so a reader meets one wording everywhere.
//
// The removal comes first and is unconditional, so the fields say what
// admission found and nothing else. A caller who supplies them cannot preserve
// them past this point, cannot forge staleness on living ground, and — the
// defect this closes — cannot suppress the real note by sending any non-empty
// string of their own.
func recordStaleness(projection workroom.Projection, bases []string, body map[string]string) {
	delete(body, "stale")
	delete(body, "staleness")
	if len(bases) == 0 {
		return
	}
	world := make(map[string]bool, len(bases))
	for _, statement := range projection.Statements {
		if statement.DescribesSupersededWorld {
			world[statement.Event] = true
		}
	}
	for _, artifact := range projection.Artifacts {
		if artifact.DescribesSupersededWorld {
			world[artifact.Event] = true
		}
	}
	sort.Strings(bases)
	parts := make([]reviewguard.Part, 0, len(bases))
	for _, basis := range bases {
		parts = append(parts, reviewguard.Part{Name: basis, Event: basis, Stale: true, World: world[basis]})
	}
	note := reviewguard.StalenessNote(projection, parts)
	if note == "" {
		return
	}
	body["stale"] = "true"
	body["staleness"] = note
}

// admitApplication is the post-dedup half of admission, scheduled by the
// kernel for every genuinely new submission against the exact pre-sequence
// frontier. Exact retries replayed before this hook ever ran, so history is
// never re-judged; everything admitted anew is judged here, whatever surface
// filed it and whether it went through the resident or degraded locally. Acts
// whose schema carries no state payload are outside this policy.
func (w *Workspace) admitApplication(ctx context.Context, application kernel.Application) error {
	switch application.Intent.Schema {
	case workroom.SchemaStateLegacy, workroom.SchemaStateV1, workroom.SchemaState:
		decoded, err := workroom.Decode(application.Intent.Schema, application.Payload)
		if err != nil {
			return fmt.Errorf("admission decode: %w", err)
		}
		state, ok := decoded.(*workroom.State)
		if !ok {
			return nil
		}
		snapshot, err := w.admissionWorld(ctx, application.Head)
		if err != nil {
			return err
		}
		_, err = w.admitState(snapshot, stateAdmission{
			Kind: state.Kind, Body: state.Body, RestsOn: application.Intent.RestsOn,
		}, application.Head)
		return err
	case workroom.SchemaRetireUnclaimed:
		decoded, err := workroom.Decode(application.Intent.Schema, application.Payload)
		if err != nil {
			return fmt.Errorf("admission decode: %w", err)
		}
		guard := decoded.(*workroom.RetireIfUnclaimed)
		if err := w.RefuseCitedRetirement(ctx, guard.Target, guard.CitedOK); err != nil {
			return err
		}
		_, err = w.admissionWorldChecked(ctx, application.Head, func(folder *workroom.Folder) error {
			return folder.AdmitRetireIfUnclaimed(intent.ActorFingerprint(application.ActorKey), *guard, application.Intent.RestsOn)
		})
		return err
	case workroom.SchemaReassignRequest:
		decoded, err := workroom.Decode(application.Intent.Schema, application.Payload)
		if err != nil {
			return fmt.Errorf("admission decode: %w", err)
		}
		guard := decoded.(*workroom.ReassignIfUnclaimed)
		snapshot, err := w.admissionWorldChecked(ctx, application.Head, func(folder *workroom.Folder) error {
			return folder.AdmitReassignIfUnclaimed(intent.ActorFingerprint(application.ActorKey), *guard, application.Intent.RestsOn)
		})
		if err != nil {
			return err
		}
		_, err = w.admitState(snapshot, stateAdmission{
			Kind: workroom.KindRequest, Body: guard.Body, RestsOn: application.Intent.RestsOn,
		}, application.Head)
		return err
	default:
		return nil
	}
}

// admissionWorld returns the verified workroom exactly at head, judged from
// its own reader rather than the shared projection. The reader-side snapshot
// refuses a rewound or sibling world and persists a rollback witness; admission
// must instead judge the position the event would actually extend, whatever a
// reader would say about the journey there.
func (w *Workspace) admissionWorld(ctx context.Context, head string) (Snapshot, error) {
	return w.admissionWorldChecked(ctx, head, nil)
}

func (w *Workspace) admissionWorldChecked(ctx context.Context, head string, check func(*workroom.Folder) error) (Snapshot, error) {
	w.admissionMu.Lock()
	defer w.admissionMu.Unlock()
	if w.admissionReader == nil {
		w.admissionReader = kernel.NewReader(w.Store)
	}
	loaded, err := w.admissionReader.Load(ctx, w.config.Genesis)
	if err != nil {
		return Snapshot{}, fmt.Errorf("admission verified the log: %w", err)
	}
	if !loaded.Full && w.admissionFolder == nil {
		// The reader advanced incrementally but this workspace has no fold to
		// advance with it. A fresh reader forces a full authenticated load.
		w.admissionReader = kernel.NewReader(w.Store)
		loaded, err = w.admissionReader.Load(ctx, w.config.Genesis)
		if err != nil {
			return Snapshot{}, fmt.Errorf("admission verified the log: %w", err)
		}
	}
	if loaded.Verification.Head != head {
		return Snapshot{}, fmt.Errorf("the workroom moved under admission: the event would extend %s while the log stands at %s; resubmit", head, loaded.Verification.Head)
	}
	folder := w.admissionFolder
	if folder == nil || loaded.Full {
		folder = workroom.NewFolder(nil)
		w.admissionFolder = folder
	}
	for _, event := range loaded.Events {
		folder.Append(w.record(event))
	}
	if check != nil {
		if err := check(folder); err != nil {
			return Snapshot{}, err
		}
	}
	return Snapshot{
		Genesis: w.config.Genesis, Head: loaded.Verification.Head, Depth: loaded.Verification.Depth,
		Projection: folder.Projection(), Vocabulary: folder.Vocabulary(),
	}, nil
}
