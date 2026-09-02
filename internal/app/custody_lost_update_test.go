package app

import (
	"context"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/apphost"
)

// The defect behind request #8762, written from the behaviour wanted rather
// than the mechanism that fixed it: one process opens the configuration,
// a second opening records actor custody, and the first persists an unrelated
// field from its stale memory. The second opening's custody must survive.
func TestStaleWorkspaceSavePreservesConcurrentCustody(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := testRepo(t)
	first, _, err := Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Open(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := second.AddActor(ctx, "human", "checker", "agent"); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Verify(ctx); err != nil {
		t.Fatal(err)
	}
	stored, err := apphost.LoadConfig(first.MetaDir)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Actors["checker"].Name != "checker" {
		t.Fatalf("custody recorded by the second opening did not survive the first opening's unrelated save: %+v", stored.Actors)
	}
	if stored.VerifiedFrontier == nil || stored.VerifiedFrontier.Depth == 0 {
		t.Fatalf("the first opening's own frontier save went missing: %+v", stored.VerifiedFrontier)
	}
}
