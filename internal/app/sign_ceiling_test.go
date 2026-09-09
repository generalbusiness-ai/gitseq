package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/observe"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// signRequest is the only place a submission is signed, so it is where the
// genesis ceiling can stand in front of every surface at once. What is being
// proved is the boundary, not the refusal: the workspace records one
// measurement where a signature is produced, and on an act that cannot be
// admitted that measurement must never appear.
func TestSignRequestRefusesOverCeilingBeforeSigning(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	workspace, seed, err := Init(ctx, testRepo(t), "human", 2<<10)
	if err != nil {
		t.Fatal(err)
	}
	signatures := &signatureCounter{}
	workspace.SetObserver(signatures)
	before, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}

	_, err = workspace.Act(ctx, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "oversized evidence",
		RestsOn: []string{seed.ID}, Attachments: map[string][]byte{"observations.json": make([]byte, 4<<10)},
		IdempotencyKey: "oversized-evidence",
	})
	if err == nil {
		t.Fatal("an act over the ceiling was signed and accepted")
	}
	for _, want := range []string{
		"event exceeds genesis ceiling: ",
		"against a ceiling of 2048",
		`attachments 4096 in 1 file, largest "observations.json" at 4096 bytes`,
		"cite a repository path instead of attaching bytes",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q does not carry %q", err.Error(), want)
		}
	}
	if signed := signatures.count.Load(); signed != 0 {
		t.Fatalf("the refused act was signed %d time(s)", signed)
	}

	after, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Head != before.Head || after.Depth != before.Depth {
		t.Fatalf("refused act changed the durable log: before=%s/%d after=%s/%d", before.Head, before.Depth, after.Head, after.Depth)
	}

	// No signature exists, so the retry key was never minted into an act. The
	// same key still files a fresh act.
	if _, err := workspace.Act(ctx, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "oversized evidence",
		RestsOn: []string{seed.ID}, Attachments: map[string][]byte{"observations.json": make([]byte, 64)},
		IdempotencyKey: "oversized-evidence",
	}); err != nil {
		t.Fatalf("the refused act's idempotency key was spent: %v", err)
	}
	if signed := signatures.count.Load(); signed != 1 {
		t.Fatalf("the admitted act was signed %d time(s), want 1", signed)
	}
	landed, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if landed.Depth != before.Depth+1 {
		t.Fatalf("admitted act depth = %d, want %d", landed.Depth, before.Depth+1)
	}
}

// A read-only attachment records no ceiling, and neither does a configuration
// written before the field existed. Both are supported workrooms, and the
// boundary has to hold on them: skipping the measurement there signs exactly
// the act it exists to refuse. Genesis is the authority, so the refusal is
// measured against the ceiling genesis records while an ordinary act still
// signs and lands.
func TestSignRequestMeasuresAgainstGenesisWhenTheLocalRecordCarriesNoCeiling(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := testRepo(t)
	workspace, seed, err := Init(ctx, repo, "human", 2<<10)
	if err != nil {
		t.Fatal(err)
	}
	// The stored record is what a reopened workspace loads, so the ceiling is
	// dropped there and the workspace reopened through the ordinary path,
	// rather than reached into in memory where the configuration consistency
	// guard would answer instead.
	forgetStoredPayloadCeiling(t, workspace.MetaDir)
	reopened, err := Open(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.View().PayloadCeiling != 0 {
		t.Fatalf("the reopened workspace still records a ceiling of %d", reopened.View().PayloadCeiling)
	}
	signatures := &signatureCounter{}
	reopened.SetObserver(signatures)
	before, err := reopened.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}

	_, err = reopened.Act(ctx, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "oversized evidence on a workroom recording no ceiling",
		RestsOn: []string{seed.ID}, Attachments: map[string][]byte{"observations.json": make([]byte, 4<<10)},
		IdempotencyKey: "no-local-ceiling-oversized",
	})
	if err == nil {
		t.Fatal("an act over the genesis ceiling was signed and accepted")
	}
	for _, want := range []string{
		"event exceeds genesis ceiling: ",
		"against a ceiling of 2048",
		`attachments 4096 in 1 file, largest "observations.json" at 4096 bytes`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q does not carry %q", err.Error(), want)
		}
	}
	if signed := signatures.count.Load(); signed != 0 {
		t.Fatalf("the refused act was signed %d time(s)", signed)
	}
	after, err := reopened.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Head != before.Head || after.Depth != before.Depth {
		t.Fatalf("refused act changed the durable log: before=%s/%d after=%s/%d", before.Head, before.Depth, after.Head, after.Depth)
	}

	// The boundary must not refuse a supported workroom outright. An ordinary
	// act on the same reopened workspace still signs and lands.
	if _, err := reopened.Act(ctx, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "an ordinary act on a workroom recording no ceiling",
		RestsOn: []string{seed.ID}, IdempotencyKey: "no-local-ceiling-ordinary",
	}); err != nil {
		t.Fatalf("an ordinary act was refused for want of a local ceiling: %v", err)
	}
	if signed := signatures.count.Load(); signed != 1 {
		t.Fatalf("the admitted act was signed %d time(s), want 1", signed)
	}
	landed, err := reopened.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if landed.Depth != before.Depth+1 {
		t.Fatalf("admitted act depth = %d, want %d", landed.Depth, before.Depth+1)
	}
}

// forgetStoredPayloadCeiling removes the recorded ceiling from the stored
// configuration, which is what a workroom attached read-only or configured
// before the field existed looks like on disk.
func forgetStoredPayloadCeiling(t *testing.T, metaDir string) {
	t.Helper()
	path := filepath.Join(metaDir, "config.json")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(content, &record); err != nil {
		t.Fatal(err)
	}
	delete(record, "payload_ceiling")
	rewritten, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, rewritten, 0o600); err != nil {
		t.Fatal(err)
	}
}

// signatureCounter counts the measurements signRequest records where a
// signature is produced, and ignores every other measurement. It belongs to
// one workspace, so parallel tests never see each other's counts.
type signatureCounter struct{ count atomic.Int64 }

func (c *signatureCounter) Record(_ context.Context, measurement observe.Measurement) {
	if measurement.Operation == observe.OperationSubmit && measurement.Path == observe.PathSignature {
		c.count.Add(1)
	}
}

// The other supported workroom that records no ceiling is one attached to a
// sequence it fetched: AttachConfig writes a configuration with no ceiling at
// all. Nothing local can answer the question there, so the genesis it attached
// to has to. This drives BuildActRequest directly, because an attached
// workspace is read-only and its acts are accepted by the workspace that owns
// the sequence.
func TestSignRequestMeasuresAgainstGenesisForAnAttachedWorkspace(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sourceRepo := testRepo(t)
	source, seed, err := Init(ctx, sourceRepo, "human", 2<<10)
	if err != nil {
		t.Fatal(err)
	}
	_, private, err := source.Actor("human")
	if err != nil {
		t.Fatal(err)
	}
	genesis := source.View().Genesis

	attachRepo := testRepo(t)
	if output, err := exec.Command("git", "-C", attachRepo, "fetch", "-q", sourceRepo,
		kernel.Ref(genesis)+":"+kernel.Ref(genesis)).CombinedOutput(); err != nil {
		t.Fatalf("git fetch: %v: %s", err, output)
	}
	attached, err := AttachConfig(ctx, attachRepo, genesis, "sha1")
	if err != nil {
		t.Fatal(err)
	}
	if attached.View().PayloadCeiling != 0 {
		t.Fatalf("the attached workspace records a ceiling of %d, so this is not the case under test", attached.View().PayloadCeiling)
	}
	signatures := &signatureCounter{}
	attached.SetObserver(signatures)
	before, err := source.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}

	_, err = attached.BuildActRequest(ctx, private, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "oversized evidence from an attached workspace",
		RestsOn: []string{seed.ID}, Attachments: map[string][]byte{"observations.json": make([]byte, 4<<10)},
		IdempotencyKey: "attached-oversized",
	})
	if err == nil {
		t.Fatal("an attached workspace signed an act over the genesis ceiling")
	}
	for _, want := range []string{
		"event exceeds genesis ceiling: ",
		"against a ceiling of 2048",
		`attachments 4096 in 1 file, largest "observations.json" at 4096 bytes`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q does not carry %q", err.Error(), want)
		}
	}
	if signed := signatures.count.Load(); signed != 0 {
		t.Fatalf("the refused act was signed %d time(s)", signed)
	}
	after, err := source.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Head != before.Head || after.Depth != before.Depth {
		t.Fatalf("refused act changed the durable log: before=%s/%d after=%s/%d", before.Head, before.Depth, after.Head, after.Depth)
	}

	// The observer is only evidence if it moves. An ordinary act built by the
	// same attached workspace signs exactly once and is accepted by the
	// workspace that owns the sequence.
	request, err := attached.BuildActRequest(ctx, private, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "an ordinary act from an attached workspace",
		RestsOn: []string{seed.ID}, IdempotencyKey: "attached-ordinary",
	})
	if err != nil {
		t.Fatalf("an ordinary attached act was refused: %v", err)
	}
	if signed := signatures.count.Load(); signed != 1 {
		t.Fatalf("the admitted act was signed %d time(s), want 1", signed)
	}
	if _, err := source.AcceptSubmission(ctx, request); err != nil {
		t.Fatalf("the source workspace refused the attached act: %v", err)
	}
	landed, err := source.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if landed.Depth != before.Depth+1 {
		t.Fatalf("admitted act depth = %d, want %d", landed.Depth, before.Depth+1)
	}
}
