// Command gitseq-github observes GitHub issues into a workroom.
//
// It is a separate process holding its own key and submitting through the same
// public surface every other actor uses. The core never learns what GitHub is,
// and nothing here reaches into the kernel or the fold.
//
// It observes nothing by default. What it observes is decided by admission
// clauses in the log, stated by an operator, and every observation it appends
// rests on the charter and on the clause that admitted it — so retiring a
// clause flares everything it let in.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/connector/github"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/residentclient"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "gitseq-github:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, arguments []string) error {
	set := flag.NewFlagSet("gitseq-github", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	repo := set.String("repo", ".", "repository holding the workroom")
	actor := set.String("as", "", "the connector's workroom actor; defaults to "+residentclient.ActorEnvironment)
	charter := set.String("charter", "", "the ratified charter event this connector acts under")
	owner := set.String("owner", "", "GitHub owner")
	name := set.String("repo-name", "", "GitHub repository name")
	server := set.String("server", "", "submit through a resident sequencer instead of writing locally")
	dry := set.Bool("dry-run", false, "report what would be observed without appending")
	propose := set.Int("propose", 0, "open a pull request fixing this observed issue number")
	branch := set.String("branch", "", "the branch carrying the work, for --propose")
	base := set.String("base", "main", "the branch the pull request should merge into")
	commit := set.String("commit", "", "the exact head under review, for --propose")
	governing := set.String("request", "", "the gitseq request governing the work, for --propose")
	artifact := set.String("artifact", "", "the artifact naming the exact head, for --propose")
	title := set.String("title", "", "the pull request title, for --propose")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	if *charter == "" {
		return errors.New("--charter is required: the connector may not append without a ratified charter to rest on")
	}
	if *owner == "" || *name == "" {
		return errors.New("--owner and --repo-name are required")
	}
	actorName, err := residentclient.ResolveActor("--as", *actor)
	if err != nil {
		return err
	}

	// The token is handed to the process. The connector has no GitHub identity
	// of its own, so whatever it writes appears as the token's owner, and its
	// reach is the token's reach rather than the charter's scope.
	token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))

	workspace, err := app.Open(ctx, *repo)
	if err != nil {
		return err
	}
	snapshot, err := workspace.Snapshot(ctx)
	if err != nil {
		return err
	}

	// The operation is decided before the doorstep, so the doorstep can judge
	// it. Asking "may I act here?" without saying which act was the gap: any
	// live inbound charter answered yes to opening a pull request.
	operation := OperationObserve
	if *propose != 0 {
		operation = OperationPropose
	}
	if err := charterIsLive(snapshot.Projection, *charter, *owner, *name, actorName, operation); err != nil {
		return err
	}

	if *propose != 0 {
		if err := proposalIsCoherent(snapshot.Projection, *governing, *artifact, *commit); err != nil {
			return err
		}
		return proposePullRequest(ctx, github.NewClient(token), *owner, *name, *dry, proposalFlags{
			issue: *propose, branch: *branch, base: *base, commit: *commit,
			request: *governing, artifact: *artifact, title: *title,
		})
	}

	connectorActor, err := workspace.ResolveActorAddress(actorName)
	if err != nil {
		return err
	}

	reading := github.ClausesFrom(clauseSources(snapshot.Projection), authors(snapshot.Projection), *charter)
	clauses := reading.Clauses
	if len(clauses) == 0 {
		// Not an error. A workroom with no clause has asked for nothing, and
		// observing anyway would be the connector deciding its own admission.
		//
		// But say which of those two happened. "No live admission clause" alone
		// reads identically whether the operator stated nothing or stated
		// something the connector threw away, and that ambiguity once had me
		// report a live ratified charter as missing.
		if len(reading.Refusals) == 0 {
			fmt.Println("no admission clause has been stated; nothing to observe")
			return nil
		}
		fmt.Printf("no admission clause was honoured; %d considered and refused:\n", len(reading.Refusals))
		for _, refusal := range reading.Refusals {
			fmt.Printf("  %s: %s\n", refusal.Event, refusal.Reason)
		}
		return nil
	}
	for _, refusal := range reading.Refusals {
		fmt.Printf("clause %s refused: %s\n", refusal.Event, refusal.Reason)
	}

	// The clauses decide the read. Nothing here enumerates the tracker, so a
	// repository costs what its clauses ask for rather than what strangers have
	// filed in it.
	client := github.NewClient(token)
	admitted, missing, err := github.Fetch(ctx, client, *owner, *name, clauses)
	if err != nil {
		return err
	}
	for _, number := range missing {
		fmt.Printf("clause names %s/%s#%d, which GitHub did not return\n", *owner, *name, number)
	}

	seen := github.Fold(observedStatements(snapshot.Projection), connectorActor.Fingerprint)
	fresh := github.Unobserved(admitted, seen)
	if len(fresh) == 0 {
		fmt.Printf("%d admitted by %d clauses, nothing new\n", len(admitted), len(clauses))
		return nil
	}

	for _, observation := range fresh {
		if *dry {
			fmt.Printf("would observe %s (admitted by %s)\n", observation.ExternalID, observation.AdmittedBy)
			continue
		}
		event, err := appendObservation(ctx, workspace, actorName, *server, *charter, observation)
		if err != nil {
			return fmt.Errorf("observing %s: %w", observation.ExternalID, err)
		}
		fmt.Printf("observed %s as %s\n", observation.ExternalID, event)
	}
	return nil
}

type proposalFlags struct {
	issue                                          int
	branch, base, commit, request, artifact, title string
}

// proposePullRequest opens one pull request for work that fixes an observed
// issue. It runs after the charter check, so the same doorstep that bounds what
// the connector reads also bounds what it writes.
//
// Every field is required and none is inferred. A pull request that named the
// wrong head, or no durable request at all, would be a rendering that points
// somewhere other than the record it claims to render — and the reader on
// GitHub has no way to tell. Refusing here costs one error message; guessing
// costs the reader's trust in every rendering.
func proposePullRequest(ctx context.Context, client *github.Client, owner, name string, dry bool, flags proposalFlags) error {
	for label, value := range map[string]string{
		"--branch": flags.branch, "--base": flags.base, "--commit": flags.commit,
		"--request": flags.request, "--artifact": flags.artifact, "--title": flags.title,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required with --propose: a rendering that cannot name its own record is worse than none", label)
		}
	}

	rendered := github.Render(github.Proposal{
		Issue:    github.Issue{Owner: owner, Repo: name, Number: flags.issue},
		Request:  flags.request,
		Artifact: flags.artifact,
		Commit:   flags.commit,
		Branch:   flags.branch,
		Base:     flags.base,
		Title:    flags.title,
	})
	if dry {
		fmt.Printf("would open a pull request on %s/%s from %s into %s:\n\n%s\n",
			owner, name, rendered.Head, rendered.Base, rendered.Body)
		return nil
	}
	delivery, err := client.Open(ctx, rendered)
	if err != nil {
		return err
	}
	// Printed so the caller can carry it as evidence on the report that closes
	// the promise to deliver. Delivery is not the work; reporting it is.
	fmt.Printf("opened %s/%s#%d %s\n", owner, name, delivery.Number, delivery.URL)
	return nil
}

// proposalIsCoherent refuses to render durable facts that do not hold together.
//
// The command already required every field to be supplied. Supplied is not the
// same as true: three well-formed strings naming a request, an artifact and a
// commit that have nothing to do with each other produce a pull request which
// confidently points at a record that does not say what it claims. The reader
// on GitHub has no way to tell, and a pull request cannot be superseded.
//
// So the relationship is checked against the projection before anything is
// posted, not the mere presence of the fields. Each mismatch is reported
// separately because they mean different things: an unknown event is a citation
// error, a retired one is a staleness error, and an artifact naming another
// commit is a claim about the wrong code.
func proposalIsCoherent(projection workroom.Projection, request, artifact, commit string) error {
	if _, err := effectiveStatement(projection, request, "request"); err != nil {
		return err
	}
	if _, err := effectiveStatement(projection, artifact, "artifact"); err != nil {
		return err
	}
	for _, candidate := range projection.Artifacts {
		if candidate.Event != artifact {
			continue
		}
		if candidate.Commit != commit {
			return fmt.Errorf("artifact %s names commit %s, not the --commit %s this would render", artifact, candidate.Commit, commit)
		}
		// The artifact must belong to the work the request governs, or the
		// rendering pairs a real request with a real artifact from elsewhere.
		if !slices.Contains(projection.Provenance[artifact], request) {
			return fmt.Errorf("artifact %s does not rest on request %s, so they are not the same piece of work", artifact, request)
		}
		return nil
	}
	return fmt.Errorf("%s is not an artifact in this workroom", artifact)
}

// effectiveStatement finds one statement of a kind that this workroom actually
// stands behind, and is the single lookup both sides of a proposal go through.
//
// Presence in Projection.Statements is not force. The fold keeps ineffective
// acts there deliberately, because the log records what was said and not only
// what carried; it is the semantic projections, Artifacts among them, that
// filter down to the effective ones. So checking retirement and staleness alone
// would admit a request the workroom never gave force to and render it on a
// public pull request as though it governed the work. The artifact side would
// have been caught later by the Artifacts scan, which only ever contains
// effective rows — but that is an accident of which projection happens to carry
// the commit, not a check, and it protected one side while leaving the other
// open. Both go through here instead.
func effectiveStatement(projection workroom.Projection, event, kind string) (workroom.Statement, error) {
	for _, statement := range projection.Statements {
		if statement.Event != event {
			continue
		}
		if string(statement.Kind) != kind {
			return workroom.Statement{}, fmt.Errorf("%s is a %s, not a %s", event, statement.Kind, kind)
		}
		if statement.Retired {
			return workroom.Statement{}, fmt.Errorf("%s %s is retired", kind, event)
		}
		if statement.Stale {
			return workroom.Statement{}, fmt.Errorf("%s %s is stale, so what it rests on has moved", kind, event)
		}
		for _, decision := range projection.Decisions {
			if decision.Event != event {
				continue
			}
			// Only plain effect will do. Disputed and the unreadable verdicts
			// each mean the fold declined to give the act force, and none of
			// them is a basis for an irreversible public write.
			if decision.Verdict != workroom.Effective {
				return workroom.Statement{}, fmt.Errorf("the fold ruled %s %s %q (%s), so this workroom never gave it force", kind, event, decision.Verdict, decision.Reason)
			}
			return statement, nil
		}
		return workroom.Statement{}, fmt.Errorf("no fold decision records %s %s, so there is nothing saying it took effect", kind, event)
	}
	return workroom.Statement{}, fmt.Errorf("%s is not in this workroom, so a rendering naming it would point at nothing", event)
}

// charterIsLive refuses to act under a charter that is absent, retired,
// unratified, or that does not authorize this exact repository and connector
// actor. The fold does not know what a charter is, so this is the connector
// holding itself to its own contract — detection at the door rather than
// attribution afterwards.
//
// Staleness is among the refusals, and an earlier version of this file removed
// it on a premise that turned out to be false.
//
// The argument for removing it was that a charter replacing another must cite
// what it replaces, so every correctly replaced charter is stale by
// construction and refusing here would leave no charter an operator could
// state. That is not how replacement works. A successor rests on stable
// current governance; the separate supersession act rests first on the
// predecessor and names the successor as its additional basis — the same
// separation this repository already requires for artifact succession. The one
// stale charter in this workroom is stale because its motivating request was
// retired, not because it replaced anything.
//
// So the refusal stays, and it fails closed. A stale charter is one whose basis
// has moved, and the thing that moved might be exactly the scope request an
// operator withdrew. Admitting it would convert a flare into continuing
// authority for an irreversible public write, and a warning printed after
// admission re-authorizes nothing and asks nobody to acknowledge anything.
//
// The binding matters as much as the liveness. A charter that names no
// repository authorizes nothing in particular, and accepting one would let this
// command observe any repository at all under some unrelated ratified
// statement while claiming a doorstep it does not have. So a charter must say
// what it charters, and a charter that does not is refused rather than read
// generously.
func charterIsLive(projection workroom.Projection, charter, owner, name, actor, operation string) error {
	for _, statement := range projection.Statements {
		if statement.Event != charter {
			continue
		}
		if statement.Retired {
			return errors.New("charter is retired")
		}
		if statement.Stale {
			return errors.New("charter is stale: a basis underneath it has moved, so its authority is no longer established; state a successor on a current basis rather than acting under this one")
		}
		if !statement.Ratified {
			return errors.New("charter is not ratified")
		}
		return charterBinds(statement.Body, charter, owner, name, actor, operation)
	}
	return fmt.Errorf("charter %s is not in this workroom", charter)
}

// charterBinds checks the charter body against what this process was told to
// do. Each mismatch is reported separately because they mean different things:
// the wrong repository is a scope error, the wrong actor is an identity error,
// and a body that says nothing is a charter that never bounded anything.
func charterBinds(body map[string]string, charter, owner, name, actor, operation string) error {
	if len(body) == 0 {
		return fmt.Errorf("charter %s names no connector, repository or actor, so it authorizes nothing in particular", charter)
	}
	if got := body["connector"]; got != github.ClauseConnector {
		return fmt.Errorf("charter is for connector %q, not %q", got, github.ClauseConnector)
	}
	if got := body["owner"]; got != owner {
		return fmt.Errorf("charter authorizes owner %q, not %q", got, owner)
	}
	if got := body["repo"]; got != name {
		return fmt.Errorf("charter authorizes repository %q, not %q", got, name)
	}
	if got := body["actor"]; got != actor {
		return fmt.Errorf("charter authorizes actor %q, not %q", got, actor)
	}
	return charterAuthorizes(body, charter, operation)
}

// Operations a charter can authorize. Reading and writing are different powers
// and are named separately, because a charter that admits foreign issues into
// the log is not thereby a licence to publish on the same forge under the
// project's name.
const (
	OperationObserve = "observe"
	OperationPropose = "propose"
)

// charterAuthorizes decides whether this charter permits the operation about to
// be performed.
//
// A charter that says nothing about operations authorizes observation and
// nothing else. That asymmetry is the point rather than a convenience: reading
// is recoverable — a wrongly admitted issue can be superseded, and the log
// carries the correction — while a pull request opened on a public repository
// under the project's name cannot be taken back by any act in this workroom.
// So the silent case fails closed on exactly the side where failure is
// permanent, and the charters already ratified for reading keep working without
// quietly acquiring the power to write.
func charterAuthorizes(body map[string]string, charter, operation string) error {
	declared := strings.Fields(body["operations"])
	if len(declared) == 0 {
		if operation == OperationObserve {
			return nil
		}
		return fmt.Errorf("charter %s declares no operations, which authorizes %s only; %s writes to the forge and needs saying so explicitly",
			charter, OperationObserve, operation)
	}
	if slices.Contains(declared, operation) {
		return nil
	}
	return fmt.Errorf("charter %s authorizes %s, not %s", charter, strings.Join(declared, " and "), operation)
}

// clauseSources carries the projection's statements to admission, including the
// fold's ruling on each.
//
// Presence in Statements is not force: the fold keeps refused acts there because
// the log records what was said and not only what carried. Passing them without
// their decision let an operator-signed clause the workroom had rejected open
// the door — the same Statements-versus-force mistake this file already fixes on
// the outbound side, which I made twice before noticing it was one mistake.
func clauseSources(projection workroom.Projection) []github.ClauseSource {
	effective := make(map[string]bool, len(projection.Decisions))
	for _, decision := range projection.Decisions {
		effective[decision.Event] = decision.Verdict == workroom.Effective
	}
	sources := make([]github.ClauseSource, 0, len(projection.Statements))
	for _, statement := range projection.Statements {
		sources = append(sources, github.ClauseSource{
			Event: statement.Event, Actor: statement.Actor, Body: statement.Body,
			Stale: statement.Stale, Retired: statement.Retired,
			Bases: projection.Provenance[statement.Event], Effective: effective[statement.Event],
		})
	}
	return sources
}

func observedStatements(projection workroom.Projection) []github.Statement {
	statements := make([]github.Statement, 0, len(projection.Statements))
	for _, statement := range projection.Statements {
		statements = append(statements, github.Statement{
			Event: statement.Event, Actor: statement.Actor, Body: statement.Body,
		})
	}
	return statements
}

func authors(projection workroom.Projection) map[string]github.Author {
	authors := make(map[string]github.Author, len(projection.Actors))
	for fingerprint, actor := range projection.Actors {
		authors[fingerprint] = github.Author{Fingerprint: fingerprint, Roles: actor.Roles}
	}
	return authors
}

// appendObservation signs and submits one observation.
//
// It is an `assert`: durable, attributed, obligating nobody. Turning an
// observation into work is a member filing a request with their own signature
// on it, which is the whole defence against an issue body that reads like an
// instruction.
func appendObservation(ctx context.Context, workspace *app.Workspace, actorName, server, charter string,
	observation github.Observation) (string, error) {
	_, private, err := workspace.Actor(actorName)
	if err != nil {
		return "", err
	}
	rests := []string{charter}
	if observation.AdmittedBy != "" && observation.AdmittedBy != charter {
		rests = append(rests, observation.AdmittedBy)
	}
	request, err := workspace.BuildActRequest(ctx, private, actorName, app.Act{
		Verb: app.VerbState, Kind: workroom.KindAssert,
		Text: observation.Text, Body: observation.Body, RestsOn: rests,
		IdempotencyKey: observation.IdempotencyKey,
	})
	if err != nil {
		return "", err
	}
	submission, err := submit(ctx, workspace, server, request)
	if err != nil {
		return "", err
	}
	return submission, nil
}

// submit sends the signed request, locally or through a resident sequencer.
// The core never holds the connector's key: the request is fully signed here
// and the service only sequences it.
func submit(ctx context.Context, workspace *app.Workspace, server string, request kernel.Request) (string, error) {
	submission, err := residentclient.New(10*time.Second).Submit(ctx, workspace, server, request)
	if err != nil {
		return "", err
	}
	return submission.Record.ID, nil
}
