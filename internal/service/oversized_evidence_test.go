package service

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/observe"
)

// The resident's /v0/act carries browser evidence as inline strings. Its 2 MiB
// body cap is the transport's bound, not the log's: a body that clears it can
// still be far over the genesis ceiling, and used to be signed and handed to
// the fold before the kernel refused it. The application now measures the act
// where it signs it. The resident sequences in process, so what separates a
// refusal raised in front of signing from the same refusal raised inside the
// fold is whether the submission path was entered at all, and the observer
// below counts exactly that.
func TestActEvidenceOverTheCeilingIsRefusedBeforeSubmission(t *testing.T) {
	t.Parallel()
	f := newAuthorizationFixture(t)
	basis := f.request("size-basis", "a basis the oversized act can rest on")
	submissions := &submissionCounter{}
	f.workspace.SetObserver(submissions)
	before, err := f.workspace.Snapshot(f.ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Over the 1 MiB ceiling and under the 2 MiB body cap, so what refuses is
	// the log's bound and not the transport's.
	id, refusal := f.post(actRequest{Session: f.credential, Act: "state", Kind: "assert",
		Text: "oversized evidence", RestsOn: []string{basis},
		Evidence: map[string]string{"observations.json": strings.Repeat("x", 1200000)}, IdempotencyKey: "oversized-evidence"})
	if id != "" {
		t.Fatalf("/v0/act sequenced evidence over the ceiling as %s", id)
	}
	for _, want := range []string{
		"event exceeds genesis ceiling: ",
		"against a ceiling of 1048576",
		`attachments 1200000 in 1 file, largest "observations.json" at 1200000 bytes`,
		"cite a repository path instead of attaching bytes",
	} {
		if !strings.Contains(refusal, want) {
			t.Fatalf("refusal %q does not carry %q", refusal, want)
		}
	}
	if entered := submissions.count.Load(); entered != 0 {
		t.Fatalf("the refused act entered the submission path %d time(s)", entered)
	}

	after, err := f.workspace.Snapshot(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Head != before.Head || after.Depth != before.Depth {
		t.Fatalf("refused act changed the durable log: before=%s/%d after=%s/%d", before.Head, before.Depth, after.Head, after.Depth)
	}

	// No signature was produced, so the retry key was never minted into an act.
	landed, retry := f.post(actRequest{Session: f.credential, Act: "state", Kind: "assert",
		Text: "oversized evidence", RestsOn: []string{basis},
		Evidence: map[string]string{"observations.json": strings.Repeat("x", 64)}, IdempotencyKey: "oversized-evidence"})
	if retry != "" {
		t.Fatalf("the refused act's idempotency key was spent: %s", retry)
	}
	if landed == "" {
		t.Fatal("evidence within the ceiling was not sequenced")
	}
	if entered := submissions.count.Load(); entered != 1 {
		t.Fatalf("admitted act entered the submission path %d time(s), want 1", entered)
	}
}

// submissionCounter counts entries into the workspace's submission path and
// ignores every other measurement.
type submissionCounter struct{ count atomic.Int64 }

func (c *submissionCounter) Record(_ context.Context, measurement observe.Measurement) {
	if measurement.Operation == observe.OperationSubmit && measurement.Path == observe.PathSubmission {
		c.count.Add(1)
	}
}
