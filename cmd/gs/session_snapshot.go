package main

import (
	"context"
	"fmt"
	"io"
	"os"

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

// loadSessionSnapshot is sessionSnapshot with its two sources named, so a test
// can prove which one answered: the local load is the function that is not
// called at all when the resident is current.
func loadSessionSnapshot(ctx context.Context, progress io.Writer, workspace *app.Workspace, serverURL string,
	load func(context.Context) (app.Snapshot, error)) (app.Snapshot, error) {
	if serverURL != "" {
		snapshot, err := residentSnapshot(ctx, workspace, serverURL)
		if err == nil {
			return snapshot, nil
		}
		fmt.Fprintf(progress, "gs: resident projection unavailable (%v); verifying the durable log locally\n", err)
	}
	return loadSnapshotWithProgress(ctx, progress, load)
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
