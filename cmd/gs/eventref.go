package main

import (
	"context"
	"fmt"
	"os"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/eventref"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// newResolver returns the one resolver every event reference of one command is
// answered from. The verified event set behind it is read at most once, and
// only if something typed actually needs resolving, so a caller who pasted
// canonical identifiers pays nothing for this.
func newResolver(ctx context.Context, workspace *app.Workspace) *eventref.Resolver {
	room := roomOf(workspace)
	return eventref.New(room, func() (eventref.Set, error) {
		snapshot, err := snapshotWithProgress(ctx, workspace)
		if err != nil {
			return eventref.Set{}, err
		}
		return eventref.FromProjection(room, snapshot.Projection), nil
	})
}

// newResolverFrom is the same resolver over a snapshot the command has already
// folded, so a read command that needed the projection anyway does not fold a
// second time.
func newResolverFrom(workspace *app.Workspace, snapshot app.Snapshot) *eventref.Resolver {
	room := roomOf(workspace)
	return eventref.New(room, func() (eventref.Set, error) {
		return eventref.FromProjection(room, snapshot.Projection), nil
	})
}

// roomOf names the workroom every reference of this command is read against.
func roomOf(workspace *app.Workspace) eventref.Room {
	view := workspace.View()
	return eventref.Room{Genesis: view.Genesis, ObjectFormat: view.ObjectFormat}
}

// showResolved names, on standard error, every identifier this command
// resolved for the caller. It is called before anything is signed, so a
// mis-resolution is visible while it can still be corrected, and it stays off
// standard output, which carries the new event's identifier and nothing else.
func showResolved(resolver *eventref.Resolver) {
	for _, line := range resolver.Lines() {
		fmt.Fprintln(os.Stderr, "gs:", line)
	}
}

// resolveRefs rewrites, in place, every event reference one command carries:
// the single-valued ones first, then the repeatable lists, each kept in the
// order it was given. Batch labels and anything else this boundary does not
// read as an event reference travel through untouched, and the first refusal
// stops the command with nothing appended.
func resolveBody(resolver *eventref.Resolver, body map[string]string) error {
	return resolver.Body(body)
}

func resolveRefs(resolver *eventref.Resolver, single []*string, lists ...*[]string) error {
	for _, reference := range single {
		if reference == nil || *reference == "" {
			continue
		}
		resolved, err := resolver.One(*reference)
		if err != nil {
			return err
		}
		*reference = resolved
	}
	for _, list := range lists {
		if list == nil {
			continue
		}
		resolved, err := resolver.Many(*list)
		if err != nil {
			return err
		}
		*list = resolved
	}
	return nil
}

// discloseBases tells an author what the citations of the act they are about
// to sign will mean here: a string that is no identifier, an identifier of
// this workroom naming no event, or an identifier of another workroom this
// room cannot verify. It describes and refuses nothing; the sequencer's own
// rules are unchanged. A workroom this process cannot read right now is
// skipped rather than refused, for the same reason admission skips it: the
// judgement that gates the append is made at sequencing.
func discloseBases(resolver *eventref.Resolver, restsOn []string) {
	if len(restsOn) == 0 {
		return
	}
	set, err := resolver.Set()
	if err != nil {
		return
	}
	for _, note := range eventref.BasisNotes(set, restsOn) {
		fmt.Fprintln(os.Stderr, "gs:", note)
	}
}

// noteDeadRestsOn tells an author, on standard error, which citations of the
// act they just filed were already dead when it landed — retired, stale,
// refused, or themselves effective supersessions. The act stays either way:
// the note describes, it does not refuse, and an author may cite a dead event
// for reasons of their own that a refusal would overrule. Each line names the
// event id as signed so it can be found again in a transcript.
//
// A citation naming no event here is a different fact, and it is reported
// before the act is signed rather than after, by discloseBases — the one
// moment at which an author can still do something about it.
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
