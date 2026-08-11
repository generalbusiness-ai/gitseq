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

	if err := charterIsLive(snapshot.Projection, *charter, *owner, *name, actorName); err != nil {
		return err
	}

	clauses := github.ClausesFrom(clauseSources(snapshot.Projection), authors(snapshot.Projection))
	if len(clauses) == 0 {
		// Not an error. A workroom with no clause has asked for nothing, and
		// observing anyway would be the connector deciding its own admission.
		fmt.Println("no live admission clause; nothing to observe")
		return nil
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

	seen := github.Fold(observedStatements(snapshot.Projection))
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

// charterIsLive refuses to act under a charter that is absent, retired, stale,
// unratified, or that does not authorize this exact repository and connector
// actor. The fold does not know what a charter is, so this is the connector
// holding itself to its own contract — detection at the door rather than
// attribution afterwards.
//
// The binding matters as much as the liveness. A charter that names no
// repository authorizes nothing in particular, and accepting one would let this
// command observe any repository at all under some unrelated ratified
// statement while claiming a doorstep it does not have. So a charter must say
// what it charters, and a charter that does not is refused rather than read
// generously.
func charterIsLive(projection workroom.Projection, charter, owner, name, actor string) error {
	for _, statement := range projection.Statements {
		if statement.Event != charter {
			continue
		}
		if statement.Retired {
			return errors.New("charter is retired")
		}
		if statement.Stale {
			return errors.New("charter is stale")
		}
		if !statement.Ratified {
			return errors.New("charter is not ratified")
		}
		return charterBinds(statement.Body, charter, owner, name, actor)
	}
	return fmt.Errorf("charter %s is not in this workroom", charter)
}

// charterBinds checks the charter body against what this process was told to
// do. Each mismatch is reported separately because they mean different things:
// the wrong repository is a scope error, the wrong actor is an identity error,
// and a body that says nothing is a charter that never bounded anything.
func charterBinds(body map[string]string, charter, owner, name, actor string) error {
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
	return nil
}

func clauseSources(projection workroom.Projection) []github.ClauseSource {
	sources := make([]github.ClauseSource, 0, len(projection.Statements))
	for _, statement := range projection.Statements {
		sources = append(sources, github.ClauseSource{
			Event: statement.Event, Actor: statement.Actor, Body: statement.Body,
			Stale: statement.Stale, Retired: statement.Retired,
		})
	}
	return sources
}

func observedStatements(projection workroom.Projection) []github.Statement {
	statements := make([]github.Statement, 0, len(projection.Statements))
	for _, statement := range projection.Statements {
		statements = append(statements, github.Statement{Event: statement.Event, Body: statement.Body})
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
