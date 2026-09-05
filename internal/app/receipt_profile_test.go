package app

import (
	"context"
	"reflect"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/apphost"
)

func TestReceiptAccountingRejectsPriorProjectionCaches(t *testing.T) {
	for _, prior := range []string{"workroom-fold@19", "workroom-fold@20"} {
		t.Run(prior, func(t *testing.T) {
			ctx := context.Background()
			workspace, _, err := Init(ctx, testRepo(t), "human", 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			want, err := workspace.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			// Poison the projection only, leaving its verified event checkpoint and
			// frontier intact. Serving this cache instead of replaying is observable.
			old := want
			old.Projection.OmittedSupersessions = 99
			workspace.snapshotMu.Lock()
			workspace.snapshotCache = &old
			workspace.snapshotProfile = apphost.DefaultApplication + "\x00" + prior
			workspace.snapshotMu.Unlock()
			got, err := workspace.SnapshotWithSource(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Snapshot, want) || got.Source != SnapshotSourceSignedCheckpointTail {
				t.Fatalf("old projection was not rebuilt from its kernel checkpoint: source=%q debt=%d", got.Source, got.Snapshot.Projection.OmittedSupersessions)
			}
			if workspace.snapshotProfile != apphost.DefaultApplication+"\x00workroom-fold@21" {
				t.Fatalf("rebuilt profile = %q", workspace.snapshotProfile)
			}
		})
	}
}
