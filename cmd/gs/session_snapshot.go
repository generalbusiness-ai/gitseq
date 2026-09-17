package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
)

// sessionSnapshot is the one projection a command judges against, and the one
// place that decides where it comes from.
//
// A command given --server names a resident that already holds this workroom's
// verified projection. Folding the whole log again to judge an act that the
// resident is about to fold is work nobody asked for, and it is the whole cost
// of these commands: tens of seconds at this workroom's depth, against about a
// second to read the answer the resident already has. So the resident's own
// /v0/status answer is taken instead, under the frontier check every resident
// answer passes — same workroom, same head, and the head unmoved across the
// read — plus the fold profile, because a projection produced by another
// interpreter is not this command's world however current its frontier is.
//
// Anything that check cannot confirm is named on standard error and answered
// by the local audit, so a resident that is unreachable, serving another
// workroom, behind this checkout, or folding under another profile costs a
// slower command and never a wrong one. Without --server nothing changes: the
// local verified log is the only source there ever was.
func sessionSnapshot(ctx context.Context, workspace *app.Workspace, serverURL string) (app.Snapshot, error) {
	return loadSessionSnapshot(ctx, os.Stderr, workspace, serverURL, workspace.Snapshot)
}

// residentProjections is the accepted resident answer, kept for the life of
// the process. A command that files a chain of acts asks this question either
// side of every one of them, and the answer is tens of megabytes at this
// workroom's depth; fetching it again between two acts costs a fraction of a
// second each time and answers about the same world the command opened in.
//
// What the memory costs is one post-act reading: the vocabulary an
// undefined-kind warning is measured against is the one this command opened
// with, so a kind defined by an earlier act of the same chain still reads as
// undefined there. Nothing rests on that answer — admission refuses an
// undefined kind from its own local audit, and the fold decides — so this is
// the cheaper mistake. Only an accepted resident answer is kept; a local audit
// is not, because the workspace already caches its own fold.
var residentProjections struct {
	sync.Mutex
	held map[string]app.Snapshot
}

func rememberedProjection(key string) (app.Snapshot, bool) {
	residentProjections.Lock()
	defer residentProjections.Unlock()
	snapshot, held := residentProjections.held[key]
	return snapshot, held
}

func rememberProjection(key string, snapshot app.Snapshot) {
	residentProjections.Lock()
	defer residentProjections.Unlock()
	if residentProjections.held == nil {
		residentProjections.held = make(map[string]app.Snapshot, 1)
	}
	residentProjections.held[key] = snapshot
}

// loadSessionSnapshot is sessionSnapshot with its two sources named, so a test
// can prove which one answered: the local load is the function that is not
// called at all when the resident is current.
func loadSessionSnapshot(ctx context.Context, progress io.Writer, workspace *app.Workspace, serverURL string,
	load func(context.Context) (app.Snapshot, error)) (app.Snapshot, error) {
	if serverURL != "" {
		key := workspace.Repo + "\x00" + serverURL
		if held, memoized := rememberedProjection(key); memoized {
			return held, nil
		}
		snapshot, err := residentSnapshot(ctx, workspace, serverURL)
		if err == nil {
			rememberProjection(key, snapshot)
			return snapshot, nil
		}
		fmt.Fprintf(progress, "gs: resident projection unavailable (%v); verifying the durable log locally\n", err)
	}
	return loadSnapshotWithProgress(ctx, progress, load)
}

// residentStatus is the complete resident projection as the display commands
// have always read it: one fetch, and the frontier check gs status documents —
// this workroom's genesis, and a head equal to the local sequence ref and
// unmoved while the answer was read. It is deliberately not the session check
// above: a page rendered for a reader states the frontier it was taken at, and
// what a command signs against is judged by more than that.
func residentStatus(ctx context.Context, workspace *app.Workspace, serverURL string) (app.Snapshot, error) {
	status, err := fetchFullStatus(ctx, serverURL)
	if err != nil {
		return app.Snapshot{}, err
	}
	if err := validateRemoteFrontier(ctx, workspace, status.Durable.Genesis, status.Durable.Head); err != nil {
		return app.Snapshot{}, err
	}
	return status.Durable, nil
}

// residentSnapshot reads the resident's complete projection and accepts it only
// as the answer to the question this checkout asked. The sequence ref is read
// before the request and again after it, so an answer is admitted only while
// this checkout's own frontier stands exactly where the resident says it does
// and has not moved underneath the read.
//
// What that check covers, and what it does not, is the trust boundary this
// command sits on: see docs/reference/architecture.md, layer 7.
func residentSnapshot(ctx context.Context, workspace *app.Workspace, serverURL string) (app.Snapshot, error) {
	before, err := workspace.Store.Head(ctx, kernel.Ref(workspace.View().Genesis))
	if err != nil {
		return app.Snapshot{}, err
	}
	status, err := fetchFullStatus(ctx, serverURL)
	if err != nil {
		return app.Snapshot{}, err
	}
	if err := validateRemoteFrontierAt(ctx, workspace, before, status.Durable.Genesis, status.Durable.Head); err != nil {
		return app.Snapshot{}, err
	}
	// A frontier says which records the projection was folded from; the profile
	// says which rules folded them. A resident running another application or
	// fold version answers about a world this command cannot reason in, so it
	// is refused with the rest.
	if profile := workspace.Profile(); status.Profile != profile {
		return app.Snapshot{}, fmt.Errorf("resident fold profile %q is not this checkout's %q", status.Profile, profile)
	}
	// The fold gives every record it holds one decision, so a complete
	// projection carries exactly as many as the frontier counts. A truncated
	// or empty answer at a correct head fails here rather than becoming a
	// world in which the records that were dropped never happened.
	if decisions := len(status.Durable.Projection.Decisions); decisions != status.Durable.Depth {
		return app.Snapshot{}, fmt.Errorf("resident projection holds %d decisions at depth %d", decisions, status.Durable.Depth)
	}
	return status.Durable, nil
}
