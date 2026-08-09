package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gitseq/spike/internal/app"
	"gitseq/spike/internal/service"
	"gitseq/spike/internal/workroom"
)

type values []string

func (v *values) String() string { return strings.Join(*v, ",") }
func (v *values) Set(value string) error {
	*v = append(*v, value)
	return nil
}

// actorEnvironment is how a concurrent instance is told which provisioned
// identity it is. Every signing command reads it when --as is absent.
const actorEnvironment = "GITSEQ_ACTOR"

// signingActor resolves the identity an act is signed with. There is no
// default name: the default was a name that several concurrent instances
// shared, which made the log attribute to a group what one instance did.
func signingActor(flagValue string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	if name := strings.TrimSpace(os.Getenv(actorEnvironment)); name != "" {
		return name, nil
	}
	return "", errors.New("no actor identity: pass --as, or set " + actorEnvironment + " to the identity this instance signs as")
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	ctx := context.Background()
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
		fmt.Fprintln(os.Stderr, "gs:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: gs <init|actor-add|actor-retire|role-grant|role-revoke|actors|state|review|merge|ratify|supersede|status|provenance|verify|serve|attach> [flags]")
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
	operator := set.String("operator", "operator", "operator actor name")
	ceiling := set.Uint64("payload-ceiling", 1<<20, "inline payload ceiling")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	workspace, seed, err := app.Init(ctx, *repo, *operator, *ceiling)
	if err != nil {
		return err
	}
	return printJSON(map[string]any{"genesis": workspace.Config.Genesis, "operator": workspace.Config.Actors[*operator], "seed": seed.ID})
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

type reviewValidator func(context.Context, *app.Workspace, string, string, string, string) (string, string, error)

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
	reviewedHead, request, err := validate(ctx, workspace, reviewer, *checkout, *artifact, *promise)
	if err != nil {
		return err
	}
	// Re-read immediately before signing. The verdict names the immutable
	// commit, so a later checkout movement cannot retarget it.
	if head, repeatedRequest, err := validate(ctx, workspace, reviewer, *checkout, *artifact, *promise); err != nil {
		return err
	} else if head != reviewedHead || repeatedRequest != request {
		return errors.New("review basis changed while validating")
	}
	record, err := submitAct(ctx, workspace, *serverURL, reviewer, app.Act{
		Verb: app.VerbState, Kind: workroom.KindReport, Text: *message,
		Body:    map[string]string{"verdict": *verdict, "head": reviewedHead, "artifact": *artifact},
		RestsOn: []string{*promise, request, *artifact}, IdempotencyKey: *key,
	})
	if err != nil {
		return err
	}
	fmt.Println(record.ID)
	return nil
}

func mergeCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("merge", arguments)
	checkout := set.String("checkout", "", "checkout receiving the merge")
	candidate := set.String("candidate", "", "full approved commit ID")
	approval := set.String("approval", "", "ratified approval report event")
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
	// Repeat the durable and local checks directly before invoking Git. The
	// merge argument remains the approved object ID, never a movable ref.
	if err := validateMerge(ctx, workspace, *checkout, *candidate, *approval); err != nil {
		return err
	}
	if _, err := git(ctx, *checkout, "merge", "--no-ff", "--no-edit", "--", *candidate); err != nil {
		return err
	}
	head, err := git(ctx, *checkout, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	fmt.Println(strings.TrimSpace(head))
	return nil
}

func validateReview(ctx context.Context, workspace *app.Workspace, actorName, checkout, artifactEvent, promiseEvent string) (string, string, error) {
	snapshot, err := workspace.Snapshot(ctx)
	if err != nil {
		return "", "", err
	}
	projection := snapshot.Projection
	artifact, err := liveArtifact(projection, artifactEvent)
	if err != nil {
		return "", "", err
	}
	implementation, err := liveStatement(projection, artifactEvent, workroom.KindArtifact)
	if err != nil {
		return "", "", fmt.Errorf("reviewed artifact: %w", err)
	}
	promise, err := liveStatement(projection, promiseEvent, workroom.KindPromise)
	if err != nil {
		return "", "", err
	}
	actor, _, err := workspace.Actor(actorName)
	if err != nil {
		return "", "", err
	}
	if promise.Actor != actor.Fingerprint {
		return "", "", errors.New("review actor did not make the named promise")
	}
	// Independence is a property of fingerprints, not of names. Refusing here
	// keeps the self-signed verdict out of the log; the projection still
	// reports independence for verdicts written any other way.
	if implementation.Actor == actor.Fingerprint {
		return "", "", errors.New("review actor signed the artifact under review; an independent reviewer must sign the verdict")
	}
	request, err := uniqueLiveBasis(projection, promiseEvent, workroom.KindRequest)
	if err != nil {
		return "", "", fmt.Errorf("review promise: %w", err)
	}
	if err := validateCheckout(ctx, workspace.Repo, checkout, artifact.Commit, true); err != nil {
		return "", "", err
	}
	return artifact.Commit, request.Event, nil
}

func validateMerge(ctx context.Context, workspace *app.Workspace, checkout, candidate, approvalEvent string) error {
	if err := validateCheckout(ctx, workspace.Repo, checkout, candidate, false); err != nil {
		return err
	}
	snapshot, err := workspace.Snapshot(ctx)
	if err != nil {
		return err
	}
	projection := snapshot.Projection
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

func liveArtifact(projection workroom.Projection, event string) (workroom.Artifact, error) {
	if !decisionEffective(projection, event) {
		return workroom.Artifact{}, errors.New("artifact is not effective")
	}
	for _, artifact := range projection.Artifacts {
		if artifact.Event == event {
			if artifact.Stale {
				return workroom.Artifact{}, errors.New("artifact is stale or retired")
			}
			return artifact, nil
		}
	}
	return workroom.Artifact{}, errors.New("artifact event is unknown")
}

func liveStatement(projection workroom.Projection, event string, kind workroom.Kind) (workroom.Statement, error) {
	if !decisionEffective(projection, event) {
		return workroom.Statement{}, errors.New("statement is not effective")
	}
	for _, statement := range projection.Statements {
		if statement.Event == event {
			if statement.Kind != kind {
				return workroom.Statement{}, fmt.Errorf("statement is %s, want %s", statement.Kind, kind)
			}
			if statement.Retired || statement.Stale {
				return workroom.Statement{}, errors.New("statement is stale or retired")
			}
			return statement, nil
		}
	}
	return workroom.Statement{}, errors.New("statement event is unknown")
}

func uniqueLiveBasis(projection workroom.Projection, event string, kind workroom.Kind) (workroom.Statement, error) {
	var found []workroom.Statement
	for _, basis := range projection.Provenance[event] {
		statement, err := liveStatement(projection, basis, kind)
		if err == nil {
			found = append(found, statement)
		}
	}
	if len(found) != 1 {
		return workroom.Statement{}, fmt.Errorf("expected one live %s basis, found %d", kind, len(found))
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
	record, err := submitAct(ctx, workspace, *serverURL, actor, app.Act{Verb: app.VerbSupersede, Target: target, Text: *message, RestsOn: rests, IdempotencyKey: *key})
	if err != nil {
		return err
	}
	fmt.Println(record.ID)
	return nil
}

func submitAct(ctx context.Context, workspace *app.Workspace, serverURL, actorName string, act app.Act) (workroom.Record, error) {
	if serverURL == "" {
		submission, err := workspace.Act(ctx, actorName, act)
		return submission.Record, err
	}
	_, private, err := workspace.Actor(actorName)
	if err != nil {
		return workroom.Record{}, err
	}
	request, err := workspace.BuildActRequest(ctx, private, actorName, act)
	if err != nil {
		return workroom.Record{}, err
	}
	encoded, _ := json.Marshal(request)
	response, err := http.Post(strings.TrimRight(serverURL, "/")+"/v0/submit", "application/json", bytes.NewReader(encoded))
	if err != nil {
		return workroom.Record{}, err
	}
	defer response.Body.Close()
	var result struct {
		app.Submission
		Error string `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return workroom.Record{}, err
	}
	if response.StatusCode != http.StatusOK {
		return workroom.Record{}, errors.New(result.Error)
	}
	return result.Record, nil
}

func statusCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("status", arguments)
	jsonOutput := set.Bool("json", false, "render JSON")
	serverURL := set.String("server", "", "workroom URL including live state")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	if *serverURL != "" {
		response, err := http.Get(strings.TrimRight(*serverURL, "/") + "/v0/status")
		if err != nil {
			return err
		}
		defer response.Body.Close()
		_, err = io.Copy(os.Stdout, response.Body)
		return err
	}
	workspace, err := app.Open(ctx, *repo)
	if err != nil {
		return err
	}
	snapshot, err := workspace.Snapshot(ctx)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return printJSON(snapshot)
	}
	_, err = os.Stdout.Write(workroom.RenderStatus(snapshot.Projection))
	return err
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
	server, err := service.New(workspace)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "gitseq workroom http://%s\n", *listen)
	httpServer := &http.Server{Addr: *listen, Handler: server.Handler(), ReadHeaderTimeout: 5 * time.Second}
	return httpServer.ListenAndServe()
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
