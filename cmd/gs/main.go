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
	"math"
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
	"github.com/generalbusiness-ai/gitseq/internal/apphost"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/residentclient"
	"github.com/generalbusiness-ai/gitseq/internal/reviewguard"
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
		usage(os.Stderr)
		os.Exit(2)
	}
	if os.Args[1] == "-h" || os.Args[1] == "--help" {
		usage(os.Stderr)
		return
	}
	// Help is ordinary command parsing with --help inserted. Every flag set
	// therefore prints the flags it actually accepts, with no second catalog
	// to drift, while the top-level form remains useful without a subcommand.
	if os.Args[1] == "help" {
		switch len(os.Args) {
		case 2:
			usage(os.Stderr)
			return
		case 3:
			if os.Args[2] == "help" {
				usage(os.Stderr)
				return
			}
			os.Args = []string{os.Args[0], os.Args[2], "--help"}
		default:
			fmt.Fprintln(os.Stderr, "gs: help accepts at most one subcommand")
			os.Exit(1)
		}
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
	case "whoami":
		err = whoamiCommand(ctx, os.Args[2:])
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
	case "reassign-if-unclaimed":
		err = reassignIfUnclaimedCommand(ctx, os.Args[2:])
	case "batch":
		err = batchCommand(ctx, os.Args[2:])
	case "publish":
		err = publishCommand(ctx, os.Args[2:])
	case "status":
		err = statusCommand(ctx, os.Args[2:])
	case "work":
		err = workCommand(ctx, os.Args[2:])
	case "artifacts":
		err = artifactsCommand(ctx, os.Args[2:])
	case "supersession-plan":
		err = supersessionPlanCommand(ctx, os.Args[2:])
	case "staleness-wave":
		err = stalenessWaveCommand(ctx, os.Args[2:])
	case "inspect":
		err = inspectCommand(ctx, os.Args[2:])
	case "reviews":
		err = reviewsCommand(ctx, os.Args[2:])
	case "provenance":
		err = provenanceCommand(ctx, os.Args[2:])
	case "verify":
		err = verifyCommand(ctx, os.Args[2:])
	case "checkpoint-clear":
		err = checkpointClearCommand(ctx, os.Args[2:])
	case "serve":
		err = serveCommand(ctx, os.Args[2:])
	case "attach":
		err = attachCommand(ctx, os.Args[2:])
	default:
		usage(os.Stderr)
		fmt.Fprintf(os.Stderr, "\ngs: unknown command %q\n", os.Args[1])
		os.Exit(2)
	}
	if err != nil {
		release()
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "gs:", err)
		os.Exit(1)
	}
}

func usage(output io.Writer) {
	fmt.Fprintln(output, "usage: gs <command> [flags]")
	fmt.Fprintln(output, "commands: init, actor-add, actor-retire, role-grant, role-revoke, actors, whoami, state, review, merge, ratify, supersede, reassign-if-unclaimed, batch, publish, status, work, artifacts, supersession-plan, staleness-wave, inspect, reviews, provenance, verify, checkpoint-clear, serve, attach")
	fmt.Fprintln(output, "run `gs help <command>` for command flags")
	fmt.Fprintln(output, "CLI walkthrough: docs/how-to/end-to-end.md")
	fmt.Fprintln(output, "command reference: docs/reference/gs/")
}

// publishCommand records what an ordinary Git remote already accepted. It
// mints no artifact: merge succession owns source paths, and a second live
// artifact per push at a source path is an accounting row the merger cannot
// lawfully pay off. What it records is an app-validated publication assert per
// changed watched path — see cmd/gs/publication.go and docs/reference/gs/publish.md.
//
// Identity is process-scoped like every other authoring command, and the
// server is resolved the same way too: an empty --server takes the address the
// repository advertises, "-" forces the local verified fold, and an
// advertisement that cannot be trusted refuses here, before a signing key is
// read or anything is queued. Passing the raw flag through would have made
// this the one write command that ignores what the repository publishes.
func publishCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("publish", arguments)
	as := set.String("as", "", "publishing actor")
	remote := set.String("remote", "origin", "configured Git remote")
	ref := set.String("ref", "", "published branch ref; defaults to the current branch")
	basis := set.String("basis", "", "event governing publication in this repository")
	serverFlag := set.String("server", "", "resident sequencer URL")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	if set.NArg() != 0 {
		return errors.New("publish takes no positional arguments")
	}
	actorName, err := signingActor(*as)
	if err != nil {
		return err
	}
	workspace, err := app.Open(ctx, *repo)
	if err != nil {
		return err
	}
	serverURL, err := resolveServerURL(workspace, *serverFlag)
	if err != nil {
		return err
	}
	actor, private, err := workspace.Actor(actorName)
	if err != nil {
		return err
	}
	report, publishErr := runPublication(ctx, workspace, private, actorName, actor.Fingerprint, serverURL, *remote, *ref, *basis)
	if printErr := printJSON(report); printErr != nil && publishErr == nil {
		publishErr = printErr
	}
	return publishErr
}

func flags(name string, arguments []string) (*flag.FlagSet, *string) {
	set := flag.NewFlagSet(name, flag.ContinueOnError)
	repo := set.String("repo", ".", "ordinary Git repository")
	set.SetOutput(os.Stderr)
	set.Usage = func() {
		synopsis := "[flags]"
		switch name {
		case "ratify", "supersede":
			synopsis = "[flags] <target-event>"
		case "reassign-if-unclaimed":
			synopsis = "[flags] <old-request-event>"
		case "batch":
			synopsis = "[flags] [file]"
		case "inspect", "provenance":
			synopsis = "[flags] <event>"
		}
		fmt.Fprintf(set.Output(), "usage: gs %s %s\n\nFlags:\n", name, synopsis)
		set.PrintDefaults()
	}
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
	view := workspace.View()
	return printJSON(map[string]any{"genesis": view.Genesis, "operator": view.Actors[name], "seed": seed.ID})
}

func actorAddCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("actor-add", arguments)
	as := set.String("as", "", "operator actor")
	name := set.String("name", "", "new actor name")
	kind := set.String("kind", "agent", "principal kind: human, agent, or service")
	serverFlag := set.String("server", "", "resident sequencer URL")
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
	if err := requireLocalAuthorityWrite(workspace, *serverFlag, "actor-add"); err != nil {
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
	serverFlag := set.String("server", "", "resident sequencer URL")
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
	if err := requireLocalAuthorityWrite(workspace, *serverFlag, "actor-retire"); err != nil {
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
	serverFlag := set.String("server", "", "resident sequencer URL")
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
	if err := requireLocalAuthorityWrite(workspace, *serverFlag, "role-grant"); err != nil {
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
	serverFlag := set.String("server", "", "resident sequencer URL")
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
	if err := requireLocalAuthorityWrite(workspace, *serverFlag, "role-revoke"); err != nil {
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

type whoamiView struct {
	SigningActor string          `json:"signing_actor,omitempty"`
	Source       string          `json:"source"`
	Provisioned  bool            `json:"provisioned"`
	Custody      bool            `json:"custody"`
	LocalCustody []app.ActorView `json:"local_custody"`
}

func whoamiCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("whoami", arguments)
	as := set.String("as", "", "actor name; defaults to "+actorEnvironment)
	jsonOutput := set.Bool("json", false, "render JSON")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	if set.NArg() != 0 {
		return errors.New("whoami takes no positional arguments")
	}
	workspace, err := app.Open(ctx, *repo)
	if err != nil {
		return err
	}
	actors, err := workspace.ActorViews(ctx)
	if err != nil {
		return err
	}
	view := whoamiView{Source: "unset", LocalCustody: make([]app.ActorView, 0)}
	view.SigningActor = strings.TrimSpace(*as)
	if view.SigningActor != "" {
		view.Source = "--as"
	} else if view.SigningActor = strings.TrimSpace(os.Getenv(actorEnvironment)); view.SigningActor != "" {
		view.Source = actorEnvironment
	}
	for _, actor := range actors {
		if actor.Custody {
			view.LocalCustody = append(view.LocalCustody, actor)
		}
		if actor.Name == view.SigningActor {
			view.Provisioned = !actor.Retired
			view.Custody = actor.Custody
		}
	}
	if *jsonOutput {
		return printJSON(view)
	}
	if view.SigningActor == "" {
		fmt.Fprintln(os.Stdout, "signing actor: not set")
	} else {
		fmt.Fprintf(os.Stdout, "signing actor: %s (from %s; provisioned=%t; custody=%t)\n", view.SigningActor, view.Source, view.Provisioned, view.Custody)
	}
	fmt.Fprintln(os.Stdout, "local custody:")
	if len(view.LocalCustody) == 0 {
		fmt.Fprintln(os.Stdout, "  none")
	}
	for _, actor := range view.LocalCustody {
		retired := ""
		if actor.Retired {
			retired = " (retired)"
		}
		fmt.Fprintf(os.Stdout, "  %s %s%s\n", actor.Name, actor.Fingerprint, retired)
	}
	return nil
}

func stateCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("state", arguments)
	as := set.String("as", "", "actor name")
	kind := set.String("kind", "", "statement kind")
	message := set.String("text", "", "statement text")
	serverFlag := set.String("server", "", "resident sequencer URL")
	key := set.String("idempotency-key", "", "stable retry key")
	deadOK := set.Bool("allow-dead-basis", false, "rest on retired or stale bases anyway, signing body.dead_basis_override=true")
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
	serverURL, err := resolveServerURL(workspace, *serverFlag)
	if err != nil {
		return err
	}
	body, err := pairs(bodyValues)
	if err != nil {
		return err
	}
	if workroom.Kind(*kind) == workroom.KindArtifact && body["commit"] != "" {
		if err := validateArtifactCommit(ctx, *repo, body["commit"]); err != nil {
			return err
		}
	}
	attachments, err := files(evidence)
	if err != nil {
		return err
	}
	record, err := submitAct(ctx, workspace, serverURL, actor, app.Act{Verb: app.VerbState, Kind: workroom.Kind(*kind), Text: *message, Body: body, RestsOn: rests, Attachments: attachments, IdempotencyKey: *key, AllowDeadBasis: *deadOK})
	if err != nil {
		return err
	}
	fmt.Println(record.ID)
	// The record id is already on standard output, so the advisory cannot be
	// mistaken for it: callers parse exactly one event id line and nothing
	// else moves there.
	if snapshot, err := workspace.Snapshot(ctx); err == nil {
		noteDeadRestsOn(snapshot.Projection, rests)
	}
	return nil
}

func reviewCommand(ctx context.Context, arguments []string) error {
	return reviewCommandWithValidator(ctx, arguments, nil)
}

// reviewCommandWithValidator files a guarded verdict through the one
// confirmation choreography reviewguard owns. A non-nil inject replaces the
// real basis read for tests that stage movement between reads.
func reviewCommandWithValidator(ctx context.Context, arguments []string, inject reviewguard.ReadFunc) error {
	set, repo := flags("review", arguments)
	as := set.String("as", "", "reviewer actor name")
	checkout := set.String("checkout", "", "checkout reviewed")
	var artifactsFlag repeatedFlag
	set.Var(&artifactsFlag, "artifact", "artifact event standing at the reviewed head; repeat to sign the whole reviewed set")
	promise := set.String("promise", "", "review promise event")
	verdict := set.String("verdict", "", "approved or changes-requested")
	message := set.String("text", "", "review report")
	var headNews repeatedFlag
	set.Var(&headNews, "ack-head-news", "durable statement sequenced after the review request that names this head or lane; repeat per event")
	serverFlag := set.String("server", "", "resident sequencer URL")
	key := set.String("idempotency-key", "", "stable retry key")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	if set.NArg() != 0 {
		return errors.New("review takes no positional arguments")
	}
	if *checkout == "" || len(artifactsFlag) == 0 || *promise == "" || *message == "" {
		return errors.New("review requires --checkout, --artifact, --promise, and --text")
	}
	// The first citation is the primary the verdict names; every citation is a
	// basis of the report. What a receipt may later retire is read from those
	// bases and nowhere else, so this list is the reviewer signing a set rather
	// than the implementer asserting one.
	cited, err := reviewguard.CheckCitations(artifactsFlag)
	if err != nil {
		return err
	}
	reviewer, err := signingActor(*as)
	if err != nil {
		return err
	}
	workspace, err := app.Open(ctx, *repo)
	if err != nil {
		return err
	}
	serverURL, err := resolveServerURL(workspace, *serverFlag)
	if err != nil {
		return err
	}
	// One guarded read takes the projection, the reviewer's fingerprint, and
	// the canonical frontier event from a single verified snapshot, and
	// reviewguard judges them; Confirm runs it three times — initial,
	// re-read, confirmation — with the exact-set, same-read, acknowledgment,
	// and build checks between and after, so this surface cannot drift from
	// the tool.
	read := reviewRead(ctx, workspace, reviewer, *checkout, cited[0], *promise)
	if inject != nil {
		read = inject
	}
	body, restsOn, err := reviewguard.Confirm(read, cited, headNews, *verdict, *message)
	if err != nil {
		return err
	}
	record, err := submitAct(ctx, workspace, serverURL, reviewer, app.Act{
		Verb: app.VerbState, Kind: workroom.KindReport, Text: *message,
		Body: body, RestsOn: restsOn, GuardedReview: true, IdempotencyKey: *key,
	})
	if err != nil {
		return err
	}
	fmt.Println(record.ID)
	return nil
}

// reviewRead returns the command line's guarded basis read: every read takes
// one verified snapshot, and the reviewed head comes from the checkout named
// on the command line.
func reviewRead(ctx context.Context, workspace *app.Workspace, actorName, checkout, artifactEvent, promiseEvent string) reviewguard.ReadFunc {
	return func() (reviewguard.Basis, []reviewguard.News, workroom.Projection, error) {
		snapshot, err := workspace.Snapshot(ctx)
		if err != nil {
			return reviewguard.Basis{}, nil, workroom.Projection{}, err
		}
		actor, _, err := workspace.Actor(actorName)
		if err != nil {
			return reviewguard.Basis{}, nil, workroom.Projection{}, err
		}
		basis, news, err := reviewguard.ReviewBasis(reviewguard.Read{
			Projection:          snapshot.Projection,
			ReviewerFingerprint: actor.Fingerprint,
			Checkout:            checkout,
			CommonDir:           workspace.CommonDir,
			FrontierEvent:       workspace.EventID(snapshot.Head),
		}, artifactEvent, promiseEvent)
		return basis, news, snapshot.Projection, err
	}
}

func mergeCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("merge", arguments)
	as := set.String("as", "", "actor recording the merge receipt")
	checkout := set.String("checkout", "", "checkout receiving the merge")
	candidate := set.String("candidate", "", "full approved commit ID")
	approval := set.String("approval", "", "ratified approval report event")
	mergeText := set.String("text", "", "plain-language merge description and impact")
	serverFlag := set.String("server", "", "resident sequencer URL")
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
	serverURL, err := resolveServerURL(workspace, *serverFlag)
	if err != nil {
		return err
	}
	existing, found, err := existingGitMergeReceipt(ctx, *checkout, *approval)
	if err != nil {
		return err
	}
	if !found {
		if _, err := validateMerge(ctx, workspace, *checkout, *candidate, *approval); err != nil {
			return err
		}
	}
	if strings.TrimSpace(*mergeText) == "" {
		return errors.New("merge requires --text with a plain-language description and impact")
	}
	actor, err := signingActor(*as)
	if err != nil {
		return err
	}
	_, private, err := workspace.Actor(actor)
	if err != nil {
		return err
	}
	merger := workspace.View().Actors[actor].Fingerprint
	if found {
		if existing.Candidate != *candidate {
			return fmt.Errorf("approval was already used for candidate %s", existing.Candidate)
		}
		checkoutHead, err := git(ctx, *checkout, "rev-parse", "--verify", "HEAD^{commit}")
		if err != nil {
			return err
		}
		if strings.TrimSpace(checkoutHead) != existing.MergeHead {
			return fmt.Errorf("approval was already used by merge %s, but checkout is at another head", existing.MergeHead)
		}
		refHead, err := git(ctx, *checkout, "rev-parse", "--verify", mergeReceiptRef(*approval))
		if err != nil {
			return err
		}
		refHead = strings.TrimSpace(refHead)
		if refHead == existing.TargetPreHead {
			if _, err := git(ctx, *checkout, "update-ref", mergeReceiptRef(*approval), existing.MergeHead, existing.TargetPreHead); err != nil {
				return fmt.Errorf("publish resumed merge receipt ref: %w", err)
			}
		} else if refHead != existing.MergeHead {
			return fmt.Errorf("merge receipt ref is at unexpected commit %s", refHead)
		}
		if err := recordMergeSuccession(ctx, workspace, *checkout, serverURL, actor, private, existing); err != nil {
			return err
		}
		fmt.Println(existing.MergeHead)
		return nil
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
	staleness, err := validateMerge(ctx, workspace, *checkout, *candidate, *approval)
	if err != nil {
		return err
	}
	// The last check before Git is touched: the signer. Nothing has been
	// reserved and nothing has moved, so this refusal costs the caller nothing
	// and leaves the approval unspent for the actor whose work it is.
	preMerge, err := workspace.Snapshot(ctx)
	if err != nil {
		return err
	}
	if err := requireApprovedImplementer(preMerge.Projection, *approval, merger); err != nil {
		return err
	}
	receiptRef := mergeReceiptRef(*approval)
	if _, err := git(ctx, *checkout, "update-ref", receiptRef, targetPreHead, ""); err != nil {
		return errors.New("approval is already reserved or used by another merge")
	}
	landed := false
	merging := false
	defer func() {
		if !landed {
			_, _ = git(context.Background(), *checkout, "update-ref", "-d", receiptRef, targetPreHead)
			if merging {
				_, _ = git(context.Background(), *checkout, "merge", "--abort")
			}
		}
	}()
	merging = true
	if _, err := git(ctx, *checkout, "merge", "--no-ff", "--no-commit", "--", *candidate); err != nil {
		return err
	}
	changes, err := readStagedMergeChanges(ctx, *checkout)
	if err != nil {
		return fmt.Errorf("read tentative merge changes: %w", err)
	}
	if err := validateMergeChangePaths(changes); err != nil {
		return err
	}
	snapshot, err := workspace.Snapshot(ctx)
	if err != nil {
		return err
	}
	predecessors := successionPredecessors(ctx, *checkout, snapshot.Projection, changes, targetPreHead, *candidate)
	plan := planSuccession(snapshot.Projection, changes, predecessors)
	if err := preflightSuccession(ctx, workspace, *checkout, plan); err != nil {
		return fmt.Errorf("merge succession preflight: %w", err)
	}
	// The prospective directional reviewed-path guard lives here, in fresh
	// preflight: after the temporary receipt reservation and the tentative
	// merge staging, but before HEAD moves and before any durable workroom
	// record is appended. It runs nowhere else — succession recording never
	// re-judges reach, so neither a fresh merge overtaken by concurrent
	// admissions nor a sealed receipt is stranded against a policy adopted
	// after the plan was authorized.
	if err := refuseUnreachableCrossAuthorRetirements(snapshot.Projection, plan, *approval, merger); err != nil {
		return fmt.Errorf("merge succession preflight: %w", err)
	}
	message, err := mergeReceiptMessage(*mergeText, *approval, *candidate, targetPreHead, staleness, plan)
	if err != nil {
		return err
	}
	// The merge commit makes its durable receipt mandatory. Prove every act in
	// that receipt chain can cross the exact local and resident admission size
	// boundaries before moving HEAD; otherwise retry could never finish it.
	prospectiveActs := successionActs(*approval, *candidate, targetPreHead, *candidate, staleness, plan)
	if err := preflightBatchAdmission(ctx, workspace, serverURL, actor, private, prospectiveActs, true); err != nil {
		return fmt.Errorf("merge succession admission preflight: %w", err)
	}
	if _, err := git(ctx, *checkout, "commit", "-m", message); err != nil {
		return err
	}
	merging = false
	landed = true
	head, err := git(ctx, *checkout, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	head = strings.TrimSpace(head)
	receipt, ok, err := readMergeReceipt(ctx, *checkout, head)
	if err != nil {
		return err
	}
	if !ok || receipt.Approval != *approval || receipt.Candidate != *candidate || receipt.TargetPreHead != targetPreHead || receipt.MergeHead != head {
		return errors.New("resulting merge commit does not carry the requested receipt")
	}
	if _, err := git(ctx, *checkout, "update-ref", receiptRef, head, targetPreHead); err != nil {
		return fmt.Errorf("publish merge receipt ref: %w", err)
	}
	if err := recordMergeSuccession(ctx, workspace, *checkout, serverURL, actor, private, receipt); err != nil {
		return err
	}
	fmt.Println(head)
	return nil
}

type mergeReceipt struct {
	Approval            string
	Candidate           string
	TargetPreHead       string
	MergeHead           string
	Retirements         string
	Successors          string
	LeftLive            string
	LeftLivePresent     bool
	ChangedPaths        string
	ChangedPathsPresent bool
	Staleness           string
}

const (
	mergeApprovalTrailer     = "Gitseq-Approval: "
	mergeCandidateTrailer    = "Gitseq-Candidate: "
	mergeTargetTrailer       = "Gitseq-Target-Pre-Head: "
	mergeRetirementsTrailer  = "Gitseq-Retirements: "
	mergeSuccessorsTrailer   = "Gitseq-Successors: "
	mergeLeftLiveTrailer     = "Gitseq-Left-Live: "
	mergeChangedPathsTrailer = "Gitseq-Changed-Paths: "
	mergeStalenessTrailer    = "Gitseq-Staleness: "
)

func mergeReceiptKey(approval string) string {
	sum := sha256.Sum256([]byte(approval))
	return "merge-receipt-" + hex.EncodeToString(sum[:])
}

func mergeReceiptRef(approval string) string {
	return "refs/gitseq/merge-receipts/" + strings.TrimPrefix(mergeReceiptKey(approval), "merge-receipt-")
}

func mergeReceiptMessage(text, approval, candidate, targetPreHead, staleness string, plan successionPlan) (string, error) {
	if (plan.leftLive != nil) != (plan.changedPaths != nil) {
		return "", errors.New("prospective merge receipt requires both left-live accounting and changed paths")
	}
	retirements, err := json.Marshal(plan.retire)
	if err != nil {
		return "", err
	}
	successors, err := json.Marshal(plan.publish)
	if err != nil {
		return "", err
	}
	message := fmt.Sprintf("%s\n\n%s%s\n%s%s\n%s%s\n%s%s\n%s%s", strings.TrimSpace(text),
		mergeApprovalTrailer, approval, mergeCandidateTrailer, candidate, mergeTargetTrailer, targetPreHead,
		mergeRetirementsTrailer, retirements, mergeSuccessorsTrailer, successors)
	if plan.leftLive != nil {
		leftLive, err := json.Marshal(plan.leftLive)
		if err != nil {
			return "", err
		}
		message += "\n" + mergeLeftLiveTrailer + string(leftLive)
	}
	if plan.changedPaths != nil {
		changedPaths, err := json.Marshal(plan.changedPaths)
		if err != nil {
			return "", err
		}
		message += "\n" + mergeChangedPathsTrailer + string(changedPaths)
	}
	if staleness != "" {
		message += "\n" + mergeStalenessTrailer + staleness
	}
	return message, nil
}

// readMergeReceipt separates "this commit is not one of our receipts" from
// "Git could not be asked". Merge commits already in main predate the sealed
// succession trailers, so treating their absence as a malformed receipt made a
// replayed old approval report a parse failure instead of the intended
// already-used refusal. A false ok is that ordinary fact, not an error.
func readMergeReceipt(ctx context.Context, checkout, head string) (mergeReceipt, bool, error) {
	message, err := git(ctx, checkout, "show", "-s", "--format=%B", head)
	if err != nil {
		return mergeReceipt{}, false, err
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
		case strings.HasPrefix(line, mergeRetirementsTrailer):
			receipt.Retirements = strings.TrimPrefix(line, mergeRetirementsTrailer)
		case strings.HasPrefix(line, mergeSuccessorsTrailer):
			receipt.Successors = strings.TrimPrefix(line, mergeSuccessorsTrailer)
		case line == strings.TrimSpace(mergeLeftLiveTrailer):
			receipt.LeftLivePresent = true
		case strings.HasPrefix(line, mergeLeftLiveTrailer):
			receipt.LeftLive = strings.TrimPrefix(line, mergeLeftLiveTrailer)
			receipt.LeftLivePresent = true
		case line == strings.TrimSpace(mergeChangedPathsTrailer):
			receipt.ChangedPathsPresent = true
		case strings.HasPrefix(line, mergeChangedPathsTrailer):
			receipt.ChangedPaths = strings.TrimPrefix(line, mergeChangedPathsTrailer)
			receipt.ChangedPathsPresent = true
		case strings.HasPrefix(line, mergeStalenessTrailer):
			receipt.Staleness = strings.TrimPrefix(line, mergeStalenessTrailer)
		}
	}
	parents, err := git(ctx, checkout, "rev-list", "--parents", "-n", "1", head)
	if err != nil {
		return mergeReceipt{}, false, err
	}
	fields := strings.Fields(parents)
	if receipt.Approval == "" || receipt.Candidate == "" || receipt.TargetPreHead == "" || receipt.Retirements == "" || receipt.Successors == "" || len(fields) != 3 ||
		fields[0] != head || fields[1] != receipt.TargetPreHead || fields[2] != receipt.Candidate {
		return mergeReceipt{}, false, nil
	}
	return receipt, true, nil
}

// existingGitMergeReceipt looks for a complete receipt for this approval. A
// commit carrying the approval trailer without the rest of the receipt is an
// older merge, not a failure: it is skipped so the durable receipt check gives
// the already-used refusal that actually describes the situation.
func existingGitMergeReceipt(ctx context.Context, checkout, approval string) (mergeReceipt, bool, error) {
	heads, err := git(ctx, checkout, "log", "--all", "--fixed-strings", "--grep="+mergeApprovalTrailer+approval, "--format=%H")
	if err != nil {
		return mergeReceipt{}, false, err
	}
	for _, head := range strings.Fields(heads) {
		receipt, ok, err := readMergeReceipt(ctx, checkout, head)
		if err != nil {
			return mergeReceipt{}, false, err
		}
		if ok && receipt.Approval == approval {
			return receipt, true, nil
		}
	}
	return mergeReceipt{}, false, nil
}

// validateMerge refuses retirement, ineffectiveness, and a record that still
// describes a superseded world. Ordinary reasoning staleness is evidence, not
// a veto: the exact immutable head was reviewed, and the merge receipt records
// what moved underneath its argument. A world-stale artifact still needs
// re-anchoring because its description follows implementation that was
// replaced; repeating the same review cannot repair that provenance.
// requireApprovedImplementer refuses a merge signed by anyone but the actor
// whose approved work is landing.
//
// It is the same boundary the fold applies to a merge receipt, moved to where
// it can still be obeyed. The fold refuses a receipt signed by anyone else, but
// it only sees the receipt, and by then Git has committed and `HEAD` has moved:
// the succession is stranded and the single-use approval is spent. Any
// participant could do that with a public ratified approval. Checking the same
// fingerprint before the merge begins turns an irreversible half-merge into an
// ordinary refusal.
//
// Only that fingerprint, and no role. A role is live standing, and standing can
// be revoked between this check and the acts it authorizes: while the tentative
// merge runs, or while the succession batch appends one act at a time. The fold
// would then refuse what this let through, after `HEAD` had already moved —
// exactly the outcome this check exists to remove, reached by a different door.
// The author of an approved artifact is a fact about a record that has already
// happened, so it cannot be revoked out from under a merge in flight. A merge
// path for anyone else needs an authorization that survives concurrent
// revocation, and that is a design, not a clause.
func requireApprovedImplementer(projection workroom.Projection, approvalEvent, merger string) error {
	if merger == "" {
		return errors.New("merge needs the signing actor's fingerprint")
	}
	review, found := projection.Review(approvalEvent)
	if !found || review.Implementer == "" {
		return errors.New("the record cannot say who implemented this approved head, so nobody may merge it on that approval")
	}
	if review.Implementer != merger {
		return fmt.Errorf("merge must be signed by the actor whose approved work is landing (%s); --as names %s",
			review.Implementer, merger)
	}
	return nil
}

func validateMerge(ctx context.Context, workspace *app.Workspace, checkout, candidate, approvalEvent string) (string, error) {
	if err := validateCheckout(ctx, workspace.Repo, checkout, candidate, false); err != nil {
		return "", err
	}
	if receipt, found, err := existingGitMergeReceipt(ctx, checkout, approvalEvent); err != nil {
		return "", err
	} else if found {
		return "", fmt.Errorf("approval was already used by merge %s into target pre-head %s", receipt.MergeHead, receipt.TargetPreHead)
	}
	snapshot, err := workspace.Snapshot(ctx)
	if err != nil {
		return "", err
	}
	projection := snapshot.Projection
	for _, statement := range projection.Statements {
		if statement.Body["merge_approval"] == approvalEvent && decisionEffective(projection, statement.Event) {
			return "", fmt.Errorf("approval already has durable merge receipt %s", statement.Event)
		}
	}
	approval, err := liveStatementAsOf(projection, approvalEvent, workroom.KindReport, verdictSequence(projection, approvalEvent))
	if err != nil {
		return "", fmt.Errorf("approval: %w", err)
	}
	if !approval.Ratified {
		return "", errors.New("approval report is not ratified by its requester")
	}
	if approval.Body["verdict"] != "approved" {
		return "", errors.New("review verdict is not approved")
	}
	if approval.Body["head"] != candidate {
		return "", fmt.Errorf("candidate %s does not equal approved head %s", candidate, approval.Body["head"])
	}
	artifactEvent := approval.Body["artifact"]
	if artifactEvent == "" || !slices.Contains(projection.Provenance[approvalEvent], artifactEvent) {
		return "", errors.New("approval does not rest on its named artifact")
	}
	artifact, err := liveArtifactAsOf(projection, artifactEvent, approval.Sequence)
	if err != nil {
		return "", fmt.Errorf("approval artifact: %w", err)
	}
	if artifact.Commit != candidate {
		return "", fmt.Errorf("approved artifact head %s does not equal candidate %s", artifact.Commit, candidate)
	}
	// The rule that review comes from a different agent is checked here rather
	// than assumed. An approval the projection cannot call independent does not
	// merge, whether because the reviewer implemented the head or because the
	// record cannot say who did.
	review, found := projection.Review(approvalEvent)
	if !found {
		return "", errors.New("approval is not projected as a review")
	}
	switch review.Independence {
	case workroom.IndependenceSelfReview:
		return "", errors.New("approval was signed by the actor who implemented this head; an independent review is required")
	case workroom.IndependenceIndependent:
		return reviewguard.StalenessNote(projection, []reviewguard.Part{
			{Name: "approval", Event: approval.Event, Stale: approval.Stale, World: approval.DescribesSupersededWorld},
			{Name: "artifact", Event: artifact.Event, Stale: artifact.Stale, World: artifact.DescribesSupersededWorld},
		}), nil
	default:
		return "", errors.New("the record cannot say whether this approval was independent; name the reviewed artifact in the review report")
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
	_, workroomCommon, err := apphost.ResolveGitDirs(ctx, workroomRepo)
	if err != nil {
		return err
	}
	_, checkoutCommon, err := apphost.ResolveGitDirs(ctx, checkout)
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

func validateArtifactCommit(ctx context.Context, repo, commit string) error {
	resolved, err := git(ctx, repo, "rev-parse", "--verify", "--end-of-options", commit+"^{commit}")
	if err != nil {
		return fmt.Errorf("artifact commit does not resolve to a commit: %w", err)
	}
	resolved = strings.TrimSpace(resolved)
	if commit != resolved {
		return fmt.Errorf("commit must be the full canonical object ID: got %s, resolved %s", commit, resolved)
	}
	return nil
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

// liveArtifact is merge-live: standing, and not describing a superseded world.
// Ordinary reasoning staleness is recorded by the merge receipt. It judges the
// world as of now, which is what a reviewer signing an artifact needs.
func liveArtifact(projection workroom.Projection, event string) (workroom.Artifact, error) {
	return liveArtifactAsOf(projection, event, math.MaxInt)
}

// liveArtifactAsOf is liveArtifact with the verdict's position in the log. A
// reviewer can only answer for the world they were shown: an artifact that
// already described a superseded world when they looked is a judgement that
// cannot be repaired by repeating it, and still refuses. A retirement landing
// afterwards is news, and news belongs in the receipt beside ordinary
// staleness, not in a refusal of a verdict that was sound when it was made.
//
// The date is the fold's, taken across every basis. Deriving it here would be a
// second copy of a rule the fold already owns, and the copy is what drifts.
func liveArtifactAsOf(projection workroom.Projection, event string, verdict int) (workroom.Artifact, error) {
	artifact, err := standingArtifact(projection, event)
	if err != nil {
		return workroom.Artifact{}, err
	}
	if artifact.DescribesSupersededWorld && !worldMovedAfterVerdict(artifact, verdict) {
		return workroom.Artifact{}, errors.New("artifact describes a superseded world")
	}
	return artifact, nil
}

// worldMovedAfterVerdict reads the fold's date and fails closed on its absence.
// Zero means the fold accounted for no active cause, and reading that as "after
// the verdict" would turn every projection this cannot date into a merge nobody
// reviewed.
func worldMovedAfterVerdict(artifact workroom.Artifact, verdict int) bool {
	return worldDatedAfter(artifact.WorldSupersededAt, verdict)
}

// verdictSequence is where a verdict sits in the log, or the end of the log if
// the projection cannot place it. An unplaceable verdict dates nothing, so it
// is treated as later than every retirement and the world staleness it was
// shown keeps refusing.
func verdictSequence(projection workroom.Projection, approval string) int {
	for _, decision := range projection.Decisions {
		if decision.Event == approval {
			return decision.Sequence
		}
	}
	return math.MaxInt
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

// liveStatement is merge-live: standing, and not describing a superseded
// world. Ordinary reasoning staleness is recorded by the merge receipt. It
// judges the world as of now, which is what a caller with no verdict to date
// against needs.
func liveStatement(projection workroom.Projection, event string, kind workroom.Kind) (workroom.Statement, error) {
	return liveStatementAsOf(projection, event, kind, math.MaxInt)
}

// liveStatementAsOf dates an approval's own superseded world the way
// liveArtifactAsOf dates the artifact's. An approval is a statement, so it
// carries the same published date, and refusing it undated while the artifact
// beside it is dated would refuse exactly the verdicts this change exists to
// admit: the approval and its artifact move together when a basis under both
// is retired after the verdict.
//
// A verdict cannot date itself, so the caller supplies the position. For the
// approval that position is its own, which is what makes a world that moved
// afterwards news rather than grounds for refusal.
func liveStatementAsOf(projection workroom.Projection, event string, kind workroom.Kind, verdict int) (workroom.Statement, error) {
	statement, err := standingStatement(projection, event, kind)
	if err != nil {
		return workroom.Statement{}, err
	}
	if statement.DescribesSupersededWorld && !worldDatedAfter(statement.WorldSupersededAt, verdict) {
		return workroom.Statement{}, errors.New("statement describes a superseded world")
	}
	return statement, nil
}

// worldDatedAfter reads the fold's date and fails closed on its absence. Zero
// means the fold accounted for no active cause, and reading that as "after the
// verdict" would turn every projection this cannot date into a merge nobody
// reviewed.
func worldDatedAfter(datedAt, verdict int) bool {
	return datedAt != 0 && datedAt > verdict
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
	serverFlag := set.String("server", "", "resident sequencer URL")
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
	serverURL, err := resolveServerURL(workspace, *serverFlag)
	if err != nil {
		return err
	}
	target := set.Arg(0)
	record, err := submitAct(ctx, workspace, serverURL, actor, app.Act{Verb: app.VerbRatify, Target: target, IdempotencyKey: *key})
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
	serverFlag := set.String("server", "", "resident sequencer URL")
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
	serverURL, err := resolveServerURL(workspace, *serverFlag)
	if err != nil {
		return err
	}
	target := set.Arg(0)
	record, err := submitAct(ctx, workspace, serverURL, actor, app.Act{Verb: app.VerbSupersede, Target: target, Text: *message, RestsOn: rests, IdempotencyKey: *key, CitedOK: *citedOK})
	if err != nil {
		return err
	}
	fmt.Println(record.ID)
	return nil
}

type reassignIfUnclaimedResult struct {
	Retirement string `json:"retirement"`
	Request    string `json:"request"`
}

// reassignIfUnclaimedCommand owns the two-act choreography so a caller cannot
// accidentally omit the old request or named retirement from the signed
// tuple. The stable key is required because the only honest recovery from a
// crash between acts is to replay the retirement exactly and continue.
func reassignIfUnclaimedCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("reassign-if-unclaimed", arguments)
	as := set.String("as", "", "requester actor name")
	to := set.String("to", "", "new requested performer")
	message := set.String("text", "", "replacement request text")
	conditions := set.String("conditions", "", "replacement conditions of satisfaction")
	retirementText := set.String("retirement-text", "retire unclaimed request before reassignment", "retirement reason")
	serverFlag := set.String("server", "", "resident sequencer URL")
	key := set.String("idempotency-key", "", "stable retry key (required)")
	citedOK := set.Bool("cited-ok", false, "retire even though documentation still cites the old request")
	var rests values
	set.Var(&rests, "rests-on", "additional current basis for the replacement request (repeatable)")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	if set.NArg() != 1 {
		return errors.New("reassign-if-unclaimed requires one old request event")
	}
	if *to == "" || *message == "" || *conditions == "" {
		return errors.New("reassign-if-unclaimed requires --to, --text, and --conditions")
	}
	if *key == "" {
		return errors.New("reassign-if-unclaimed requires --idempotency-key for two-act retry recovery")
	}
	actor, err := signingActor(*as)
	if err != nil {
		return err
	}
	workspace, err := app.Open(ctx, *repo)
	if err != nil {
		return err
	}
	serverURL, err := resolveServerURL(workspace, *serverFlag)
	if err != nil {
		return err
	}
	oldRequest := set.Arg(0)
	retirement, err := submitAct(ctx, workspace, serverURL, actor, app.Act{
		Verb: app.VerbRetireIfUnclaimed, Target: oldRequest, Text: *retirementText,
		IdempotencyKey: *key + "/retirement", CitedOK: *citedOK,
	})
	if err != nil {
		return err
	}
	replacement, err := submitAct(ctx, workspace, serverURL, actor, app.Act{
		Verb: app.VerbReassignIfUnclaimed, Target: oldRequest, Retirement: retirement.ID,
		Text: *message, Body: map[string]string{"to": *to, "conditions": *conditions},
		RestsOn: rests, IdempotencyKey: *key + "/request",
	})
	if err != nil {
		return fmt.Errorf("guarded retirement %s landed or replayed, but its replacement was refused: %w; re-read the old request before retrying", retirement.ID, err)
	}
	return printJSON(reassignIfUnclaimedResult{Retirement: retirement.ID, Request: replacement.ID})
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
	Retirement     string            `json:"retirement,omitempty"`
	RestsOn        []string          `json:"rests_on,omitempty"`
	IdempotencyKey string            `json:"idempotency_key,omitempty"`

	// AllowDeadBasis is the per-act form of gs state's --allow-dead-basis:
	// asking for it signs body.dead_basis_override=true on that act.
	AllowDeadBasis bool `json:"allow_dead_basis,omitempty"`
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
	serverFlag := set.String("server", "", "resident sequencer URL")
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
	serverURL, err := resolveServerURL(workspace, *serverFlag)
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
	report, err := runBatch(ctx, workspace, serverURL, *as, private, acts, *citedOK)
	noteBatchDeadRestsOn(ctx, workspace, acts, report)
	if printErr := printJSON(report); printErr != nil && err == nil {
		err = printErr
	}
	return err
}

// noteBatchDeadRestsOn gives every act a batch landed the same advisory the
// single state path gives, from one projection read for the whole chain. The
// labels a batch resolves internally never reach this function, so they are
// rebuilt from the report: an act that landed or replayed names its event, in
// order, which is exactly what runBatch minted as it went.
func noteBatchDeadRestsOn(ctx context.Context, workspace *app.Workspace, acts []batchAct, report batchReport) {
	minted := make(map[string]string)
	for _, outcome := range report.Acts {
		if outcome.Label != "" && outcome.Event != "" {
			minted[outcome.Label] = outcome.Event
		}
	}
	snapshot, err := workspace.Snapshot(ctx)
	if err != nil {
		return
	}
	for _, entry := range acts {
		resolved := make([]string, 0, len(entry.RestsOn))
		for _, reference := range entry.RestsOn {
			resolved = append(resolved, resolveLabel(reference, minted))
		}
		noteDeadRestsOn(snapshot.Projection, resolved)
	}
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
	// Before the first append, build every act through the same application
	// boundary submission uses: an undefined kind, a verdict shape, a spoofed
	// reserved field, or a dead basis stops the batch before anything lands.
	// The authoritative re-judgement still happens per act at sequencing.
	if position, failure := preflightAdmission(ctx, workspace, serverURL, actorName, private, acts, citedOK); failure != nil {
		report.Acts[position].Outcome = "failed"
		report.Error = failure
		return report, failure
	}
	minted := make(map[string]string, len(acts))
	for position, entry := range acts {
		act := resolveBatchAct(entry, minted, citedOK)
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

// preflightBatchAdmission constructs every request with the same application
// encoder used by submission, then checks the kernel and optional resident
// byte ceilings without appending. Intra-batch event IDs are not known yet;
// their canonical IDs always have the genesis hash width, so a valid dummy OID
// preserves the exact envelope and JSON lengths of the eventual references.
func preflightBatchAdmission(ctx context.Context, workspace *app.Workspace, serverURL, actorName string, private ed25519.PrivateKey, acts []batchAct, citedOK bool) error {
	if position, failure := checkBatch(acts); failure != nil {
		return fmt.Errorf("act %d: %w", position, failure)
	}
	if position, failure := preflightAdmission(ctx, workspace, serverURL, actorName, private, acts, citedOK); failure != nil {
		return fmt.Errorf("act %d: %w", position, failure)
	}
	return nil
}

// preflightAdmission builds every act through the application boundary and
// checks the kernel and optional resident byte ceilings without appending. It
// reports the position of the first refusal as a batch failure.
func preflightAdmission(ctx context.Context, workspace *app.Workspace, serverURL, actorName string, private ed25519.PrivateKey, acts []batchAct, citedOK bool) (int, *batchError) {
	if serverURL != "" {
		if _, err := residentclient.ValidateURL(serverURL); err != nil {
			return 0, batchFail("admission", "%v", err)
		}
	}
	view := workspace.View()
	syntheticEvent := workspace.EventID(strings.Repeat("0", len(view.Genesis)))
	minted := make(map[string]string, len(acts))
	for position, entry := range acts {
		act := resolveBatchAct(entry, minted, citedOK)
		request, err := workspace.BuildActRequest(ctx, private, actorName, act)
		if err != nil {
			return position, batchFail("admission", "%v", err)
		}
		if err := kernel.ValidateRequestSize(request, view.PayloadCeiling); err != nil {
			return position, batchFail("admission", "%v", err)
		}
		if serverURL != "" {
			if err := service.ValidateSubmissionRequestSize(request); err != nil {
				return position, batchFail("admission", "%v", err)
			}
		}
		if entry.Label != "" {
			minted[entry.Label] = syntheticEvent
		}
	}
	return 0, nil
}

func resolveBatchAct(entry batchAct, minted map[string]string, citedOK bool) app.Act {
	act := app.Act{
		Verb: entry.Verb, Kind: entry.Kind, Text: entry.Text, Body: entry.Body,
		Target: resolveLabel(entry.Target, minted), Retirement: resolveLabel(entry.Retirement, minted), IdempotencyKey: entry.IdempotencyKey,
		CitedOK: citedOK, AllowDeadBasis: entry.AllowDeadBasis,
	}
	for _, reference := range entry.RestsOn {
		act.RestsOn = append(act.RestsOn, resolveLabel(reference, minted))
	}
	return act
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
		case app.VerbRatify, app.VerbSupersede, app.VerbRetireIfUnclaimed:
			if entry.Target == "" {
				return position, batchFail("verb", "%s requires a target", entry.Verb)
			}
			if entry.Verb == app.VerbRetireIfUnclaimed && entry.Retirement != "" {
				return position, batchFail("verb", "%s cannot name a prior retirement", entry.Verb)
			}
		case app.VerbReassignIfUnclaimed:
			if entry.Target == "" || entry.Retirement == "" {
				return position, batchFail("verb", "%s requires target and retirement", entry.Verb)
			}
			if entry.Kind != "" {
				return position, batchFail("verb", "%s has the request kind fixed by its schema", entry.Verb)
			}
		default:
			return position, batchFail("verb", "unknown verb %q", entry.Verb)
		}
		references := append([]string{entry.Target, entry.Retirement}, entry.RestsOn...)
		for _, reference := range references {
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
		return app.Submission{}, explainLifecycleRefusal(err)
	}
	submission, err := submitRequest(ctx, workspace, serverURL, request)
	if err != nil {
		return app.Submission{}, explainLifecycleRefusal(err)
	}
	if act.Verb == app.VerbState {
		warnUndefinedKind(ctx, workspace, act.Kind)
	}
	return submission, nil
}

func explainLifecycleRefusal(err error) error {
	if err == nil || strings.Contains(err.Error(), "docs/reference/gs/state.md") {
		return err
	}
	message := err.Error()
	switch {
	case strings.Contains(message, "dangling promise has no request"):
		return fmt.Errorf("%w. Add exactly one live request event with --rests-on. See docs/reference/gs/state.md#citing", err)
	case strings.Contains(message, "report rests on the request while promise"),
		strings.Contains(message, "report cites a request other than the one its promise answers"),
		strings.Contains(message, "report requires exactly one effective promise"),
		strings.Contains(message, "report requires exactly one effective request"),
		strings.Contains(message, "report actor must be the promisor"),
		strings.Contains(message, "report requires exactly one effective promise or request"):
		return fmt.Errorf("%w. File against the one live promise you made; use the request directly only when you made no promise. See docs/reference/gs/state.md#citing", err)
	default:
		return err
	}
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

// noteDeadRestsOn tells an author, on standard error, which citations of the
// act they just filed were already dead when it landed — retired, stale, or
// themselves effective supersessions. The act stays either way: the note
// describes, it does not refuse, and an author may cite a dead event for
// reasons of their own that a refusal would overrule. Each line names the
// event id as written so it can be found again in a transcript.
func noteDeadRestsOn(projection workroom.Projection, restsOn []string) {
	dead := workroom.DeadBases(projection, restsOn)
	if len(dead) == 0 {
		return
	}
	said := make(map[string]bool, len(dead))
	for _, id := range restsOn {
		if reason, isDead := dead[id]; isDead && !said[id] {
			said[id] = true
			fmt.Fprintf(os.Stderr, "note: rests-on %s is already dead (%s)\n", id, reason)
		}
	}
}

// submitRequest sequences one signed act, and refuses rather than folding it
// locally when the resident does not answer. A silent local fallback would
// trade the second the resident costs for a whole-log fold nobody asked for,
// and now that the address is usually the repository's own advertisement
// rather than something the author typed, they would have no way to tell.
// Only a refused dial is definite enough to say nothing landed — the same
// asymmetry the ownership probe holds — so only that failure names the way
// out; any other refusal is reported as it came.
func submitRequest(ctx context.Context, workspace *app.Workspace, serverURL string, request kernel.Request) (app.Submission, error) {
	submission, err := residentclient.New(10*time.Second).Submit(ctx, workspace, serverURL, request)
	if errors.Is(err, syscall.ECONNREFUSED) {
		return app.Submission{}, fmt.Errorf("no resident is listening at %s, so nothing was appended: start one with `gs serve`, or pass --server %s to fold this act locally: %w", serverURL, localFold, err)
	}
	return submission, err
}

func statusCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("status", arguments)
	jsonOutput := set.Bool("json", false, "render JSON")
	all := set.Bool("all", false, "render complete human-readable status")
	serverFlag := set.String("server", "", "workroom URL including live state")
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
	serverURL, err := resolveServerURL(workspace, *serverFlag)
	if err != nil {
		return err
	}
	if serverURL != "" {
		if *jsonOutput || *all {
			status, remoteErr := fetchFullStatus(ctx, serverURL)
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
			summary, remoteErr := fetchSummary(ctx, workspace, serverURL)
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
	if serverURL != "" {
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

// localFold is the --server value that forces the local verified fold. It is
// not a URL, so no advertisement can ever collide with it.
const localFold = "-"

// resolveServerURL decides where one command acts. An explicit --server URL
// is honoured after loopback validation; the value "-" forces the local
// verified fold even when a resident is advertised; and an empty flag takes
// the address the repository itself publishes, so the resident a checkout
// already runs answers by default and a repository without one acts locally
// exactly as before.
//
// Only a genuinely missing record is absence. A record that is present and
// cannot be trusted — unreadable, oversized, not a record, addressless, or
// naming another workroom — is refused rather than ignored: it is an ordinary
// file any local process can write, and quietly folding a durable act locally
// because it was corrupt would hide both the tampering and the minutes it
// costs. The refusal names the way out, and it happens here, before any
// command reads a signing key or appends anything.
func resolveServerURL(workspace *app.Workspace, explicit string) (string, error) {
	if explicit == localFold {
		return "", nil
	}
	if explicit != "" {
		return residentclient.ValidateURL(explicit)
	}
	advertisement := workspace.ResidentAdvertisement()
	if advertisement.State == app.NoAdvertisement {
		return "", nil
	}
	if advertisement.State == app.AdvertisementUnusable {
		return "", fmt.Errorf("%w; pass --server %s to act locally instead", residentclient.UntrustedAdvertisement(advertisement.Reason), localFold)
	}
	validated, err := residentclient.ValidateURL(advertisement.URL)
	if err != nil {
		return "", fmt.Errorf("%w; pass --server %s to act locally instead", residentclient.UnusableAdvertisedURL(advertisement.URL, err), localFold)
	}
	return validated, nil
}

// requireLocalAuthorityWrite keeps the authority and custody commands on the
// same writer boundary as every other durable command. Their operations span
// more than one append, and actor-add and actor-retire also change local key
// custody, so they have no single resident request to submit. An advertised or
// explicit resident must therefore stop them before any local mutation. The
// same explicit sentinel as the other commands lets an operator choose the
// local path deliberately.
func requireLocalAuthorityWrite(workspace *app.Workspace, explicit, command string) error {
	serverURL, err := resolveServerURL(workspace, explicit)
	if err != nil {
		return err
	}
	if serverURL != "" {
		return fmt.Errorf("gs %s has no resident write path; pass --server %s to act locally instead", command, localFold)
	}
	return nil
}

func fetchSummary(ctx context.Context, workspace *app.Workspace, raw string) (service.SummaryStatus, error) {
	before, err := workspace.Store.Head(ctx, kernel.Ref(workspace.View().Genesis))
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
	current, err := workspace.Store.Head(ctx, kernel.Ref(workspace.View().Genesis))
	if err != nil {
		return err
	}
	return validateRemoteFrontierAt(ctx, workspace, current, genesis, head)
}

func validateRemoteFrontierAt(ctx context.Context, workspace *app.Workspace, before, genesis, head string) error {
	expected := workspace.View().Genesis
	after, err := workspace.Store.Head(ctx, kernel.Ref(expected))
	if err != nil {
		return err
	}
	if genesis != expected {
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

// The bounded query commands below are CLI reach over selections that already
// exist. Each calls the same statusview builder the MCP tools and the resident
// HTTP routes call, so a selector means one thing whichever surface asks, and
// --json prints the page shape those surfaces already return. Their shared
// helpers and human renderers live in query.go; the entry points live here
// because this file is the command surface.

func workCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("work", arguments)
	as := set.String("as", "", "actor whose work is selected")
	var lanes values
	set.Var(&lanes, "lane", "relationship lane; repeat to name several (default all four)")
	var statuses values
	set.Var(&statuses, "status", "lifecycle status; repeat to name several")
	stale := set.String("stale", "", "staleness policy: summary, include, only, or exclude")
	limit := set.Int("limit", 0, "page size")
	cursor := set.String("cursor", "", "opaque continuation from a previous page")
	jsonOutput := set.Bool("json", false, "render JSON")
	serverFlag := set.String("server", "", "resident sequencer URL")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	if set.NArg() != 0 {
		return errors.New("work takes no positional arguments")
	}
	workspace, err := app.Open(ctx, *repo)
	if err != nil {
		return err
	}
	// A read still needs to know whose work it is. Opening the workroom first
	// lets the refusal name the identities whose keys this checkout actually
	// holds, rather than merely telling a novice that an identity is missing.
	actorName, err := signingActorFrom("--as", *as)
	if err != nil {
		return workIdentityRefusal(ctx, workspace, err)
	}
	serverURL, err := resolveServerURL(workspace, *serverFlag)
	if err != nil {
		return err
	}
	fingerprint := workspace.View().Actors[actorName].Fingerprint
	if fingerprint == "" {
		return workIdentityRefusal(ctx, workspace, fmt.Errorf("actor %q is not provisioned in this checkout", actorName))
	}
	query := statusview.WorkQuery{Actor: fingerprint, Statuses: statuses, Stale: statusview.StaleFilter(*stale), Limit: *limit, Cursor: *cursor}
	for _, lane := range lanes {
		query.Lanes = append(query.Lanes, statusview.WorkLane(lane))
	}
	var page statusview.WorkPage
	answered := false
	if serverURL != "" {
		answered = askResident(ctx, workspace, serverURL, "/v0/work-query", query, &page, func() statusview.Frontier { return page.Frontier })
	}
	if !answered {
		snapshot, err := snapshotWithProgress(ctx, workspace)
		if err != nil {
			return err
		}
		page, err = statusview.BuildWorkPage(snapshot, query, serverURL != "")
		if err != nil {
			return err
		}
	}
	if *jsonOutput {
		return printJSON(page)
	}
	_, err = os.Stdout.WriteString(renderWorkPage(page, querySource(serverURL != "", answered)))
	return err
}

func workIdentityRefusal(ctx context.Context, workspace *app.Workspace, cause error) error {
	actors, err := workspace.ActorViews(ctx)
	if err != nil {
		return fmt.Errorf("%w; local custody could not be read: %v", cause, err)
	}
	var names []string
	for _, actor := range actors {
		if actor.Custody && !actor.Retired {
			names = append(names, actor.Name)
		}
	}
	available := "none"
	if len(names) > 0 {
		available = strings.Join(names, ", ")
	}
	return fmt.Errorf("%w; local custody actors: %s. Choose one with --as or %s; run `gs whoami --repo %s` for details. See docs/reference/gs/whoami.md", cause, available, actorEnvironment, workspace.Repo)
}

func artifactsCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("artifacts", arguments)
	var paths values
	set.Var(&paths, "path", "exact artifact path; repeat to name several")
	state := set.String("state", "", "lifecycle state: live, retired, succeeded, or all")
	reaches := set.String("reaches", "", "select artifacts whose chain of artifact bases reaches an artifact at this exact path")
	limit := set.Int("limit", 0, "page size")
	cursor := set.String("cursor", "", "opaque continuation from a previous page")
	jsonOutput := set.Bool("json", false, "render JSON")
	serverFlag := set.String("server", "", "resident sequencer URL")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	if set.NArg() != 0 {
		return errors.New("artifacts takes no positional arguments")
	}
	workspace, err := app.Open(ctx, *repo)
	if err != nil {
		return err
	}
	serverURL, err := resolveServerURL(workspace, *serverFlag)
	if err != nil {
		return err
	}
	query := statusview.ArtifactSelection{Paths: paths, State: statusview.ArtifactState(*state), Reaches: *reaches, Limit: *limit, Cursor: *cursor}
	var page statusview.ArtifactPage
	answered := false
	var snapshot app.Snapshot
	degraded := false
	if serverURL != "" && *state == "" && *reaches == "" {
		wire := statusview.ArtifactQuery{Paths: paths, Limit: *limit, Cursor: *cursor}
		answered = askResident(ctx, workspace, serverURL, "/v0/artifact-query", wire, &page, func() statusview.Frontier { return page.Frontier })
	} else if serverURL != "" {
		// The extra selectors are deliberately CLI-only. Read the resident's
		// existing full snapshot instead of smuggling them through the bounded
		// HTTP request type and silently widening that protocol.
		status, remoteErr := fetchFullStatus(ctx, serverURL)
		if remoteErr == nil {
			remoteErr = validateRemoteFrontier(ctx, workspace, status.Durable.Genesis, status.Durable.Head)
		}
		if remoteErr == nil {
			snapshot = status.Durable
			answered = true
		} else {
			fmt.Fprintf(os.Stderr, "gs: resident status unavailable (%v); performing verified local fallback\n", remoteErr)
			degraded = true
		}
	}
	if !answered {
		degraded = degraded || serverURL != ""
		if snapshot.Head == "" {
			snapshot, err = snapshotWithProgress(ctx, workspace)
			if err != nil {
				return err
			}
		}
		page, err = statusview.BuildArtifactSelectionPage(snapshot, query, degraded)
		if err != nil {
			return err
		}
	} else if snapshot.Head != "" {
		page, err = statusview.BuildArtifactSelectionPage(snapshot, query, false)
		if err != nil {
			return err
		}
	}
	if *jsonOutput {
		return printJSON(page)
	}
	_, err = os.Stdout.WriteString(renderArtifactPage(page, querySource(serverURL != "", answered)))
	return err
}

// supersessionPlanCommand turns one complete bounded page of live artifacts
// at an exact path into input for gs batch. It refuses before printing if the
// selected population does not fit, so redirecting its JSON can never create a
// plausible-looking partial migration file.
func supersessionPlanCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("supersession-plan", arguments)
	path := set.String("path", "", "exact live artifact path to retire")
	message := set.String("text", "", "plain-language supersession reason")
	prefix := set.String("idempotency-prefix", "supersede-", "prefix joined to each target event")
	limit := set.Int("limit", statusview.ArtifactPageMax, "maximum complete plan size")
	jsonOutput := set.Bool("json", false, "emit a gs batch JSON array")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	if set.NArg() != 0 {
		return errors.New("supersession-plan takes no positional arguments")
	}
	if *path == "" || *message == "" {
		return errors.New("supersession-plan requires --path and --text")
	}
	workspace, err := app.Open(ctx, *repo)
	if err != nil {
		return err
	}
	snapshot, err := snapshotWithProgress(ctx, workspace)
	if err != nil {
		return err
	}
	page, err := statusview.BuildArtifactSelectionPage(snapshot, statusview.ArtifactSelection{
		Paths: []string{*path}, State: statusview.ArtifactStateLive, Limit: *limit,
	}, false)
	if err != nil {
		return err
	}
	plan, err := buildSupersessionPlan(page, *message, *prefix)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return printJSON(plan)
	}
	_, err = os.Stdout.WriteString(renderSupersessionPlan(page.Frontier, *path, plan))
	return err
}

func stalenessWaveCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("staleness-wave", arguments)
	path := set.String("path", "", "exact artifact path whose causal wave is measured")
	jsonOutput := set.Bool("json", false, "render JSON")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	if set.NArg() != 0 || *path == "" {
		return errors.New("staleness-wave requires --path and takes no positional arguments")
	}
	workspace, err := app.Open(ctx, *repo)
	if err != nil {
		return err
	}
	snapshot, err := snapshotWithProgress(ctx, workspace)
	if err != nil {
		return err
	}
	wave, err := statusview.BuildStalenessWave(snapshot, *path, false)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return printJSON(wave)
	}
	_, err = os.Stdout.WriteString(renderStalenessWave(wave, "verified local"))
	return err
}

func inspectCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("inspect", arguments)
	jsonOutput := set.Bool("json", false, "render JSON")
	serverFlag := set.String("server", "", "resident sequencer URL")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	if set.NArg() != 1 {
		return errors.New("inspect requires one event")
	}
	event := set.Arg(0)
	workspace, err := app.Open(ctx, *repo)
	if err != nil {
		return err
	}
	serverURL, err := resolveServerURL(workspace, *serverFlag)
	if err != nil {
		return err
	}
	var inspection statusview.ItemInspection
	answered := false
	if serverURL != "" {
		answered = askResident(ctx, workspace, serverURL, "/v0/inspect", statusview.InspectRequest{Event: event}, &inspection, func() statusview.Frontier { return inspection.Frontier })
	}
	if !answered {
		snapshot, err := snapshotWithProgress(ctx, workspace)
		if err != nil {
			return err
		}
		inspection, err = statusview.BuildItemInspection(snapshot, event, serverURL != "")
		if err != nil {
			if err.Error() == "event is not in the durable projection" {
				return fmt.Errorf("%w. Event IDs must use the full git:sha1:<genesis>#git:sha1:<event> form (or this repository's object format); #N is a display index only and does not resolve. Copy the full ID from `gs work --json` or other --json output", err)
			}
			return err
		}
	}
	if *jsonOutput {
		return printJSON(inspection)
	}
	_, err = os.Stdout.WriteString(renderInspection(inspection, querySource(serverURL != "", answered)))
	return err
}

// reviewsCommand answers the runbook's quiet-wave precondition: no review
// request still waiting for its first verdict, and no approval still out of a
// named branch. Both halves are one gate, so they are one command; separating
// them invites running the irreversible step on half an answer.
//
// It prints the whole report and then refuses if the gate is not quiet, so a
// shell can gate on the exit status without reading the text, and a person
// reading the text can see exactly what is outstanding.
func reviewsCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("reviews", arguments)
	branch := set.String("branch", "main", "branch an approved head must already be an ancestor of")
	checkout := set.String("checkout", "", "checkout whose Git history answers the ancestry question (default --repo)")
	limit := set.Int("limit", 0, "how many events to name under each count")
	jsonOutput := set.Bool("json", false, "render JSON")
	serverFlag := set.String("server", "", "resident sequencer URL")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	if set.NArg() != 0 {
		return errors.New("reviews takes no positional arguments")
	}
	if *checkout == "" {
		*checkout = *repo
	}
	workspace, err := app.Open(ctx, *repo)
	if err != nil {
		return err
	}
	serverURL, err := resolveServerURL(workspace, *serverFlag)
	if err != nil {
		return err
	}
	// There is no resident route for this gate, and inventing one would change
	// a surface this change is not allowed to change. The resident's existing
	// complete projection is read instead, and the same builder folds it.
	snapshot := app.Snapshot{}
	degraded := false
	if serverURL != "" {
		status, remoteErr := fetchFullStatus(ctx, serverURL)
		if remoteErr == nil {
			remoteErr = validateRemoteFrontier(ctx, workspace, status.Durable.Genesis, status.Durable.Head)
		}
		if remoteErr == nil {
			snapshot = status.Durable
		} else {
			fmt.Fprintf(os.Stderr, "gs: resident status unavailable (%v); performing verified local fallback\n", remoteErr)
			degraded = true
		}
	}
	if snapshot.Head == "" {
		snapshot, err = snapshotWithProgress(ctx, workspace)
		if err != nil {
			return err
		}
	}
	gate, err := statusview.BuildReviewGate(snapshot, *limit, degraded)
	if err != nil {
		return err
	}
	displayLimit := *limit
	if displayLimit == 0 {
		displayLimit = statusview.ReviewListDefault
	}
	allApprovedHeads := statusview.ActionableApprovedHeads(snapshot.Projection)
	landing, err := classifyHeads(ctx, *checkout, *branch, allApprovedHeads, displayLimit)
	if err != nil {
		return err
	}
	if *jsonOutput {
		if err := printJSON(struct {
			statusview.ReviewGate
			Branch         string   `json:"branch"`
			OutOf          []string `json:"approved_heads_out_of_branch"`
			OutOfTotal     int      `json:"approved_heads_out_of_branch_total"`
			OutOfOmitted   int      `json:"approved_heads_out_of_branch_omitted,omitempty"`
			Unknown        []string `json:"approved_heads_unknown_here"`
			UnknownTotal   int      `json:"approved_heads_unknown_here_total"`
			UnknownOmitted int      `json:"approved_heads_unknown_here_omitted,omitempty"`
			QuietGate      bool     `json:"quiet"`
		}{gate, *branch, landing.out, landing.outTotal, landing.outOmitted, landing.unknown, landing.unknownTotal, landing.unknownOmitted,
			gate.Quiet() && landing.outTotal == 0 && landing.unknownTotal == 0}); err != nil {
			return err
		}
	} else if _, err := os.Stdout.WriteString(renderReviewGate(gate, *branch, querySource(serverURL != "", !degraded && serverURL != ""), landing)); err != nil {
		return err
	}
	return landing.refusal(gate, *branch)
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

func checkpointClearCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("checkpoint-clear", arguments)
	if err := set.Parse(arguments); err != nil {
		return err
	}
	if set.NArg() != 0 {
		return errors.New("checkpoint-clear accepts no arguments")
	}
	workspace, err := app.Open(ctx, *repo)
	if err != nil {
		return err
	}
	if err := workspace.InvalidateCheckpoint(ctx); err != nil {
		return err
	}
	return printJSON(map[string]any{"genesis": workspace.View().Genesis, "checkpoint": "cleared"})
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
	if workspace.View().ReadOnly {
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
	// Bind before claiming, so the address in the ownership claim is the one
	// actually being served — including the port the kernel chose when the
	// listen address asked for any. Binding is not what authorizes serving;
	// ownership is, and it is taken next.
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	ownership, err := claimResidency(ctx, workspace, "http://"+listener.Addr().String())
	if err != nil {
		// Nothing was served and the port does not stay held. A refusing
		// process must leave the repository exactly as it found it.
		_ = listener.Close()
		return err
	}
	// Ownership is released only after the listener has stopped accepting, so
	// whoever wins the freed claim cannot find this process still answering on
	// the address the claim named.
	defer func() {
		_ = listener.Close()
		release, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		ownership.Release(release)
	}()
	withdraw, err := workspace.PublishResident("http://" + listener.Addr().String())
	if err != nil {
		return err
	}
	defer withdraw()
	fmt.Fprintf(os.Stderr, "gitseq workroom http://%s\n%s\n", listener.Addr(), service.TrustedProcessPosture)
	httpServer := residentHTTPServer(residentHTTPHandler(telemetryRuntime.Handler(service.TrustedHostHandler(server.Handler()))))
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

// residentProbeTimeout bounds the liveness question asked of an existing
// claim. It is short because the answer is served from configuration and a
// healthy resident returns it immediately; a resident that cannot answer this
// quickly is ambiguous, and ambiguity leaves the claim alone.
const residentProbeTimeout = 2 * time.Second

const (
	residentReadHeaderTimeout = 5 * time.Second
	residentReadTimeout       = 10 * time.Second
	residentWriteTimeout      = 40 * time.Second
	residentIdleTimeout       = 60 * time.Second
	residentMaxHeaderBytes    = 64 << 10
)

func residentHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: residentReadHeaderTimeout,
		ReadTimeout:       residentReadTimeout,
		WriteTimeout:      residentWriteTimeout,
		IdleTimeout:       residentIdleTimeout,
		MaxHeaderBytes:    residentMaxHeaderBytes,
	}
}

// residentHTTPHandler removes the connection-wide response deadline only for
// /v0/status. A cold verified rebuild can legitimately take longer than the
// ordinary response budget, and abandoning the client response does not stop
// that shared rebuild. Every other route keeps the server's write deadline.
func residentHTTPHandler(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/v0/status" {
			if err := http.NewResponseController(writer).SetWriteDeadline(time.Time{}); err != nil {
				http.Error(writer, "status response deadline could not be cleared", http.StatusInternalServerError)
				return
			}
		}
		handler.ServeHTTP(writer, request)
	})
}

// claimResidency takes the repository's one resident claim before anything is
// served. The probe that decides whether an existing claim still has a service
// behind it lives in the resident client, which is the one place that knows how
// to speak to a resident and which addresses are safe to dial at all.
func claimResidency(ctx context.Context, workspace *app.Workspace, url string) (*app.ResidentOwnership, error) {
	client := residentclient.New(residentProbeTimeout)
	return workspace.ClaimResident(ctx, url, func(parent context.Context, claim app.ResidentClaim) app.Liveness {
		probe, cancel := context.WithTimeout(parent, residentProbeTimeout)
		defer cancel()
		return client.ProbeResident(probe, claim)
	})
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
	server := residentHTTPServer(mux)
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

var lookupIP = net.LookupIP

func validateLoopbackListen(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid --listen address: %w", err)
	}
	if host == "" {
		return errors.New("--listen must name a loopback address; the resident service is a trusted local multi-actor custodian")
	}
	if ip := net.ParseIP(host); ip != nil {
		if !ip.IsLoopback() {
			return errors.New("--listen must name a loopback address; the resident service is a trusted local multi-actor custodian")
		}
		return nil
	}
	addresses, err := lookupIP(host)
	if err != nil || len(addresses) == 0 {
		return errors.New("--listen must resolve only to loopback addresses; the resident service is a trusted local multi-actor custodian")
	}
	for _, resolved := range addresses {
		if !resolved.IsLoopback() {
			return errors.New("--listen must resolve only to loopback addresses; the resident service is a trusted local multi-actor custodian")
		}
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
	if err := validateConfiguredRemote(ctx, repo, remote); err != nil {
		return err
	}
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
	if _, err := git(ctx, repo, "fetch", "--atomic", "--no-tags", "--", remote, sequenceFetchRefspec); err != nil {
		return fmt.Errorf("fetch sequence refs without rewind: %w", err)
	}
	return nil
}

func validateConfiguredRemote(ctx context.Context, repo, remote string) error {
	if remote == "" || strings.HasPrefix(remote, "-") || strings.Contains(remote, "::") || strings.Contains(remote, "://") || filepath.IsAbs(remote) {
		return fmt.Errorf("--remote must name a configured Git remote, got %q", remote)
	}
	if _, err := git(ctx, repo, "remote", "get-url", "--", remote); err != nil {
		return fmt.Errorf("--remote %q is not a configured Git remote: %w", remote, err)
	}
	return nil
}

func git(ctx context.Context, repo string, arguments ...string) (string, error) {
	args := make([]string, 0, len(arguments)+3)
	args = append(args, "--no-replace-objects", "-C", repo)
	args = append(args, arguments...)
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
