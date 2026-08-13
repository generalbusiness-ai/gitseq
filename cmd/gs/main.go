package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/residentclient"
	"github.com/generalbusiness-ai/gitseq/internal/service"
	"github.com/generalbusiness-ai/gitseq/internal/statusview"
	"github.com/generalbusiness-ai/gitseq/internal/telemetry"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

type values []string

func (v *values) String() string { return strings.Join(*v, ",") }
func (v *values) Set(value string) error {
	*v = append(*v, value)
	return nil
}

// actorEnvironment is how a concurrent instance is told which provisioned
// identity it is. Every signing command reads it when --as is absent.
const actorEnvironment = residentclient.ActorEnvironment

// signingActor resolves the identity an act is signed with. There is no
// default name: the default was a name that several concurrent instances
// shared, which made the log attribute to a group what one instance did.
func signingActor(flagValue string) (string, error) {
	return signingActorFrom("--as", flagValue)
}

// signingActorFrom names the flag in its refusal, because the flag that
// carries the identity is not the same one everywhere: `gs init` mints the
// operator with --operator, every later command signs with --as.
func signingActorFrom(flagName, flagValue string) (string, error) {
	return residentclient.ResolveActor(flagName, flagValue)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	// A person stops a long-running command with an interrupt, so that is the
	// path the command has to unwind on. Without this, serving died where it
	// stood and left the repository advertising a process that no longer
	// exists, sending every later client at a dead address. Cancelling the
	// whole command's context gives one stop path rather than a second one
	// that only the tests take.
	ctx, release := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer release()
	var err error
	switch os.Args[1] {
	case "init":
		err = initCommand(ctx, os.Args[2:])
	case "actor-add":
		err = actorAddCommand(ctx, os.Args[2:])
	case "role-grant":
		err = roleGrantCommand(ctx, os.Args[2:])
	case "role-revoke":
		err = roleRevokeCommand(ctx, os.Args[2:])
	case "actor-retire":
		err = actorRetireCommand(ctx, os.Args[2:])
	case "actors":
		err = actorsCommand(ctx, os.Args[2:])
	case "state":
		err = stateCommand(ctx, os.Args[2:])
	case "review":
		err = reviewCommand(ctx, os.Args[2:])
	case "merge":
		err = mergeCommand(ctx, os.Args[2:])
	case "ratify":
		err = ratifyCommand(ctx, os.Args[2:])
	case "supersede":
		err = supersedeCommand(ctx, os.Args[2:])
	case "batch":
		err = batchCommand(ctx, os.Args[2:])
	case "status":
		err = statusCommand(ctx, os.Args[2:])
	case "provenance":
		err = provenanceCommand(ctx, os.Args[2:])
	case "verify":
		err = verifyCommand(ctx, os.Args[2:])
	case "serve":
		err = serveCommand(ctx, os.Args[2:])
	case "attach":
		err = attachCommand(ctx, os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		release()
		fmt.Fprintln(os.Stderr, "gs:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: gs <init|actor-add|actor-retire|role-grant|role-revoke|actors|state|review|merge|ratify|supersede|batch|status|provenance|verify|serve|attach> [flags]")
	os.Exit(2)
}

func flags(name string, arguments []string) (*flag.FlagSet, *string) {
	set := flag.NewFlagSet(name, flag.ContinueOnError)
	repo := set.String("repo", ".", "ordinary Git repository")
	set.SetOutput(io.Discard)
	return set, repo
}

func initCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("init", arguments)
	// No default name. The sequencer key signs the genesis commit; the
	// operator seeded here signs their own acts, beginning with the bootstrap
	// roster statement that admits them and grants the operator role, which
	// carries ratifier with it. An identity holding ratifier from the first
	// event is not one to leave to a default: falling back to "operator" put
	// an identity nobody picked at the root of the log, and made "there is no
	// default identity" false at the one command where it matters most.
	operator := set.String("operator", "", "operator actor name; defaults to "+actorEnvironment)
	ceiling := set.Uint64("payload-ceiling", 1<<20, "inline payload ceiling")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	name, err := signingActorFrom("--operator", *operator)
	if err != nil {
		return err
	}
	workspace, seed, err := app.Init(ctx, *repo, name, *ceiling)
	if err != nil {
		return err
	}
	return printJSON(map[string]any{"genesis": workspace.Config.Genesis, "operator": workspace.Config.Actors[name], "seed": seed.ID})
}

func actorAddCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("actor-add", arguments)
	as := set.String("as", "", "operator actor")
	name := set.String("name", "", "new actor name")
	kind := set.String("kind", "agent", "principal kind: human, agent, or service")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	operator, err := signingActor(*as)
	if err != nil {
		return err
	}
	workspace, err := app.Open(ctx, *repo)
	if err != nil {
		return err
	}
	actor, records, err := workspace.AddActor(ctx, operator, *name, *kind)
	if err != nil {
		return err
	}
	return printJSON(map[string]any{"actor": actor, "events": []string{records[0].ID, records[1].ID}})
}

func actorRetireCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("actor-retire", arguments)
	as := set.String("as", "", "retiring actor")
	actor := set.String("actor", "", "actor name, @name, or fingerprint")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	if *actor == "" {
		return errors.New("actor-retire requires --actor")
	}
	retirer, err := signingActor(*as)
	if err != nil {
		return err
	}
	workspace, err := app.Open(ctx, *repo)
	if err != nil {
		return err
	}
	records, err := workspace.RetireActor(ctx, retirer, *actor)
	if err != nil {
		return err
	}
	events := make([]string, 0, len(records))
	for _, record := range records {
		events = append(events, record.ID)
	}
	return printJSON(map[string]any{"actor": *actor, "retired": true, "custody_removed": true, "events": events})
}

func roleGrantCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("role-grant", arguments)
	as := set.String("as", "", "granting actor")
	actor := set.String("actor", "", "actor name, @name, or fingerprint")
	role := set.String("role", "", "durable authority role")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	grantor, err := signingActor(*as)
	if err != nil {
		return err
	}
	workspace, err := app.Open(ctx, *repo)
	if err != nil {
		return err
	}
	records, err := workspace.GrantRole(ctx, grantor, *actor, *role)
	if err != nil {
		return err
	}
	events := make([]string, 0, len(records))
	for _, record := range records {
		events = append(events, record.ID)
	}
	return printJSON(map[string]any{"actor": *actor, "role": *role, "events": events})
}

func roleRevokeCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("role-revoke", arguments)
	as := set.String("as", "", "revoking actor")
	actor := set.String("actor", "", "actor name, @name, or fingerprint")
	role := set.String("role", "", "durable authority role")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	revoker, err := signingActor(*as)
	if err != nil {
		return err
	}
	workspace, err := app.Open(ctx, *repo)
	if err != nil {
		return err
	}
	records, err := workspace.RevokeRole(ctx, revoker, *actor, *role)
	if err != nil {
		return err
	}
	events := make([]string, 0, len(records))
	for _, record := range records {
		events = append(events, record.ID)
	}
	return printJSON(map[string]any{"actor": *actor, "role": *role, "events": events})
}

func actorsCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("actors", arguments)
	if err := set.Parse(arguments); err != nil {
		return err
	}
	workspace, err := app.Open(ctx, *repo)
	if err != nil {
		return err
	}
	actors, err := workspace.ActorViews(ctx)
	if err != nil {
		return err
	}
	return printJSON(actors)
}

func stateCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("state", arguments)
	as := set.String("as", "", "actor name")
	kind := set.String("kind", "", "statement kind")
	message := set.String("text", "", "statement text")
	serverURL := set.String("server", "", "resident sequencer URL")
	key := set.String("idempotency-key", "", "stable retry key")
	var bodyValues, rests, evidence values
	set.Var(&bodyValues, "body", "body key=value (repeatable)")
	set.Var(&rests, "rests-on", "causal event id (repeatable)")
	set.Var(&evidence, "evidence", "attachment name=path (repeatable)")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	actor, err := signingActor(*as)
	if err != nil {
		return err
	}
	workspace, err := app.Open(ctx, *repo)
	if err != nil {
		return err
	}
	body, err := pairs(bodyValues)
	if err != nil {
		return err
	}
	attachments, err := files(evidence)
	if err != nil {
		return err
	}
	record, err := submitAct(ctx, workspace, *serverURL, actor, app.Act{Verb: app.VerbState, Kind: workroom.Kind(*kind), Text: *message, Body: body, RestsOn: rests, Attachments: attachments, IdempotencyKey: *key})
	if err != nil {
		return err
	}
	fmt.Println(record.ID)
	return nil
}

// reviewBasis is everything a signed verdict has to name: the exact immutable
// head reviewed, the request the review answers, and whatever had moved
// underneath while the reviewer signed anyway.
type reviewBasis struct {
	Head      string
	Request   string
	Staleness string
}

type reviewValidator func(context.Context, *app.Workspace, string, string, string, string) (reviewBasis, error)

func reviewCommand(ctx context.Context, arguments []string) error {
	return reviewCommandWithValidator(ctx, arguments, validateReview)
}

func reviewCommandWithValidator(ctx context.Context, arguments []string, validate reviewValidator) error {
	set, repo := flags("review", arguments)
	as := set.String("as", "", "reviewer actor name")
	checkout := set.String("checkout", "", "checkout reviewed")
	artifact := set.String("artifact", "", "artifact event naming the reviewed head")
	promise := set.String("promise", "", "review promise event")
	verdict := set.String("verdict", "", "approved or changes-requested")
	message := set.String("text", "", "review report")
	serverURL := set.String("server", "", "resident sequencer URL")
	key := set.String("idempotency-key", "", "stable retry key")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	if set.NArg() != 0 {
		return errors.New("review takes no positional arguments")
	}
	if *checkout == "" || *artifact == "" || *promise == "" || *message == "" {
		return errors.New("review requires --checkout, --artifact, --promise, and --text")
	}
	reviewer, err := signingActor(*as)
	if err != nil {
		return err
	}
	if *verdict != "approved" && *verdict != "changes-requested" {
		return errors.New("review --verdict must be approved or changes-requested")
	}
	workspace, err := app.Open(ctx, *repo)
	if err != nil {
		return err
	}
	basis, err := validate(ctx, workspace, reviewer, *checkout, *artifact, *promise)
	if err != nil {
		return err
	}
	// Re-read immediately before signing. The verdict names the immutable
	// commit, so a later checkout movement cannot retarget it.
	if repeated, err := validate(ctx, workspace, reviewer, *checkout, *artifact, *promise); err != nil {
		return err
	} else if repeated != basis {
		return errors.New("review basis changed while validating")
	}
	body := map[string]string{"verdict": *verdict, "head": basis.Head, "artifact": *artifact}
	// A review over a moved world says so in its own words. Without this the
	// verdict would read as though nothing had moved, which is the lie the
	// refusal was there to prevent and a worse one than the refusal.
	if basis.Staleness != "" {
		body["stale"] = "true"
		body["staleness"] = basis.Staleness
	}
	record, err := submitAct(ctx, workspace, *serverURL, reviewer, app.Act{
		Verb: app.VerbState, Kind: workroom.KindReport, Text: *message,
		Body:    body,
		RestsOn: []string{*promise, basis.Request, *artifact}, IdempotencyKey: *key,
	})
	if err != nil {
		return err
	}
	fmt.Println(record.ID)
	return nil
}

func mergeCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("merge", arguments)
	as := set.String("as", "", "actor recording the merge receipt")
	checkout := set.String("checkout", "", "checkout receiving the merge")
	candidate := set.String("candidate", "", "full approved commit ID")
	approval := set.String("approval", "", "ratified approval report event")
	mergeText := set.String("text", "", "plain-language merge description and impact")
	serverURL := set.String("server", "", "resident sequencer URL")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	if set.NArg() != 0 {
		return errors.New("merge takes no positional arguments")
	}
	if *checkout == "" || *candidate == "" || *approval == "" {
		return errors.New("merge requires --checkout, --candidate, and --approval")
	}
	workspace, err := app.Open(ctx, *repo)
	if err != nil {
		return err
	}
	if err := validateMerge(ctx, workspace, *checkout, *candidate, *approval); err != nil {
		return err
	}
	if strings.TrimSpace(*mergeText) == "" {
		return errors.New("merge requires --text with a plain-language description and impact")
	}
	actor, err := signingActor(*as)
	if err != nil {
		return err
	}
	targetPreHead, err := git(ctx, *checkout, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return err
	}
	targetPreHead = strings.TrimSpace(targetPreHead)
	if _, err := git(ctx, *checkout, "merge-base", "--is-ancestor", *candidate, targetPreHead); err == nil {
		return errors.New("approved candidate is already contained in the target")
	}
	// Repeat the durable and local checks directly before invoking Git. The
	// merge argument remains the approved object ID, never a movable ref.
	if err := validateMerge(ctx, workspace, *checkout, *candidate, *approval); err != nil {
		return err
	}
	receiptRef := mergeReceiptRef(*approval)
	if _, err := git(ctx, *checkout, "update-ref", receiptRef, targetPreHead, ""); err != nil {
		return errors.New("approval is already reserved or used by another merge")
	}
	landed := false
	defer func() {
		if !landed {
			_, _ = git(context.Background(), *checkout, "update-ref", "-d", receiptRef, targetPreHead)
		}
	}()
	message := mergeReceiptMessage(*mergeText, *approval, *candidate, targetPreHead)
	if _, err := git(ctx, *checkout, "merge", "--no-ff", "-m", message, "--", *candidate); err != nil {
		return err
	}
	head, err := git(ctx, *checkout, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	head = strings.TrimSpace(head)
	receipt, err := readMergeReceipt(ctx, *checkout, head)
	if err != nil {
		return err
	}
	if receipt.Approval != *approval || receipt.Candidate != *candidate || receipt.TargetPreHead != targetPreHead || receipt.MergeHead != head {
		return errors.New("resulting merge commit does not carry the requested receipt")
	}
	if _, err := git(ctx, *checkout, "update-ref", receiptRef, head, targetPreHead); err != nil {
		return fmt.Errorf("publish merge receipt ref: %w", err)
	}
	landed = true
	if _, err := submitAct(ctx, workspace, *serverURL, actor, app.Act{
		Verb: app.VerbState, Kind: workroom.KindAssert, Text: "approved candidate merged",
		Body: map[string]string{
			"merge_approval": *approval, "merge_candidate": *candidate,
			"merge_target_pre_head": targetPreHead, "merge_head": head,
		},
		RestsOn: []string{*approval}, IdempotencyKey: mergeReceiptKey(*approval),
	}); err != nil {
		return fmt.Errorf("record merge receipt: %w", err)
	}
	fmt.Println(head)
	return nil
}

type mergeReceipt struct {
	Approval      string
	Candidate     string
	TargetPreHead string
	MergeHead     string
}

const (
	mergeApprovalTrailer  = "Gitseq-Approval: "
	mergeCandidateTrailer = "Gitseq-Candidate: "
	mergeTargetTrailer    = "Gitseq-Target-Pre-Head: "
)

func mergeReceiptKey(approval string) string {
	sum := sha256.Sum256([]byte(approval))
	return "merge-receipt-" + hex.EncodeToString(sum[:])
}

func mergeReceiptRef(approval string) string {
	return "refs/gitseq/merge-receipts/" + strings.TrimPrefix(mergeReceiptKey(approval), "merge-receipt-")
}

func mergeReceiptMessage(text, approval, candidate, targetPreHead string) string {
	return fmt.Sprintf("%s\n\n%s%s\n%s%s\n%s%s", strings.TrimSpace(text),
		mergeApprovalTrailer, approval, mergeCandidateTrailer, candidate, mergeTargetTrailer, targetPreHead)
}

func readMergeReceipt(ctx context.Context, checkout, head string) (mergeReceipt, error) {
	message, err := git(ctx, checkout, "show", "-s", "--format=%B", head)
	if err != nil {
		return mergeReceipt{}, err
	}
	receipt := mergeReceipt{MergeHead: head}
	for _, line := range strings.Split(message, "\n") {
		switch {
		case strings.HasPrefix(line, mergeApprovalTrailer):
			receipt.Approval = strings.TrimPrefix(line, mergeApprovalTrailer)
		case strings.HasPrefix(line, mergeCandidateTrailer):
			receipt.Candidate = strings.TrimPrefix(line, mergeCandidateTrailer)
		case strings.HasPrefix(line, mergeTargetTrailer):
			receipt.TargetPreHead = strings.TrimPrefix(line, mergeTargetTrailer)
		}
	}
	parents, err := git(ctx, checkout, "rev-list", "--parents", "-n", "1", head)
	if err != nil {
		return mergeReceipt{}, err
	}
	fields := strings.Fields(parents)
	if receipt.Approval == "" || receipt.Candidate == "" || receipt.TargetPreHead == "" || len(fields) != 3 ||
		fields[0] != head || fields[1] != receipt.TargetPreHead || fields[2] != receipt.Candidate {
		return mergeReceipt{}, errors.New("malformed merge receipt commit")
	}
	return receipt, nil
}

func existingGitMergeReceipt(ctx context.Context, checkout, approval string) (mergeReceipt, bool, error) {
	heads, err := git(ctx, checkout, "log", "--all", "--fixed-strings", "--grep="+mergeApprovalTrailer+approval, "--format=%H")
	if err != nil {
		return mergeReceipt{}, false, err
	}
	for _, head := range strings.Fields(heads) {
		receipt, err := readMergeReceipt(ctx, checkout, head)
		if err != nil {
			return mergeReceipt{}, false, err
		}
		if receipt.Approval == approval {
			return receipt, true, nil
		}
	}
	return mergeReceipt{}, false, nil
}

// validateReview admits a review of a world that has moved. Retirement and
// ineffectiveness still refuse: a withdrawn pointer names nothing to review,
// and neither does an act that never took force. Staleness does not refuse,
// because deciding whether the movement matters to this exact commit is the
// reviewer's work, and refusing it leaves the question permanently unanswered
// by the only party positioned to answer it.
func validateReview(ctx context.Context, workspace *app.Workspace, actorName, checkout, artifactEvent, promiseEvent string) (reviewBasis, error) {
	snapshot, err := workspace.Snapshot(ctx)
	if err != nil {
		return reviewBasis{}, err
	}
	projection := snapshot.Projection
	artifact, err := standingArtifact(projection, artifactEvent)
	if err != nil {
		return reviewBasis{}, err
	}
	// The same artifact read as a statement, for the one fact the artifact
	// projection does not carry: who signed it. Standing rather than live,
	// because a moved world is the reviewer's question to answer and refusing
	// it here would take back the latitude the gate above just granted.
	implementation, err := standingStatement(projection, artifactEvent, workroom.KindArtifact)
	if err != nil {
		return reviewBasis{}, fmt.Errorf("reviewed artifact: %w", err)
	}
	promise, err := standingStatement(projection, promiseEvent, workroom.KindPromise)
	if err != nil {
		return reviewBasis{}, err
	}
	actor, _, err := workspace.Actor(actorName)
	if err != nil {
		return reviewBasis{}, err
	}
	if promise.Actor != actor.Fingerprint {
		return reviewBasis{}, errors.New("review actor did not make the named promise")
	}
	// Independence is a property of fingerprints, not of names. Refusing here
	// keeps the self-signed verdict out of the log; the projection still
	// reports independence for verdicts written any other way.
	if implementation.Actor == actor.Fingerprint {
		return reviewBasis{}, errors.New("review actor signed the artifact under review; an independent reviewer must sign the verdict")
	}
	request, err := uniqueStandingBasis(projection, promiseEvent, workroom.KindRequest)
	if err != nil {
		return reviewBasis{}, fmt.Errorf("review promise: %w", err)
	}
	if err := validateCheckout(ctx, workspace.Repo, checkout, artifact.Commit, true); err != nil {
		return reviewBasis{}, err
	}
	return reviewBasis{Head: artifact.Commit, Request: request.Event, Staleness: reviewStaleness(projection, []reviewPart{
		{name: "artifact", event: artifact.Event, stale: artifact.Stale, world: artifact.DescribesSupersededWorld},
		{name: "promise", event: promise.Event, stale: promise.Stale, world: promise.DescribesSupersededWorld},
		{name: "request", event: request.Event, stale: request.Stale, world: request.DescribesSupersededWorld},
	})}, nil
}

// reviewPart is one thing a review stands on, with the two staleness facts the
// projection keeps about it.
type reviewPart struct {
	name  string
	event string
	stale bool
	world bool
}

// stalenessCauseCap bounds the causes a verdict body names. A verdict is a
// message, not a projection: past a handful of retired bases a reader goes to
// gs provenance, and an unbounded body would only get in the way.
const stalenessCauseCap = 4

// reviewStaleness says in one line what had moved under a review. It names the
// stale parts, then whether the movement was in the world they describe rather
// than the argument they stand on, then the retired bases themselves — the
// last being what a reader actually has to go and look at.
func reviewStaleness(projection workroom.Projection, parts []reviewPart) string {
	var moved, roots []string
	world := false
	for _, part := range parts {
		if !part.stale {
			continue
		}
		moved = append(moved, part.name)
		roots = append(roots, part.event)
		world = world || part.world
	}
	if len(moved) == 0 {
		return ""
	}
	note := strings.Join(moved, ", ") + " stale"
	if world {
		note += "; describes a superseded world"
	}
	causes := retiredBases(projection, roots)
	if len(causes) == 0 {
		return note
	}
	suffix := ""
	if len(causes) > stalenessCauseCap {
		suffix = fmt.Sprintf(" and %d more", len(causes)-stalenessCauseCap)
		causes = causes[:stalenessCauseCap]
	}
	return note + "; retired bases: " + strings.Join(causes, ", ") + suffix
}

// retiredBases walks provenance from the given events down to the retired
// statements underneath them: the acts that actually moved. The walk stops at
// each retired basis, because that is the nearest act a reader can act on and
// everything under it is stale only through it. Breadth-first over a visited
// set, so a shared ancestor is named once and a diamond terminates.
func retiredBases(projection workroom.Projection, events []string) []string {
	retired := make(map[string]bool)
	for _, statement := range projection.Statements {
		if statement.Retired {
			retired[statement.Event] = true
		}
	}
	seen := make(map[string]bool, len(events))
	queue := append([]string(nil), events...)
	for _, event := range events {
		seen[event] = true
	}
	var found []string
	for len(queue) > 0 {
		event := queue[0]
		queue = queue[1:]
		for _, basis := range projection.Provenance[event] {
			if seen[basis] {
				continue
			}
			seen[basis] = true
			if retired[basis] {
				found = append(found, basis)
				continue
			}
			queue = append(queue, basis)
		}
	}
	slices.Sort(found)
	return found
}

// validateMerge keeps the strict reading that review has given up. Review is a
// judgement and merge is the machine acting on one: put the latitude where a
// reviewer is present to exercise it, and keep the refusal where nobody is.
// A refused review left a question no one could answer; a refused merge leaves
// a signed approval standing and asks only that the record be brought up to
// date, which is repair rather than deadlock. It also leaves the meaning of
// staleness untouched at the one gate that moves main, where a proposal on
// exactly that question is still in flight.
func validateMerge(ctx context.Context, workspace *app.Workspace, checkout, candidate, approvalEvent string) error {
	if err := validateCheckout(ctx, workspace.Repo, checkout, candidate, false); err != nil {
		return err
	}
	if receipt, found, err := existingGitMergeReceipt(ctx, checkout, approvalEvent); err != nil {
		return err
	} else if found {
		return fmt.Errorf("approval was already used by merge %s into target pre-head %s", receipt.MergeHead, receipt.TargetPreHead)
	}
	snapshot, err := workspace.Snapshot(ctx)
	if err != nil {
		return err
	}
	projection := snapshot.Projection
	for _, statement := range projection.Statements {
		if statement.Body["merge_approval"] == approvalEvent && decisionEffective(projection, statement.Event) {
			return fmt.Errorf("approval already has durable merge receipt %s", statement.Event)
		}
	}
	approval, err := liveStatement(projection, approvalEvent, workroom.KindReport)
	if err != nil {
		return fmt.Errorf("approval: %w", err)
	}
	if !approval.Ratified {
		return errors.New("approval report is not ratified by its requester")
	}
	if approval.Body["verdict"] != "approved" {
		return errors.New("review verdict is not approved")
	}
	if approval.Body["head"] != candidate {
		return fmt.Errorf("candidate %s does not equal approved head %s", candidate, approval.Body["head"])
	}
	artifactEvent := approval.Body["artifact"]
	if artifactEvent == "" || !slices.Contains(projection.Provenance[approvalEvent], artifactEvent) {
		return errors.New("approval does not rest on its named artifact")
	}
	artifact, err := liveArtifact(projection, artifactEvent)
	if err != nil {
		return fmt.Errorf("approval artifact: %w", err)
	}
	if artifact.Commit != candidate {
		return fmt.Errorf("approved artifact head %s does not equal candidate %s", artifact.Commit, candidate)
	}
	// The rule that review comes from a different agent is checked here rather
	// than assumed. An approval the projection cannot call independent does not
	// merge, whether because the reviewer implemented the head or because the
	// record cannot say who did.
	review, found := projection.Review(approvalEvent)
	if !found {
		return errors.New("approval is not projected as a review")
	}
	switch review.Independence {
	case workroom.IndependenceSelfReview:
		return errors.New("approval was signed by the actor who implemented this head; an independent review is required")
	case workroom.IndependenceIndependent:
		return nil
	default:
		return errors.New("the record cannot say whether this approval was independent; name the reviewed artifact in the review report")
	}
}

func validateCheckout(ctx context.Context, workroomRepo, checkout, commit string, requireHead bool) error {
	want, err := canonicalCommit(ctx, checkout, commit)
	if err != nil {
		return err
	}
	if want != commit {
		return fmt.Errorf("commit must be the full canonical object ID: got %s, resolved %s", commit, want)
	}
	_, workroomCommon, err := app.ResolveGitDirs(ctx, workroomRepo)
	if err != nil {
		return err
	}
	_, checkoutCommon, err := app.ResolveGitDirs(ctx, checkout)
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

func canonicalCommit(ctx context.Context, repo, commit string) (string, error) {
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

// standingArtifact returns an artifact that may still be acted on. Retirement
// withdraws the pointer and is a refusal; being judged ineffective means the
// pointer was never conferred. Staleness is neither: it says a basis moved
// under the artifact, while the commit it names is immutable and still names
// exactly what it named. Callers that want the strict reading say so.
func standingArtifact(projection workroom.Projection, event string) (workroom.Artifact, error) {
	if !decisionEffective(projection, event) {
		return workroom.Artifact{}, errors.New("artifact is not effective")
	}
	for _, artifact := range projection.Artifacts {
		if artifact.Event == event {
			if artifact.Retired {
				return workroom.Artifact{}, errors.New("artifact is retired")
			}
			return artifact, nil
		}
	}
	return workroom.Artifact{}, errors.New("artifact event is unknown")
}

// liveArtifact is the strict reading: current as well as standing.
func liveArtifact(projection workroom.Projection, event string) (workroom.Artifact, error) {
	artifact, err := standingArtifact(projection, event)
	if err != nil {
		return workroom.Artifact{}, err
	}
	if artifact.Stale {
		return workroom.Artifact{}, errors.New("artifact is stale")
	}
	return artifact, nil
}

// standingStatement is the same judgement for a statement: refuse what was
// retired or judged ineffective, and report staleness rather than refuse it.
func standingStatement(projection workroom.Projection, event string, kind workroom.Kind) (workroom.Statement, error) {
	if !decisionEffective(projection, event) {
		return workroom.Statement{}, errors.New("statement is not effective")
	}
	for _, statement := range projection.Statements {
		if statement.Event == event {
			if statement.Kind != kind {
				return workroom.Statement{}, fmt.Errorf("statement is %s, want %s", statement.Kind, kind)
			}
			if statement.Retired {
				return workroom.Statement{}, errors.New("statement is retired")
			}
			return statement, nil
		}
	}
	return workroom.Statement{}, errors.New("statement event is unknown")
}

// liveStatement is the strict reading: current as well as standing.
func liveStatement(projection workroom.Projection, event string, kind workroom.Kind) (workroom.Statement, error) {
	statement, err := standingStatement(projection, event, kind)
	if err != nil {
		return workroom.Statement{}, err
	}
	if statement.Stale {
		return workroom.Statement{}, errors.New("statement is stale")
	}
	return statement, nil
}

func uniqueStandingBasis(projection workroom.Projection, event string, kind workroom.Kind) (workroom.Statement, error) {
	var found []workroom.Statement
	for _, basis := range projection.Provenance[event] {
		statement, err := standingStatement(projection, basis, kind)
		if err == nil {
			found = append(found, statement)
		}
	}
	if len(found) != 1 {
		return workroom.Statement{}, fmt.Errorf("expected one standing %s basis, found %d", kind, len(found))
	}
	return found[0], nil
}

func decisionEffective(projection workroom.Projection, event string) bool {
	for _, decision := range projection.Decisions {
		if decision.Event == event {
			return decision.Verdict == workroom.Effective
		}
	}
	return false
}

func ratifyCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("ratify", arguments)
	as := set.String("as", "", "actor name")
	serverURL := set.String("server", "", "resident sequencer URL")
	key := set.String("idempotency-key", "", "stable retry key")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	if set.NArg() != 1 {
		return errors.New("ratify requires one target event")
	}
	actor, err := signingActor(*as)
	if err != nil {
		return err
	}
	workspace, err := app.Open(ctx, *repo)
	if err != nil {
		return err
	}
	target := set.Arg(0)
	record, err := submitAct(ctx, workspace, *serverURL, actor, app.Act{Verb: app.VerbRatify, Target: target, IdempotencyKey: *key})
	if err != nil {
		return err
	}
	fmt.Println(record.ID)
	return nil
}

func supersedeCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("supersede", arguments)
	as := set.String("as", "", "actor name")
	message := set.String("text", "", "reason")
	citedOK := set.Bool("cited-ok", false, "retire even though documentation still cites the target")
	serverURL := set.String("server", "", "resident sequencer URL")
	key := set.String("idempotency-key", "", "stable retry key")
	var rests values
	set.Var(&rests, "rests-on", "additional causal event id")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	if set.NArg() != 1 {
		return errors.New("supersede requires one target event")
	}
	actor, err := signingActor(*as)
	if err != nil {
		return err
	}
	workspace, err := app.Open(ctx, *repo)
	if err != nil {
		return err
	}
	target := set.Arg(0)
	record, err := submitAct(ctx, workspace, *serverURL, actor, app.Act{Verb: app.VerbSupersede, Target: target, Text: *message, RestsOn: rests, IdempotencyKey: *key, CitedOK: *citedOK})
	if err != nil {
		return err
	}
	fmt.Println(record.ID)
	return nil
}

// batchAct is one entry of a batch file. Field names and meanings follow
// app.Act; label is local to the batch and never leaves it.
type batchAct struct {
	Label          string            `json:"label,omitempty"`
	Verb           app.Verb          `json:"verb"`
	Kind           workroom.Kind     `json:"kind,omitempty"`
	Text           string            `json:"text,omitempty"`
	Body           map[string]string `json:"body,omitempty"`
	Target         string            `json:"target,omitempty"`
	RestsOn        []string          `json:"rests_on,omitempty"`
	IdempotencyKey string            `json:"idempotency_key,omitempty"`
}

// batchError names the class of a batch failure so a caller can branch on it
// without reading the prose.
type batchError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *batchError) Error() string { return "batch " + e.Code + ": " + e.Message }

func batchFail(code, format string, arguments ...any) *batchError {
	return &batchError{Code: code, Message: fmt.Sprintf(format, arguments...)}
}

// batchOutcome is what became of one act. Landed and replayed both name a
// durable event: replayed means an earlier run of the same batch already
// appended it under the same idempotency key.
type batchOutcome struct {
	Position int    `json:"position"`
	Label    string `json:"label,omitempty"`
	Event    string `json:"event,omitempty"`
	Outcome  string `json:"outcome"` // landed, replayed, failed, or skipped
}

type batchReport struct {
	Acts     []batchOutcome `json:"acts"`
	Landed   int            `json:"landed"`
	Replayed int            `json:"replayed"`
	Error    *batchError    `json:"error,omitempty"`
}

// batchCommand appends an ordered chain of acts in one process. Opening the
// workspace once means the log is loaded and verified once: the resident
// submitter keeps that verified frontier and dedup index, and every further act
// in the chain only extends it.
//
// The input is a JSON array of acts, read from a file or from standard input
// when the argument is "-" or absent:
//
//	[
//	  {"label": "req", "verb": "state", "kind": "request", "text": "do the thing",
//	   "body": {"to": "@worker", "conditions": "tests green"},
//	   "rests_on": ["git:sha1:<genesis>#git:sha1:<event>"],
//	   "idempotency_key": "thing-request"},
//	  {"label": "promise", "verb": "state", "kind": "promise", "text": "I will",
//	   "rests_on": ["$req"], "idempotency_key": "thing-promise"}
//	]
//
// An array rather than one act per line, because the whole file is then parsed
// before anything lands: a malformed entry anywhere costs nothing. The array
// must be the whole input — only whitespace may follow it — so trailing bytes
// are refused rather than silently ignored. A later act may cite an earlier act
// of the same batch as "$label" in rests_on or target, and the label resolves to
// the event id minted for that act. Every reference is checked before the first
// append, so an unknown or forward label also lands nothing.
//
// Acts land one at a time; the batch is not atomic. Events are commits on
// refs/seq/<genesis>, and the kernel owns the whole write: envelope and actor
// signature verification, the genesis payload ceiling, the admission hook, the
// dedup index, sequencer signing, and the compare-and-swap that publishes each
// commit. There is no multi-event entry point, and building a chain of commits
// outside that path in order to move the ref once would mean repeating those
// checks where the kernel cannot enforce them. Per-act idempotency keys carry
// the recovery instead: rerunning the same file replays the prefix that already
// landed, without duplicating it, and continues from the first act that did
// not. Acts given no idempotency key are not resumable and land afresh.
//
// With --server the same signed requests are forwarded to the resident
// sequencer one at a time through /v0/submit. That server holds the single
// verified frontier, and batch semantics stay per-act exactly as they are
// locally.
func batchCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("batch", arguments)
	as := set.String("as", "", "actor name for every act in the batch")
	serverURL := set.String("server", "", "resident sequencer URL")
	citedOK := set.Bool("cited-ok", false, "retire even though documentation still cites a target")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	if set.NArg() > 1 {
		return errors.New("batch takes one file, or - for standard input")
	}
	if *as == "" {
		return errors.New("batch requires --as")
	}
	acts, err := readBatch(set.Arg(0))
	if err != nil {
		return err
	}
	workspace, err := app.Open(ctx, *repo)
	if err != nil {
		return err
	}
	_, private, err := workspace.Actor(*as)
	if err != nil {
		return err
	}
	// Before the first append, for the same reason readBatch reads the whole
	// file first: a batch that cannot land cleanly should land nothing. Both
	// times a retirement broke main it came through here, not through the
	// single-act path, so a guard only on gs supersede would have caught
	// neither.
	for position, entry := range acts {
		if entry.Verb != app.VerbSupersede || strings.HasPrefix(entry.Target, "$") {
			continue
		}
		if err := workspace.RefuseCitedRetirement(ctx, entry.Target, *citedOK); err != nil {
			return fmt.Errorf("act %d: %w", position, err)
		}
	}
	report, err := runBatch(ctx, workspace, *serverURL, *as, private, acts, *citedOK)
	if printErr := printJSON(report); printErr != nil && err == nil {
		err = printErr
	}
	return err
}

// readBatch reads the whole input before anything lands, and proves the act
// array consumed all of it. An empty path or "-" reads standard input.
func readBatch(path string) ([]batchAct, error) {
	var content []byte
	var err error
	if path == "" || path == "-" {
		content, err = io.ReadAll(os.Stdin)
	} else {
		content, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var acts []batchAct
	if err := decoder.Decode(&acts); err != nil {
		return nil, batchFail("input", "read batch acts: %v", err)
	}
	// The array must be the whole input. decoder.More only reports whether the
	// value being parsed has another element, so a stray closing delimiter
	// reads to it as a clean end and the acts before it would land anyway.
	// A second Decode that returns io.EOF is the proof: anything else left in
	// the stream, well formed or not, decodes into a value or fails, and both
	// reject the input before the first append.
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, batchFail("input", "batch input has content after the act array")
	}
	if len(acts) == 0 {
		return nil, batchFail("input", "batch input contains no acts")
	}
	return acts, nil
}

// runBatch checks the whole chain, then appends it act by act against the one
// verified frontier the workspace already holds.
func runBatch(ctx context.Context, workspace *app.Workspace, serverURL, actorName string, private ed25519.PrivateKey, acts []batchAct, citedOK bool) (batchReport, error) {
	report := batchReport{Acts: make([]batchOutcome, len(acts))}
	for position, entry := range acts {
		report.Acts[position] = batchOutcome{Position: position, Label: entry.Label, Outcome: "skipped"}
	}
	if position, failure := checkBatch(acts); failure != nil {
		report.Acts[position].Outcome = "failed"
		report.Error = failure
		return report, failure
	}
	minted := make(map[string]string, len(acts))
	for position, entry := range acts {
		act := app.Act{
			Verb: entry.Verb, Kind: entry.Kind, Text: entry.Text, Body: entry.Body,
			Target: resolveLabel(entry.Target, minted), IdempotencyKey: entry.IdempotencyKey,
			CitedOK: citedOK,
		}
		for _, reference := range entry.RestsOn {
			act.RestsOn = append(act.RestsOn, resolveLabel(reference, minted))
		}
		submission, err := submitSigned(ctx, workspace, serverURL, actorName, private, act)
		if err != nil {
			failure := batchFail("submit", "%v", err)
			report.Acts[position].Outcome = "failed"
			report.Error = failure
			return report, failure
		}
		report.Acts[position].Event = submission.Record.ID
		if submission.Result.Replay {
			report.Acts[position].Outcome = "replayed"
			report.Replayed++
		} else {
			report.Acts[position].Outcome = "landed"
			report.Landed++
		}
		if entry.Label != "" {
			minted[entry.Label] = submission.Record.ID
		}
	}
	return report, nil
}

// checkBatch validates the shape of every act and proves that each intra-batch
// reference names a label defined by a strictly earlier act. It runs before the
// first append, so a chain that cannot resolve lands nothing.
func checkBatch(acts []batchAct) (int, *batchError) {
	labels := make(map[string]int, len(acts))
	for position, entry := range acts {
		switch entry.Verb {
		case app.VerbState:
			if entry.Target != "" {
				return position, batchFail("verb", "state takes no target")
			}
		case app.VerbRatify, app.VerbSupersede:
			if entry.Target == "" {
				return position, batchFail("verb", "%s requires a target", entry.Verb)
			}
		default:
			return position, batchFail("verb", "unknown verb %q", entry.Verb)
		}
		for _, reference := range append([]string{entry.Target}, entry.RestsOn...) {
			name, cited := strings.CutPrefix(reference, "$")
			if !cited {
				continue
			}
			if _, defined := labels[name]; !defined {
				return position, batchFail("reference", "$%s is not a label of an earlier act", name)
			}
		}
		if entry.Label == "" {
			continue
		}
		if strings.HasPrefix(entry.Label, "$") {
			return position, batchFail("label", "label %q must not begin with $", entry.Label)
		}
		if _, exists := labels[entry.Label]; exists {
			return position, batchFail("label", "label %q is used twice", entry.Label)
		}
		labels[entry.Label] = position
	}
	return 0, nil
}

// resolveLabel turns a "$label" citation into the event id minted for that act.
// Anything else is already a durable identifier and passes through. checkBatch
// has proved the label belongs to an earlier act, and the batch stops at the
// first failure, so every label reached here has been minted.
func resolveLabel(reference string, minted map[string]string) string {
	if name, cited := strings.CutPrefix(reference, "$"); cited {
		return minted[name]
	}
	return reference
}

func submitAct(ctx context.Context, workspace *app.Workspace, serverURL, actorName string, act app.Act) (workroom.Record, error) {
	_, private, err := workspace.Actor(actorName)
	if err != nil {
		return workroom.Record{}, err
	}
	submission, err := submitSigned(ctx, workspace, serverURL, actorName, private, act)
	return submission.Record, err
}

// submitSigned appends one act with custody the caller already holds. A chain
// of acts therefore reads the actor key once, and a local submission reuses the
// workspace's resident verified frontier instead of scanning the log again.
// Every command that writes for an author comes through here, so this is where
// an author is told that the kind they wrote means nothing here.
func submitSigned(ctx context.Context, workspace *app.Workspace, serverURL, actorName string, private ed25519.PrivateKey, act app.Act) (app.Submission, error) {
	request, err := workspace.BuildActRequest(ctx, private, actorName, act)
	if err != nil {
		return app.Submission{}, err
	}
	submission, err := submitRequest(ctx, workspace, serverURL, request)
	if err != nil {
		return app.Submission{}, err
	}
	if act.Verb == app.VerbState {
		warnUndefinedKind(ctx, workspace, act.Kind)
	}
	return submission, nil
}

// warnUndefinedKind tells an author, on the stream they are already reading,
// that the act which just landed carries a kind no rule in this workroom
// reads. The act stays: warning is the whole of the change, and refusing it
// would hide the attempt. Reading the vocabulary costs one projection of the
// log, which a deliberate durable write can afford, and a chain of writes in
// one process pays for once.
func warnUndefinedKind(ctx context.Context, workspace *app.Workspace, kind workroom.Kind) {
	if warning := residentclient.UndefinedKindWarning(ctx, workspace, kind); warning != "" {
		fmt.Fprintln(os.Stderr, "gs: warning:", warning)
	}
}

func submitRequest(ctx context.Context, workspace *app.Workspace, serverURL string, request kernel.Request) (app.Submission, error) {
	return residentclient.New(10*time.Second).Submit(ctx, workspace, serverURL, request)
}

func statusCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("status", arguments)
	jsonOutput := set.Bool("json", false, "render JSON")
	all := set.Bool("all", false, "render complete human-readable status")
	serverURL := set.String("server", "", "workroom URL including live state")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	if *jsonOutput && *all {
		return errors.New("status accepts only one of --all and --json")
	}
	workspace, err := app.Open(ctx, *repo)
	if err != nil {
		return err
	}
	if *serverURL != "" {
		if err := validateLoopbackServer(*serverURL); err != nil {
			return err
		}
		if *jsonOutput || *all {
			status, remoteErr := fetchFullStatus(ctx, *serverURL)
			if remoteErr == nil {
				if err := validateRemoteFrontier(ctx, workspace, status.Durable.Genesis, status.Durable.Head); err == nil {
					if *jsonOutput {
						return printJSON(status.Durable)
					}
					_, err = os.Stdout.Write(workroom.RenderStatus(status.Durable.Projection))
					return err
				} else {
					remoteErr = err
				}
			}
			fmt.Fprintf(os.Stderr, "gs: resident status unavailable (%v); performing verified local fallback\n", remoteErr)
		} else {
			summary, remoteErr := fetchSummary(ctx, workspace, *serverURL)
			if remoteErr == nil {
				_, err = os.Stdout.Write(statusview.Render(summary.Durable, "resident summary"))
				return err
			}
			fmt.Fprintf(os.Stderr, "gs: resident summary unavailable (%v); performing verified local fallback\n", remoteErr)
		}
	}
	snapshot, err := snapshotWithProgress(ctx, workspace)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return printJSON(snapshot)
	}
	if *all {
		_, err = os.Stdout.Write(workroom.RenderStatus(snapshot.Projection))
		return err
	}
	source := "verified local"
	if *serverURL != "" {
		source = "verified local fallback"
	}
	summary := statusview.Build(snapshot.Genesis, snapshot.Head, snapshot.Depth, snapshot.Projection)
	_, err = os.Stdout.Write(statusview.Render(summary, source))
	return err
}

const (
	summaryResponseLimit = 64 << 10
	fullResponseLimit    = 64 << 20
)

func validateLoopbackServer(raw string) error {
	_, err := residentclient.ValidateURL(raw)
	return err
}

func fetchSummary(ctx context.Context, workspace *app.Workspace, raw string) (service.SummaryStatus, error) {
	before, err := workspace.Store.Head(ctx, kernel.Ref(workspace.Config.Genesis))
	if err != nil {
		return service.SummaryStatus{}, err
	}
	var summary service.SummaryStatus
	if err := residentclient.New(2*time.Second).GetJSON(ctx, raw, "/v0/status-summary", summaryResponseLimit, &summary); err != nil {
		return service.SummaryStatus{}, err
	}
	if err := validateRemoteFrontierAt(ctx, workspace, before, summary.Durable.Genesis, summary.Durable.Head); err != nil {
		return service.SummaryStatus{}, err
	}
	if len(summary.Cursor.Frontier) != 1 || summary.Cursor.Frontier[0].Genesis != summary.Durable.Genesis || summary.Cursor.Frontier[0].Head != summary.Durable.Head || summary.Cursor.Frontier[0].Depth != summary.Durable.Depth {
		return service.SummaryStatus{}, errors.New("resident summary cursor does not match its durable frontier")
	}
	return summary, nil
}

func fetchFullStatus(ctx context.Context, raw string) (service.Status, error) {
	var status service.Status
	err := residentclient.New(10*time.Second).GetJSON(ctx, raw, "/v0/status", fullResponseLimit, &status)
	return status, err
}

func validateRemoteFrontier(ctx context.Context, workspace *app.Workspace, genesis, head string) error {
	current, err := workspace.Store.Head(ctx, kernel.Ref(workspace.Config.Genesis))
	if err != nil {
		return err
	}
	return validateRemoteFrontierAt(ctx, workspace, current, genesis, head)
}

func validateRemoteFrontierAt(ctx context.Context, workspace *app.Workspace, before, genesis, head string) error {
	after, err := workspace.Store.Head(ctx, kernel.Ref(workspace.Config.Genesis))
	if err != nil {
		return err
	}
	if genesis != workspace.Config.Genesis {
		return errors.New("resident summary genesis does not match the selected workroom")
	}
	if before != after {
		return errors.New("workroom head moved while resident status was read")
	}
	if head != after {
		return errors.New("resident summary head is not current")
	}
	return nil
}

func snapshotWithProgress(ctx context.Context, workspace *app.Workspace) (app.Snapshot, error) {
	return loadSnapshotWithProgress(ctx, os.Stderr, workspace.Snapshot)
}

func loadSnapshotWithProgress(ctx context.Context, progress io.Writer, load func(context.Context) (app.Snapshot, error)) (app.Snapshot, error) {
	type result struct {
		snapshot app.Snapshot
		err      error
	}
	ready := make(chan result, 1)
	go func() {
		snapshot, err := load(ctx)
		ready <- result{snapshot: snapshot, err: err}
	}()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case result := <-ready:
		return result.snapshot, result.err
	case <-timer.C:
		fmt.Fprintln(progress, "gs: verifying the durable log; this may take a while")
		select {
		case result := <-ready:
			return result.snapshot, result.err
		case <-ctx.Done():
			return app.Snapshot{}, ctx.Err()
		}
	case <-ctx.Done():
		return app.Snapshot{}, ctx.Err()
	}
}

func provenanceCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("provenance", arguments)
	if err := set.Parse(arguments); err != nil {
		return err
	}
	if set.NArg() != 1 {
		return errors.New("provenance requires one event")
	}
	workspace, err := app.Open(ctx, *repo)
	if err != nil {
		return err
	}
	snapshot, err := workspace.Snapshot(ctx)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(workroom.RenderProvenance(snapshot.Projection, set.Arg(0)))
	return err
}

func verifyCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("verify", arguments)
	if err := set.Parse(arguments); err != nil {
		return err
	}
	workspace, err := app.Open(ctx, *repo)
	if err != nil {
		return err
	}
	verification, err := workspace.Verify(ctx)
	if err != nil {
		return err
	}
	return printJSON(verification)
}

func serveCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("serve", arguments)
	listen := set.String("listen", "127.0.0.1:7777", "HTTP listen address")
	otlpEndpoint := set.String("otel-endpoint", "", "OTLP/HTTP Collector endpoint; disabled when empty")
	profileListen := set.String("profile-listen", "", "separate loopback pprof address; disabled when empty")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	if err := validateLoopbackListen(*listen); err != nil {
		return err
	}
	workspace, err := app.Open(ctx, *repo)
	if err != nil {
		return err
	}
	if workspace.Config.ReadOnly {
		return errors.New("cannot serve a read-only attachment")
	}
	telemetryRuntime, err := telemetry.NewOTLP(ctx, *otlpEndpoint)
	if err != nil {
		return fmt.Errorf("configure telemetry: %w", err)
	}
	defer telemetryRuntime.Shutdown(context.Background())
	server, err := service.NewObserved(workspace, telemetryRuntime.Observer())
	if err != nil {
		return err
	}
	stopProfile, err := serveProfiler(ctx, *profileListen)
	if err != nil {
		return err
	}
	defer stopProfile()
	// Bind before publishing, so the address advertised to clients is the one
	// actually being served — including the port the kernel chose when the
	// listen address asked for any.
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	defer listener.Close()
	withdraw, err := workspace.PublishResident("http://" + listener.Addr().String())
	if err != nil {
		return err
	}
	defer withdraw()
	fmt.Fprintf(os.Stderr, "gitseq workroom http://%s\n", listener.Addr())
	httpServer := &http.Server{Handler: telemetryRuntime.Handler(server.Handler()), ReadHeaderTimeout: 5 * time.Second}
	// The watcher retires with the command it serves, so a serving call that
	// ends some other way does not leave a goroutine holding the server.
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-ctx.Done():
			_ = httpServer.Close()
		case <-finished:
		}
	}()
	// Being told to stop is how serving ends, so ending that way is success.
	// Reporting it as a failure would make every ordinary stop look like a
	// fault in the logs of whatever supervises this.
	if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func serveProfiler(ctx context.Context, address string) (func(), error) {
	if address == "" {
		return func() {}, nil
	}
	if err := validateLoopbackListen(address); err != nil {
		return nil, fmt.Errorf("profile listener: %w", err)
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /debug/pprof/", pprof.Index)
	mux.HandleFunc("GET /debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("POST /debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("GET /debug/pprof/trace", pprof.Trace)
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		_ = server.Close()
	}()
	go func() { _ = server.Serve(listener) }()
	return func() {
		_ = server.Close()
		_ = listener.Close()
	}, nil
}

func validateLoopbackListen(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid --listen address: %w", err)
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("--listen must name a loopback address; the resident service is a trusted local multi-actor custodian")
	}
	return nil
}

func attachCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("attach", arguments)
	remote := set.String("remote", "origin", "Git remote")
	genesis := set.String("genesis", "", "workroom genesis hash")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	if *genesis == "" {
		return errors.New("attach requires --genesis")
	}
	if err := fetchSequenceRefs(ctx, *repo, *remote); err != nil {
		return err
	}
	formatOutput, err := git(ctx, *repo, "rev-parse", "--show-object-format")
	if err != nil {
		return err
	}
	workspace, err := app.AttachConfig(ctx, *repo, *genesis, strings.TrimSpace(formatOutput))
	if err != nil {
		return err
	}
	verification, err := workspace.Verify(ctx)
	if err != nil {
		return err
	}
	return printJSON(verification)
}

const (
	sequenceFetchRefspec       = "refs/seq/*:refs/seq/*"
	forcedSequenceFetchRefspec = "+" + sequenceFetchRefspec
)

func fetchSequenceRefs(ctx context.Context, repo, remote string) error {
	key := "remote." + remote + ".fetch"
	existing, _ := git(ctx, repo, "config", "--get-all", key)
	if containsLine(existing, forcedSequenceFetchRefspec) {
		if _, err := git(ctx, repo, "config", "--fixed-value", "--unset-all", key, forcedSequenceFetchRefspec); err != nil {
			return fmt.Errorf("remove legacy forced sequence fetch rule: %w", err)
		}
	}
	if !containsLine(existing, sequenceFetchRefspec) {
		if _, err := git(ctx, repo, "config", "--add", key, sequenceFetchRefspec); err != nil {
			return err
		}
	}
	if _, err := git(ctx, repo, "fetch", "--atomic", "--no-tags", remote, sequenceFetchRefspec); err != nil {
		return fmt.Errorf("fetch sequence refs without rewind: %w", err)
	}
	return nil
}

func git(ctx context.Context, repo string, arguments ...string) (string, error) {
	args := append([]string{"-C", repo}, arguments...)
	output, err := exec.CommandContext(ctx, "git", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(arguments, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

func containsLine(content, line string) bool {
	for _, candidate := range strings.Split(content, "\n") {
		if strings.TrimSpace(candidate) == line {
			return true
		}
	}
	return false
}

func pairs(items []string) (map[string]string, error) {
	result := make(map[string]string)
	for _, item := range items {
		key, value, ok := strings.Cut(item, "=")
		if !ok || key == "" || value == "" {
			return nil, fmt.Errorf("expected key=value, got %q", item)
		}
		result[key] = value
	}
	if len(result) == 0 {
		return nil, nil
	}
	return result, nil
}

func files(items []string) (map[string][]byte, error) {
	result := make(map[string][]byte)
	for _, item := range items {
		name, path, ok := strings.Cut(item, "=")
		if !ok || filepath.Base(name) != name {
			return nil, fmt.Errorf("expected safe-name=path, got %q", item)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		result[name] = content
	}
	if len(result) == 0 {
		return nil, nil
	}
	return result, nil
}

func printJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
