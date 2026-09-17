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

// The step commands — gs promise, gs artifact, gs review-request, gs land —
// encode one step of the working cycle each. Each preflights what the fold
// already requires, refuses with the fix named, and composes the existing
// state, batch, ratify, merge-plan and merge paths. docs/reference/gs/ states
// what each one does; the comments here stay on ordering, recovery and safety.
//
// stepSession is their shared opening: the actor, the destination sequencer,
// one verified projection, and the resolver that answers every short reference
// from that same projection. Reading the projection once is what lets a whole
// act be judged against one world, and sessionSnapshot is where that one world
// comes from: the resident's, when --server names one standing exactly here,
// and the local audit otherwise.
type stepSession struct {
	ctx context.Context
	// set is this command's own flags, kept so that a refusal about the
	// invocation — an identity nobody holds, a reference naming nothing here —
	// can answer with the usage and worked example every other gs command
	// answers with.
	set         *flag.FlagSet
	workspace   *app.Workspace
	repo        string
	actor       string
	fingerprint string
	serverURL   string
	snapshot    app.Snapshot
	resolver    *eventref.Resolver
	// One pass over the projection answers every lookup these commands make.
	// The snapshot is taken once and never moves under them, so the indexes
	// cannot disagree with the world the preflight judged.
	statements map[string]workroom.Statement
	byRequest  map[string]workroom.Commitment
	byPromise  map[string]workroom.Commitment
	byReport   map[string]workroom.Commitment
}

// stepUsage states the positional form a step command takes: Go's flag parsing
// stops at the first positional, so the form has to be said somewhere.
func stepUsage(set *flag.FlagSet, form string) {
	set.Usage = func() {
		fmt.Fprintf(set.Output(), "usage: %s\n\nFlags:\n", form)
		set.PrintDefaults()
		printExample(set)
	}
}

func openStep(ctx context.Context, set *flag.FlagSet, repo, as, server string) (*stepSession, error) {
	actor, err := signingActorOrUsage(set, as)
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
		return nil, usageReferenceError(set, workIdentityRefusal(ctx, workspace,
			fmt.Errorf("actor %q is not provisioned in this checkout", actor)))
	}
	snapshot, err := sessionSnapshot(ctx, workspace, serverURL)
	if err != nil {
		return nil, err
	}
	session := &stepSession{
		ctx: ctx, set: set, workspace: workspace, repo: repo, actor: actor, fingerprint: fingerprint,
		serverURL: serverURL, snapshot: snapshot, resolver: newResolverFrom(workspace, snapshot),
		statements: make(map[string]workroom.Statement, len(snapshot.Projection.Statements)),
		byRequest:  make(map[string]workroom.Commitment),
		byPromise:  make(map[string]workroom.Commitment),
		byReport:   make(map[string]workroom.Commitment),
	}
	for _, statement := range snapshot.Projection.Statements {
		session.statements[statement.Event] = statement
	}
	for _, commitment := range snapshot.Projection.Commitments {
		session.byRequest[commitment.Request] = commitment
		if commitment.Promise != "" {
			session.byPromise[commitment.Promise] = commitment
		}
		if commitment.Report != "" {
			session.byReport[commitment.Report] = commitment
		}
	}
	return session, nil
}

// resolve answers every short reference this command was given from the one
// projection the preflight is judged against, and names what it resolved
// before anything is signed.
func (s *stepSession) resolve(references ...*string) error {
	if err := resolveRefs(s.resolver, references); err != nil {
		return s.usage(err)
	}
	showResolved(s.resolver)
	return nil
}

// usage answers a malformed invocation: this command's flags and one worked
// example, then the reason. A reference that names nothing here is a mistyped
// command, not a state of the workroom, so it is answered the way every other
// malformed invocation is.
func (s *stepSession) usage(err error) error {
	return usageReferenceError(s.set, err)
}

func (s *stepSession) projection() workroom.Projection { return s.snapshot.Projection }

func (s *stepSession) statement(event string) (workroom.Statement, bool) {
	statement, found := s.statements[event]
	return statement, found
}

func (s *stepSession) commitmentByRequest(request string) (workroom.Commitment, bool) {
	commitment, found := s.byRequest[request]
	return commitment, found
}

func (s *stepSession) commitmentByPromise(promise string) (workroom.Commitment, bool) {
	commitment, found := s.byPromise[promise]
	return commitment, found
}

func (s *stepSession) commitmentByReport(report string) (workroom.Commitment, bool) {
	commitment, found := s.byReport[report]
	return commitment, found
}

// name is how a refusal talks about another actor: a fingerprint tells a reader
// nothing they can act on.
func (s *stepSession) name(fingerprint string) string {
	if fingerprint == "" {
		return "nobody"
	}
	if name := statusview.ActorName(s.projection(), fingerprint); name != "" {
		return name
	}
	return short(fingerprint)
}

// latestWithdrawnPromise is the newest promise of this actor's, on this
// request, that they have since superseded. Every claim after a withdrawal
// names the one it follows, so this is what makes the key of the current claim
// recomputable rather than remembered.
func (s *stepSession) latestWithdrawnPromise(request string) string {
	withdrawn, at := "", 0
	for _, statement := range s.projection().Statements {
		if statement.Kind != workroom.KindPromise || statement.Actor != s.fingerprint || !statement.Retired {
			continue
		}
		if statement.Sequence < at {
			continue
		}
		for _, basis := range s.projection().Provenance[statement.Event] {
			if basis == request {
				withdrawn, at = statement.Event, statement.Sequence
			}
		}
	}
	return withdrawn
}

// retrying reports whether an event the log already holds is the very act
// this run would submit under this key. It is what separates a caller
// repeating a command after a lost answer — which must replay — from a
// second, different act on the same commitment.
func (s *stepSession) retrying(key, event string) bool {
	if key == "" || event == "" {
		return false
	}
	_, private, err := s.workspace.Actor(s.actor)
	if err != nil {
		return false
	}
	accepted, held := s.workspace.AcceptedActUnderKey(s.ctx, private, s.actor, key)
	return held && accepted == event
}

func (s *stepSession) submit(act app.Act) (workroom.Record, error) {
	return submitAct(s.ctx, s.workspace, s.serverURL, s.actor, act)
}

// requireFullCommit refuses an abbreviated head before anything is signed: a
// verdict finds its artifact by exact string comparison and gs merge resolves
// nothing shorter, so an abbreviation can take no part in either.
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

// oneLine keeps a quoted statement to one readable line: a label for it, not a
// copy of it.
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

// staleWarning names the staleness and whose repair it is, and the command
// carries on: ordinary staleness is not a question.
func staleWarning(kind, event, because, author string) string {
	cause := ""
	if because != "" {
		cause = fmt.Sprintf(" (basis %s was retired)", short(because))
	}
	return fmt.Sprintf("warning: %s %s is stale%s; ask %s to refile it on current bases once the conditions, your availability and the governing decisions are confirmed unchanged",
		kind, short(event), cause, author)
}
