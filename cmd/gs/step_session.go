package main

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/eventref"
	"github.com/generalbusiness-ai/gitseq/internal/statusview"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// The working cycle has steps, and each step has one durable shape: which
// event to rest on, which body fields to carry, which artifact to name first,
// what order to publish in. AGENTS.md and SKILL.md state those shapes in
// prose, and an actor who reconstructs them by hand gets them wrong in the
// same few ways every time. gs promise, gs artifact, gs review-request and
// gs land encode one step each.
//
// None of them is a new rule. Every one preflights what the fold already
// requires, refuses with the fix named, and then composes the existing state,
// batch, ratify, merge-plan and merge paths. A refusal here is a refusal the
// fold would make later, moved to where nothing has been appended yet.
//
// stepSession is the opening every one of them shares: the actor, the
// destination sequencer, one verified projection, and the resolver that
// answers every short reference from that same projection. Reading the
// projection once is what lets a preflight judge a whole act against one
// world.
type stepSession struct {
	ctx         context.Context
	workspace   *app.Workspace
	repo        string
	actor       string
	fingerprint string
	serverURL   string
	snapshot    app.Snapshot
	resolver    *eventref.Resolver
}

// stepUsage states the positional form a step command takes. The shared flag
// set prints "[flags]" for a command it does not know, and Go's flag parsing
// stops at the first positional, so a caller who writes the paths before the
// flags gets a refusal that has to explain itself. This is where the form is
// said once.
func stepUsage(set *flag.FlagSet, form string) {
	set.Usage = func() {
		fmt.Fprintf(set.Output(), "usage: %s\n\nFlags:\n", form)
		set.PrintDefaults()
	}
}

func openStep(ctx context.Context, repo, as, server string) (*stepSession, error) {
	actor, err := signingActor(as)
	if err != nil {
		return nil, err
	}
	workspace, err := app.Open(ctx, repo)
	if err != nil {
		return nil, err
	}
	serverURL, err := resolveServerURL(workspace, server)
	if err != nil {
		return nil, err
	}
	fingerprint := workspace.View().Actors[actor].Fingerprint
	if fingerprint == "" {
		return nil, workIdentityRefusal(ctx, workspace, fmt.Errorf("actor %q is not provisioned in this checkout", actor))
	}
	snapshot, err := snapshotWithProgress(ctx, workspace)
	if err != nil {
		return nil, err
	}
	return &stepSession{
		ctx: ctx, workspace: workspace, repo: repo, actor: actor, fingerprint: fingerprint,
		serverURL: serverURL, snapshot: snapshot, resolver: newResolverFrom(workspace, snapshot),
	}, nil
}

// resolve answers every short reference this command was given from the one
// projection the preflight is judged against, and names what it resolved
// before anything is signed.
func (s *stepSession) resolve(references ...*string) error {
	if err := resolveRefs(s.resolver, references); err != nil {
		return err
	}
	showResolved(s.resolver)
	return nil
}

func (s *stepSession) projection() workroom.Projection { return s.snapshot.Projection }

func (s *stepSession) statement(event string) (workroom.Statement, bool) {
	for _, statement := range s.projection().Statements {
		if statement.Event == event {
			return statement, true
		}
	}
	return workroom.Statement{}, false
}

func (s *stepSession) commitmentByRequest(request string) (workroom.Commitment, bool) {
	for _, commitment := range s.projection().Commitments {
		if commitment.Request == request {
			return commitment, true
		}
	}
	return workroom.Commitment{}, false
}

func (s *stepSession) commitmentByPromise(promise string) (workroom.Commitment, bool) {
	for _, commitment := range s.projection().Commitments {
		if commitment.Promise == promise {
			return commitment, true
		}
	}
	return workroom.Commitment{}, false
}

func (s *stepSession) commitmentByReport(report string) (workroom.Commitment, bool) {
	for _, commitment := range s.projection().Commitments {
		if commitment.Report == report {
			return commitment, true
		}
	}
	return workroom.Commitment{}, false
}

// name is how a refusal talks about another actor: by the name the durable
// roster gives their fingerprint, because a fingerprint in a refusal tells a
// reader nothing they can act on.
func (s *stepSession) name(fingerprint string) string {
	if fingerprint == "" {
		return "nobody"
	}
	if name := statusview.ActorName(s.projection(), fingerprint); name != "" {
		return name
	}
	return short(fingerprint)
}

func (s *stepSession) submit(act app.Act) (workroom.Record, error) {
	return submitAct(s.ctx, s.workspace, s.serverURL, s.actor, act)
}

// requireFullCommit refuses an abbreviated head before anything is signed. A
// review verdict finds its artifact by comparing this field as an exact
// string, and gs merge resolves nothing shorter, so an abbreviated commit is
// an artifact that can take no part in either.
func requireFullCommit(flagName, head string) error {
	if len(head) == 40 || len(head) == 64 {
		hex := true
		for _, r := range head {
			if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
				hex = false
				break
			}
		}
		if hex {
			return nil
		}
	}
	return fmt.Errorf("%s %s is not a full canonical object ID; pass the whole hash, from `git rev-parse HEAD`", flagName, head)
}

// oneLine keeps a quoted statement to one readable line of a refusal or a
// default text. The whole statement stays where it was written; this is a
// label for it, not a copy of it.
func oneLine(text string, limit int) string {
	// Cut the raw text first. The display filter escapes a newline into a
	// visible \x0a, so a line taken after filtering carries the rest of the
	// statement as escape sequences instead of stopping at the first line.
	line := text
	if index := strings.IndexAny(line, "\r\n"); index >= 0 {
		line = line[:index]
	}
	// A work row carries text the display filter has already escaped, so the
	// first line break may have arrived here as the four characters \x0a.
	for _, escape := range []string{`\x0a`, `\x0d`} {
		if index := strings.Index(line, escape); index >= 0 {
			line = line[:index]
		}
	}
	line = strings.TrimSpace(statusview.Text(strings.TrimSpace(line)))
	if len(line) > limit {
		line = strings.TrimSpace(line[:limit]) + "…"
	}
	return line
}

// staleWarning is the one sentence AGENTS.md step 1 owes a stale row: the
// staleness is named, the repair belongs to the request's author, and the
// command carries on. Ordinary staleness is not a question.
func staleWarning(kind, event, because, author string) string {
	cause := ""
	if because != "" {
		cause = fmt.Sprintf(" (basis %s was retired)", short(because))
	}
	return fmt.Sprintf("warning: %s %s is stale%s; ask %s to refile it on current bases once the conditions, your availability and the governing decisions are confirmed unchanged",
		kind, short(event), cause, author)
}
