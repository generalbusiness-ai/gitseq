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
// dead-basis escape, and StaleBasesField whenever it admits a stale basis.
var ReservedBodyFields = []string{"dead_basis_override", "review_path", "head_news_acknowledged", "review_frontier", StaleBasesField}

// StaleBasesField is where admission records that this state rests on a basis
// something under which had already been withdrawn. The value is the same
// one-line staleness note gs merge writes into a receipt. It is testimony, not
// authority: it neither repairs the staleness nor changes what the act may do.
const StaleBasesField = "stale_bases"

// stateAdmission is one state write offered to Workroom admission: what it
// says, what it rests on, and whether the author asked for the explicit
// escapes. The guarded-review marker travels inside Body as the reserved
// review_path field, because that is where every reader can still see it.
type stateAdmission struct {
	Kind           workroom.Kind
	Body           map[string]string
	RestsOn        []string
	AllowDeadBasis bool
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
	if err := w.refuseDeadBases(snapshot, admission.RestsOn, body, admission.AllowDeadBasis); err != nil {
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
// dead_basis_override and stale_bases are deliberately absent here: both are
// honoured wherever they are found, because this builder writes them itself
// and re-judges the same body at sequencing, and neither grants anything on
// its own. Caller input carrying either is refused earlier, by
// refuseClientReservedFields.
func refuseReservedFields(body map[string]string, guardedReview bool) error {
	for _, field := range ReservedBodyFields {
		value, present := body[field]
		if !present || field == "dead_basis_override" || field == StaleBasesField {
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

// refuseDeadBases judges the bases a state rests on the way gs merge judges
// the bases of an approval. A retired basis is withdrawn ground and refuses:
// nothing stands there any more. A basis whose only problem is staleness
// stands exactly where it stood, so the state is admitted and the staleness is
// written onto it, in the one line a merge receipt would have carried. An
// effective supersession stays advisory: citing the retirement itself can be
// intentional.
//
// The escape for a retired basis is explicit and recorded, never silent:
// asking for it signs body.dead_basis_override=true, and an act arriving with
// that signature already on it honours it. Neither the refusal, the override,
// nor the recorded note removes staleness or grants authority; existing
// standing and staleness judgements continue unchanged.
func (w *Workspace) refuseDeadBases(snapshot Snapshot, restsOn []string, body map[string]string, allowed bool) error {
	dead := workroom.DeadBases(snapshot.Projection, restsOn)
	var blocking, stale []string
	for basis, class := range dead {
		switch class {
		case workroom.DeadBasisSupersede:
			continue
		case workroom.DeadBasisStale:
			stale = append(stale, basis)
		default:
			blocking = append(blocking, fmt.Sprintf("%s (%s)", basis, class))
		}
	}
	sort.Strings(stale)
	if note := staleBasisNote(snapshot.Projection, stale); note != "" {
		body[StaleBasesField] = note
	}
	if len(blocking) == 0 {
		return nil
	}
	if body["dead_basis_override"] == "true" {
		return nil
	}
	sort.Strings(blocking)
	if !allowed {
		return fmt.Errorf("this state rests on %d already-dead basis(es): %s; rerun with --allow-dead-basis, or allow_dead_basis=true, to sign body.dead_basis_override=true recording that you saw them",
			len(blocking), strings.Join(blocking, ", "))
	}
	body["dead_basis_override"] = "true"
	return nil
}

// staleBasisNote renders what moved under these bases, in the shape a merge
// receipt records: the stale bases themselves, whether any of them describes a
// superseded world, and the retired acts underneath them that a reader can act
// on. Bases arrive sorted, so the same world always produces the same line.
func staleBasisNote(projection workroom.Projection, bases []string) string {
	if len(bases) == 0 {
		return ""
	}
	parts := make([]reviewguard.Part, 0, len(bases))
	for _, basis := range bases {
		parts = append(parts, reviewguard.Part{
			Name: basis, Event: basis, Stale: true,
			World: describesSupersededWorld(projection, basis),
		})
	}
	return reviewguard.StalenessNote(projection, parts)
}

// describesSupersededWorld answers the narrower staleness fact about one
// event. A basis is a statement or an artifact; anything else the projection
// does not keep the fact about, and reports the plain staleness alone.
func describesSupersededWorld(projection workroom.Projection, event string) bool {
	for _, statement := range projection.Statements {
		if statement.Event == event {
			return statement.DescribesSupersededWorld
		}
	}
	for _, artifact := range projection.Artifacts {
		if artifact.Event == event {
			return artifact.DescribesSupersededWorld
		}
	}
	return false
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
