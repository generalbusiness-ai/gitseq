package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/generalbusiness-ai/gitseq/internal/eventref"
	"github.com/generalbusiness-ai/gitseq/internal/reviewguard"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// Guarded creation of one checkout for one governing record.
//
// What creation makes is a directory, a branch, a private record, a
// commit-message template and one attempt ref. It signs nothing and admits
// nothing, so it refuses only on what it checks itself, and none of what it
// makes is evidence: the record is a claim, the branch name is a claim, the
// attempt ref is a claim. Durable admission reads the durable record alone,
// and a flag that silences a local warning here cannot reach an authority
// check, because the two never meet.
//
// Every check has three possible determinations rather than two. It can pass,
// it can refuse on a determinate negative, or it can fail to establish
// anything at all — which is what an unreadable projection or an unreadable
// checkout listing produces. Unknown never proves a negative, so a check whose
// input was unavailable is reported as not established, warned about, and
// creation proceeds. It is never reported as passed, and never as failed.
//
// A fourth word is used for the two confirmable warnings, a settled governing
// commitment and a duplicate head. Both are determinate negatives that the
// adopted design says must not refuse outright, so they refuse without their
// confirmation flag and read `warned` with it. Four words, and each means
// exactly one thing for creation: `refused` always stops, `warned` never does,
// `passed` means the check looked and found nothing wrong, and
// `not established` means it could not look.
const (
	CheckPassed         = "passed"
	CheckRefused        = "refused"
	CheckWarned         = "warned"
	CheckNotEstablished = "not established"
)

// The checks, named once so the command, the tests and the documentation say
// the same words.
const (
	CheckGoverningRecord = "governing record"
	CheckRecordKind      = "record kind"
	CheckAddressee       = "addressee"
	CheckSettlement      = "settlement"
	CheckDuplicateHead   = "duplicate head"
	CheckRepository      = "repository"
	CheckCheckoutRoot    = "checkout root"
	CheckDestination     = "destination"
	CheckBranch          = "branch"
)

// CheckoutCheck is one eligibility question, its determination, and why.
type CheckoutCheck struct {
	Name    string `json:"check"`
	Outcome string `json:"outcome"`
	Detail  string `json:"detail,omitempty"`
}

func (c CheckoutCheck) String() string {
	if c.Detail == "" {
		return c.Name + ": " + c.Outcome
	}
	return c.Name + ": " + c.Outcome + " (" + c.Detail + ")"
}

// CheckoutIntent is one request to create one checkout.
type CheckoutIntent struct {
	// Governing is the full canonical identifier the selector resolved to.
	// Only a full identifier reaches here: a record number and a hash
	// fragment are ways of typing one at the command boundary, and the
	// resolver there refuses what it cannot resolve.
	Governing string
	// Actor is the acting actor's name. It is read for the addressee check
	// and for nothing else; creation reads no private key and signs nothing.
	Actor string
	Path  string
	// Branch and Start name the branch to create and the commit it starts at.
	// Neither has a default: no configured checkout root exists in this
	// source and no branch naming convention is invented here, so the caller
	// names both rather than inheriting a policy nobody wrote down.
	Branch string
	Start  string
	// Snapshot is the verified projection eligibility is read from, and
	// Unreadable is the failure of the read that would have produced it.
	// Exactly one of them is meaningful.
	Snapshot   Snapshot
	Unreadable error
	// SettledOK and DuplicateOK are the two local confirmations. Neither is
	// ever signed, neither becomes a body field or a basis, and neither is an
	// input to attempt allocation.
	SettledOK   bool
	DuplicateOK bool
}

// CheckoutResult is what creation determined and, when it created anything,
// what it made.
type CheckoutResult struct {
	Checks    []CheckoutCheck `json:"checks"`
	Created   bool            `json:"created"`
	Governing string          `json:"governing"`
	Path      string          `json:"path,omitempty"`
	Branch    string          `json:"branch,omitempty"`
	Head      string          `json:"head,omitempty"`

	Record       CheckoutRecord  `json:"record,omitempty"`
	RecordPath   string          `json:"record_path,omitempty"`
	TemplatePath string          `json:"template_path,omitempty"`
	Trailer      string          `json:"trailer,omitempty"`
	Attempt      CheckoutAttempt `json:"attempt,omitempty"`
}

// Report is the checks in the order they were asked, one line each.
func (r CheckoutResult) Report() []string {
	lines := make([]string, 0, len(r.Checks))
	for _, check := range r.Checks {
		lines = append(lines, check.String())
	}
	return lines
}

// Refusals names every check that stopped creation.
func (r CheckoutResult) Refusals() []string {
	var refused []string
	for _, check := range r.Checks {
		if check.Outcome == CheckRefused {
			refused = append(refused, check.String())
		}
	}
	return refused
}

// CreateCheckout runs every check, and creates only if none refused.
//
// All the checks run even after one refuses. They are reads, they cost
// nothing beside a refusal that has already happened, and a caller told about
// one problem at a time fixes them one at a time. Nothing is written to the
// filesystem or to a ref until every one of them has been asked.
func (w *Workspace) CreateCheckout(ctx context.Context, intent CheckoutIntent) (CheckoutResult, error) {
	result := CheckoutResult{Governing: intent.Governing, Path: intent.Path, Branch: intent.Branch}
	add := func(name, outcome, detail string) {
		result.Checks = append(result.Checks, CheckoutCheck{Name: name, Outcome: outcome, Detail: detail})
	}

	fingerprint, err := w.actingFingerprint(intent.Actor)
	if err != nil {
		return result, err
	}
	start, err := w.resolveCommit(ctx, intent.Start)
	if err != nil {
		return result, fmt.Errorf("resolve --start %q: %w", intent.Start, err)
	}
	result.Head = start

	w.checkGoverningRecord(intent, add)
	w.checkEligibility(intent, fingerprint, add)
	// The checkout listing is S1's, taken through the same eight-second
	// cached read the worktrees endpoint uses. It reports duplicate heads
	// among the checkouts that exist; a checkout that does not exist yet is
	// not among them, so the head this creation would sit at is compared
	// against that same captured listing rather than against a second
	// listing taken here.
	views, listErr := w.LocalWorktrees(ctx)
	w.checkDuplicateHead(intent, start, views, listErr, add)
	w.checkDestinationAndRepository(intent, views, listErr, add)
	w.checkCheckoutRoot(ctx, intent, add)
	w.checkBranch(ctx, intent, add)

	if refusals := result.Refusals(); len(refusals) > 0 {
		return result, fmt.Errorf("refused before writing anything:\n  %s", strings.Join(refusals, "\n  "))
	}
	return w.createCheckout(ctx, intent, start, result)
}

// actingFingerprint is the acting actor's event-signature fingerprint, which
// is what a request's addressee field holds. No private key is read: creation
// signs nothing.
func (w *Workspace) actingFingerprint(name string) (string, error) {
	if name == "" {
		return "", errors.New("no acting actor; creation needs one to ask whether a request is addressed to it")
	}
	actor, err := w.ResolveActor(name)
	if err != nil {
		return "", err
	}
	return actor.Fingerprint, nil
}

// resolveCommit reads one revision through the closed Git environment, so
// that the repository this command was pointed at is the repository the
// answer comes from.
func (w *Workspace) resolveCommit(ctx context.Context, revision string) (string, error) {
	if revision == "" {
		return "", errors.New("no revision named")
	}
	output, err := repositoryLocalGit(ctx, w.Repo, "rev-parse", "--verify", "--end-of-options", revision+"^{commit}").Output()
	if err != nil {
		return "", fmt.Errorf("%q names no commit in this repository", revision)
	}
	resolved := strings.TrimSpace(string(output))
	if !exactObjectID(resolved) {
		return "", fmt.Errorf("%q resolved to %q, which is not a full object name", revision, resolved)
	}
	return resolved, nil
}

// checkGoverningRecord classifies the identifier without reading any log. A
// canonical identifier of another workroom is a determinate negative even
// with no projection at all: this repository cannot verify it, and stamping a
// checkout with it would put another room's event in this room's record.
func (w *Workspace) checkGoverningRecord(intent CheckoutIntent, add func(name, outcome, detail string)) {
	room := eventref.Room{Genesis: w.config.Genesis, ObjectFormat: w.config.ObjectFormat}
	switch room.Form(intent.Governing) {
	case eventref.FormCanonical:
		add(CheckGoverningRecord, CheckPassed, "a canonical identifier of this workroom")
	case eventref.FormExternal:
		add(CheckGoverningRecord, CheckRefused, fmt.Sprintf("%s names an event of another workroom, which this repository cannot verify", intent.Governing))
	default:
		add(CheckGoverningRecord, CheckRefused, fmt.Sprintf("%q is not a canonical event identifier", intent.Governing))
	}
}

// checkEligibility asks the three questions the durable record answers: is
// this record a kind creation may be governed by, is it addressed to the
// acting actor, and has the commitment it names already settled.
//
// With no readable projection none of the three has an answer. They are
// reported as not established rather than as passed or as failed, because
// unknown is not a negative and is not a consent. What creation then makes
// grants nothing: the record it writes is evidence and not permission, and
// durable admission reads the durable record itself, so nothing an unreadable
// projection let through can reach an authority check.
func (w *Workspace) checkEligibility(intent CheckoutIntent, fingerprint string, add func(name, outcome, detail string)) {
	if intent.Unreadable != nil {
		reason := "could not read the durable record set: " + intent.Unreadable.Error()
		add(CheckRecordKind, CheckNotEstablished, reason)
		add(CheckAddressee, CheckNotEstablished, reason)
		add(CheckSettlement, CheckNotEstablished, reason)
		return
	}
	projection := intent.Snapshot.Projection
	room := eventref.Room{Genesis: w.config.Genesis, ObjectFormat: w.config.ObjectFormat}
	set := eventref.FromProjection(room, projection)
	if !set.Resolvable(intent.Governing) {
		add(CheckRecordKind, CheckRefused, fmt.Sprintf("%s names no durable record of this workroom", intent.Governing))
		add(CheckAddressee, CheckRefused, "there is no record here to be addressed to anybody")
		add(CheckSettlement, CheckRefused, "there is no commitment here to be settled or open")
		return
	}

	var statement workroom.Statement
	for _, candidate := range projection.Statements {
		if candidate.Event == intent.Governing {
			statement = candidate
			break
		}
	}
	var commitment workroom.Commitment
	held := false
	for _, candidate := range projection.Commitments {
		if candidate.Request == intent.Governing {
			commitment, held = candidate, true
			break
		}
	}

	addressed := commitment.AddressedTo
	if addressed == "" {
		addressed = statement.Body["to"]
	}
	isRequest := statement.Kind == workroom.KindRequest
	// The assigned limb is tried first. A request addressed to the acting
	// actor is an assignment, and an assignment that has closed is a dead
	// lane whatever else the record may also serve as. The adopted-decision
	// limb is the other shape AGENTS.md names: self-initiated work files no
	// request, so the record its commits rest on is the decision that
	// authorised it, recognised by the review guard's own rule rather than
	// by a private copy of it.
	assigned := isRequest && addressed != "" && addressed == fingerprint
	adopted := reviewguard.AdoptedDecision(projection, intent.Governing) == nil

	switch {
	case assigned:
		add(CheckRecordKind, CheckPassed, "a request addressed to the acting actor")
		add(CheckAddressee, CheckPassed, intent.Actor)
		w.checkSettlement(intent, commitment, held, add)
	case adopted:
		add(CheckRecordKind, CheckPassed, "an adopted decision, which is how self-initiated work is named")
		add(CheckAddressee, CheckPassed, "an adopted decision is authority rather than an assignment, so it addresses nobody")
		add(CheckSettlement, CheckPassed, "an adopted decision names no assignment for this checkout to have outlived")
	case isRequest:
		add(CheckRecordKind, CheckPassed, "a request")
		add(CheckAddressee, CheckRefused, fmt.Sprintf("%s is addressed to %q, not to %s (%s)", intent.Governing, addressed, intent.Actor, fingerprint))
		w.checkSettlement(intent, commitment, held, add)
	default:
		add(CheckRecordKind, CheckRefused, fmt.Sprintf("%s is a %s statement, which is neither a request nor an adopted decision", intent.Governing, statementKindName(statement)))
		add(CheckAddressee, CheckRefused, "a record that is not a request addresses nobody")
		add(CheckSettlement, CheckRefused, "a record that is not a request names no commitment")
	}
}

func statementKindName(statement workroom.Statement) string {
	if statement.Kind == "" {
		return "an unrecognised"
	}
	return string(statement.Kind)
}

// checkSettlement reads the commitment's own Status, which is the settlement
// field. Stale is a separate qualifier beside it and is not a settlement: a
// stale request is one whose reasoning moved, and its work is still owed. The
// vocabulary is the one the merge plan reads, where a status word this
// version has never heard of counts as settled and therefore warns — the safe
// direction here, because the consequence is a warning and a flag rather than
// a refusal to act, the opposite of the direction a deletion decision needs.
func (w *Workspace) checkSettlement(intent CheckoutIntent, commitment workroom.Commitment, held bool, add func(name, outcome, detail string)) {
	if !held {
		add(CheckSettlement, CheckPassed, "no commitment names this record")
		return
	}
	if workroom.UnsettledCommitment(commitment.Status) {
		detail := commitment.Status
		if commitment.Stale {
			// Staleness is recorded and never gates creation.
			detail += "; the record is stale, which is not settlement"
		}
		add(CheckSettlement, CheckPassed, detail)
		return
	}
	detail := fmt.Sprintf("settled: %s, by %s", commitment.Status, settlingEvent(commitment))
	if intent.SettledOK {
		add(CheckSettlement, CheckWarned, detail+"; confirmed by --settled-ok")
		return
	}
	add(CheckSettlement, CheckRefused, detail+"; pass --settled-ok to create on a settled lane anyway")
}

// settlingEvent names the event that closed a commitment, so the warning
// points at a record a person can go and read rather than only at a word.
func settlingEvent(commitment workroom.Commitment) string {
	switch {
	case commitment.Report != "":
		return "report " + commitment.Report
	case commitment.LandingReceipt != "":
		return "landing receipt " + commitment.LandingReceipt
	case commitment.SuccessorRequest != "":
		return "successor request " + commitment.SuccessorRequest
	case commitment.Terminal != "":
		return "a " + commitment.Terminal + " ending this projection names no event for"
	default:
		return "an ending this projection names no event for"
	}
}

// checkDuplicateHead warns when the head this checkout would sit at is
// already held by another checkout of this repository. A second checkout at
// one head is ordinary — a recut, a review tree, a checkout of the target
// ref's own head — so it is warned and confirmed, never refused outright.
//
// The checkout this command was run in is not counted. Every ordinary
// creation starts a branch where the caller is standing, so that checkout
// shares the new head by construction; counting it would make the warning
// fire on every creation and teach a reader to pass the confirmation flag
// without reading it, which is the failure a warning exists to avoid. The
// tree being cut from is not a duplicate of the tree being cut.
func (w *Workspace) checkDuplicateHead(intent CheckoutIntent, start string, views LocalRepo, listErr error, add func(name, outcome, detail string)) {
	if listErr != nil {
		add(CheckDuplicateHead, CheckNotEstablished, "could not read this repository's checkout listing: "+listErr.Error())
		return
	}
	var sharing []string
	for _, view := range views.Worktrees {
		if view.Head == start && !view.Current {
			sharing = append(sharing, view.Checkout)
		}
	}
	if len(sharing) == 0 {
		add(CheckDuplicateHead, CheckPassed, "no other checkout of this repository is at "+start)
		return
	}
	detail := fmt.Sprintf("%s is already held by %s", start, strings.Join(sharing, ", "))
	if intent.DuplicateOK {
		add(CheckDuplicateHead, CheckWarned, detail+"; confirmed by --duplicate-ok")
		return
	}
	add(CheckDuplicateHead, CheckRefused, detail+"; pass --duplicate-ok to create a second checkout at that head")
}

// checkDestinationAndRepository asks the two questions the filesystem
// answers, and both are determinate whatever the projection is doing.
//
// The destination question is whether this path is a safe place to make a
// checkout, judged on its own terms rather than against the configured root,
// which is a separate check with a separate answer. What is refused here is a
// path that is not absolute-resolvable, one whose final component is already
// a symbolic link, and one that already exists as anything but an empty
// directory.
//
// The repository question is whether this destination belongs to this
// workroom's repository. Two things could make it not: a path inside the
// repository's own Git directory, which would put a working tree inside the
// machinery that manages it, and a path inside a checkout this repository
// already has. Both are compared on resolved paths and by path elements, so a
// symbolic link cannot carry a destination into either and "/a/bc" is not
// inside "/a/b".
func (w *Workspace) checkDestinationAndRepository(intent CheckoutIntent, views LocalRepo, listErr error, add func(name, outcome, detail string)) {
	if strings.TrimSpace(intent.Path) == "" {
		add(CheckDestination, CheckRefused, "no destination named")
		add(CheckRepository, CheckRefused, "no destination to place in a repository")
		return
	}
	absolute, err := filepath.Abs(intent.Path)
	if err != nil {
		add(CheckDestination, CheckRefused, fmt.Sprintf("%q cannot be resolved to an absolute path: %v", intent.Path, err))
		add(CheckRepository, CheckRefused, "no resolvable destination to place in a repository")
		return
	}
	switch {
	case isSymbolicLink(absolute):
		add(CheckDestination, CheckRefused, absolute+" is a symbolic link; the name a person would remove and the directory they would remove are two different things")
	case existsAndIsNotAnEmptyDirectory(absolute):
		add(CheckDestination, CheckRefused, absolute+" already exists and is not an empty directory")
	default:
		add(CheckDestination, CheckPassed, absolute)
	}

	resolved := resolvePathForContainment(absolute)
	gitDir, commonDir := resolvePathForContainment(w.GitDir), resolvePathForContainment(w.CommonDir)
	if withinRoot(gitDir, resolved) || withinRoot(commonDir, resolved) {
		add(CheckRepository, CheckRefused, absolute+" is inside this repository's Git directory, which is not a place a working tree may go")
		return
	}
	// A destination this repository does not own is not something the
	// listing can say anything about, so the Git-directory refusal above is
	// asked first and still fires. What the listing adds is whether the
	// destination falls inside a checkout this repository already has.
	if listErr != nil {
		add(CheckRepository, CheckNotEstablished, "the Git directory containment held, but this repository's checkout listing could not be read: "+listErr.Error())
		return
	}
	for _, view := range views.Worktrees {
		tree := resolvePathForContainment(view.resolved)
		if tree != "" && withinRoot(tree, resolved) {
			add(CheckRepository, CheckRefused, fmt.Sprintf("%s is inside the existing checkout %s", absolute, view.Checkout))
			return
		}
	}
	add(CheckRepository, CheckPassed, "this workroom's repository, whose common Git directory is "+w.CommonDir)
}

// checkCheckoutRoot refuses a destination outside the root this repository
// configures.
//
// It refuses only against a root somebody chose. With nothing configured
// there is no boundary for a destination to be outside of, and the fallback
// layer 1 protects with is not a boundary anybody asked for: refusing against
// it would refuse ordinary destinations for the sake of a value nobody wrote
// down. So the check reports that it could not be established, names the key
// that would establish it, and creation proceeds.
func (w *Workspace) checkCheckoutRoot(ctx context.Context, intent CheckoutIntent, add func(name, outcome, detail string)) {
	absolute, err := filepath.Abs(strings.TrimSpace(intent.Path))
	if err != nil || strings.TrimSpace(intent.Path) == "" {
		add(CheckCheckoutRoot, CheckRefused, "no resolvable destination to place under a checkout root")
		return
	}
	root, configured := w.checkoutRoot(ctx, w.repositoryTop(ctx))
	if !configured {
		add(CheckCheckoutRoot, CheckNotEstablished, fmt.Sprintf(
			"this repository configures no %s, so there is no boundary for a destination to be outside of; %s is what protects existing checkouts and is not a value anybody chose",
			checkoutRootKey, root))
		return
	}
	if !withinRoot(resolvePathForContainment(root), resolvePathForContainment(absolute)) {
		add(CheckCheckoutRoot, CheckRefused, fmt.Sprintf("%s is outside the configured checkout root %s", absolute, root))
		return
	}
	add(CheckCheckoutRoot, CheckPassed, "inside the configured checkout root "+root)
}

// existsAndIsNotAnEmptyDirectory reports whether something is already at this
// path that creation would have to disturb. An empty directory is admitted
// because `git worktree add` accepts one; anything else is refused.
func existsAndIsNotAnEmptyDirectory(path string) bool {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	if err != nil || !info.IsDir() {
		return true
	}
	entries, err := os.ReadDir(path)
	return err != nil || len(entries) > 0
}

// checkBranch refuses a branch name this repository already has. Creation
// makes a branch; attaching a new checkout to a branch somebody else is
// working on is a different act, and this command does not offer it.
func (w *Workspace) checkBranch(ctx context.Context, intent CheckoutIntent, add func(name, outcome, detail string)) {
	name := strings.TrimSpace(intent.Branch)
	if name == "" {
		add(CheckBranch, CheckRefused, "no branch named")
		return
	}
	ref := "refs/heads/" + name
	if err := repositoryLocalGit(ctx, w.Repo, "check-ref-format", ref).Run(); err != nil {
		add(CheckBranch, CheckRefused, fmt.Sprintf("%q is not a valid branch name", name))
		return
	}
	value, present, err := w.Store.RefValue(ctx, ref)
	if err != nil {
		add(CheckBranch, CheckRefused, fmt.Sprintf("could not read %s: %v", ref, err))
		return
	}
	if present {
		add(CheckBranch, CheckRefused, fmt.Sprintf("%s already exists at %s; creation makes a branch rather than taking one over", ref, value))
		return
	}
	add(CheckBranch, CheckPassed, ref+" does not exist yet")
}

// createCheckout does the writing, once every check has been asked.
//
// The order is the order a reader has to be able to trust. The checkout and
// its branch come first, because they are what the rest describes. The
// attempt ref is allocated next, so the record can name it. The record and
// its template are written together under one lock, and then read back, so
// that what is reported is what the checkout carries rather than what this
// process meant to write. The attempt is finally pointed at the head the
// checkout actually holds, read from the created checkout rather than assumed
// from the revision the caller typed; when the two agree, which is the
// ordinary case, that writes nothing at all.
//
// Nothing here removes anything on failure. A creation that stops half-way
// leaves what it made, and says what it made. Removal is a separate
// deliberate act throughout this design, and a cleanup path here would be the
// one place in it that deletes a person's directory on an error nobody chose.
func (w *Workspace) createCheckout(ctx context.Context, intent CheckoutIntent, start string, result CheckoutResult) (CheckoutResult, error) {
	destination, err := filepath.Abs(intent.Path)
	if err != nil {
		return result, err
	}
	// The one command in this package that changes the repository takes the
	// same closed Git environment as the reads, for the same reason: ambient
	// routing must not decide which repository is written.
	if output, err := repositoryLocalGit(ctx, w.Repo, "worktree", "add", "-b", intent.Branch, destination, start).CombinedOutput(); err != nil {
		return result, fmt.Errorf("git worktree add: %w: %s", err, strings.TrimSpace(string(output)))
	}
	result.Created, result.Path, result.Head = true, destination, start

	attempt, err := w.ClaimCheckoutAttempt(ctx, intent.Governing, start)
	if err != nil {
		return result, err
	}
	result.Attempt = attempt

	record := CheckoutRecord{
		Genesis:      w.config.Genesis,
		ObjectFormat: w.config.ObjectFormat,
		Governing:    intent.Governing,
		Branch:       intent.Branch,
		AttemptRef:   attempt.Ref,
		Attempt:      attempt.Number,
	}
	recordPath, templatePath, err := w.writeCheckoutRecord(ctx, destination, record)
	if err != nil {
		return result, err
	}
	stored, err := w.readCheckoutRecord(ctx, destination)
	if err != nil {
		return result, err
	}
	result.Record, result.RecordPath, result.TemplatePath = stored, recordPath, templatePath
	result.Trailer = stored.Trailer()

	if head, err := w.checkoutHead(ctx, destination); err == nil {
		pointed, err := w.PointCheckoutAttempt(ctx, attempt, head)
		if err != nil {
			return result, err
		}
		result.Attempt, result.Head = pointed, head
	}

	return result, nil
}

// checkoutHead reads the commit a created checkout actually holds.
func (w *Workspace) checkoutHead(ctx context.Context, checkout string) (string, error) {
	output, err := repositoryLocalGit(ctx, checkout, "rev-parse", "--verify", "HEAD").Output()
	if err != nil {
		return "", err
	}
	head := strings.TrimSpace(string(output))
	if !exactObjectID(head) {
		return "", fmt.Errorf("%s reports head %q, which is not a full object name", checkout, head)
	}
	return head, nil
}
