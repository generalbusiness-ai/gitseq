package host_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/host"
	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/apphost"
	"github.com/generalbusiness-ai/gitseq/internal/gitstore"
	"github.com/generalbusiness-ai/gitseq/internal/intent"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
)

// These tests use only what an application outside this module can use. The
// one exception is the Workroom repository built through internal/app, which
// exists to prove that an outside application refuses one.

const (
	testName = "gitseq-host-test"
	testFold = "gitseq-host-test-fold@0"
)

func testApplication() host.Application {
	return host.Application{Name: testName, FoldVersion: testFold, SourceURL: "https://example.invalid/app.git"}
}

func testRepo(t testing.TB) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	return repo
}

func testKey(t testing.TB) ed25519.PrivateKey {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return private
}

func initialized(t *testing.T, ctx context.Context) (string, *host.Workspace, ed25519.PrivateKey) {
	t.Helper()
	repo := testRepo(t)
	key := testKey(t)
	workspace, err := host.Init(ctx, repo, testApplication(), key, host.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return repo, workspace, key
}

func externallySign(t testing.TB, prepared host.PreparedAct, private ed25519.PrivateKey) host.SignedAct {
	t.Helper()
	message, err := host.ActorSigningBytes(prepared)
	if err != nil {
		t.Fatal(err)
	}
	return host.SignedAct{
		Prepared: prepared, ActorKey: private.Public().(ed25519.PublicKey),
		ActorSignature: ed25519.Sign(private, message),
	}
}

// The whole point of the package in one test: an application creates a
// repository, two different keys act in it, and replaying the records gives
// the application its own state back.
func TestOutsideApplicationAppendsAndReplaysItsOwnRecords(t *testing.T) {
	ctx := context.Background()
	repo, workspace, initializer := initialized(t, ctx)
	opponent := testKey(t)

	first, err := workspace.Append(ctx, initializer, host.Act{Schema: "test/move@0", Payload: []byte(`{"m":"e4"}`)})
	if err != nil {
		t.Fatal(err)
	}
	second, err := workspace.Append(ctx, opponent, host.Act{Schema: "test/move@0", Payload: []byte(`{"m":"e5"}`), RestsOn: []string{first.ID}})
	if err != nil {
		t.Fatal(err)
	}

	log, err := workspace.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The binding is the first record, so the two acts follow it.
	if log.Depth != 3 || len(log.Records) != 3 {
		t.Fatalf("log depth %d with %d records, want 3 records: the binding and both acts", log.Depth, len(log.Records))
	}
	if log.Genesis == "" || log.Head == "" {
		t.Fatalf("log = %+v, want the sequence position it was read at", log)
	}
	acts := log.Records[1:]
	if acts[0].ID != first.ID || acts[1].ID != second.ID {
		t.Fatalf("records %s, %s, want the appended records in order %s, %s", acts[0].ID, acts[1].ID, first.ID, second.ID)
	}
	if !bytes.Equal(acts[0].Payload, []byte(`{"m":"e4"}`)) || acts[0].Schema != "test/move@0" {
		t.Fatalf("record = %s %q, want the act exactly as signed", acts[0].Schema, acts[0].Payload)
	}
	if len(acts[1].RestsOn) != 1 || acts[1].RestsOn[0] != first.ID {
		t.Fatalf("rests on %v, want the causal chain the actor signed", acts[1].RestsOn)
	}
	// Players are keys: the two acts are told apart by who signed them, with
	// no roster anywhere.
	if acts[0].Actor == acts[1].Actor {
		t.Fatalf("both records report actor %s, want the two signing keys distinguished", acts[0].Actor)
	}
	if !acts[0].ActorKey.Equal(initializer.Public()) || !acts[1].ActorKey.Equal(opponent.Public()) {
		t.Fatal("records do not carry the public keys that signed them")
	}
	if acts[0].Timestamp == 0 {
		t.Fatal("record carries no sequencer timestamp, so a fold has no log-internal time to judge on")
	}

	// A second process reads the same log and reaches the same state.
	reopened, err := host.Open(ctx, repo, testApplication())
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := reopened.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(replayed.Records) != len(log.Records) || replayed.Head != log.Head {
		t.Fatalf("replay = %d records at %s, want %d at %s", len(replayed.Records), replayed.Head, len(log.Records), log.Head)
	}
	for index, record := range replayed.Records {
		if record.ID != log.Records[index].ID || !bytes.Equal(record.Payload, log.Records[index].Payload) {
			t.Fatalf("record %d differs between the writer and a fresh reader", index)
		}
	}
}

// The actor signs outside host and submits only public material. The host's
// private-key Append remains a convenience, not the only way to write.
func TestExternallySignedActAppendsWithoutPrivateKeyCustody(t *testing.T) {
	ctx := context.Background()
	_, workspace, _ := initialized(t, ctx)
	actor := testKey(t)
	prepared, err := workspace.Prepare(host.Act{
		Schema: "test/move@0", Payload: []byte(`{"m":"e4"}`), IdempotencyKey: "external-move-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	record, err := workspace.AppendSigned(ctx, externallySign(t, prepared, actor))
	if err != nil {
		t.Fatal(err)
	}
	if record.Schema != "test/move@0" || string(record.Payload) != `{"m":"e4"}` || !record.ActorKey.Equal(actor.Public()) {
		t.Fatalf("record = %+v, want the externally signed act and actor", record)
	}

	// The prepared idempotency position is the retry token. Reusing it returns
	// the same record; preparing the source act again would intentionally make
	// a fresh act when no key was supplied.
	replay, err := workspace.AppendSigned(ctx, externallySign(t, prepared, actor))
	if err != nil || replay.ID != record.ID {
		t.Fatalf("external retry = %+v, %v, want %s", replay, err, record.ID)
	}
}

// Signature, actor key, canonical encoding, payload, sequence, namespace,
// unsupported envelope fields, and reserved-schema checks all sit before
// submission. The exact sequence error makes that assertion sensitive to the
// host guard: the kernel also refuses another target, but with a different
// error. The loose-object count makes the payload assertion mutation-sensitive:
// the kernel also refuses a mismatched payload tree, but only after writing the
// presented payload tree, which this boundary promises not to reach for
// malformed public input.
func TestAppendSignedRejectsTamperingBeforeAnyGitWrite(t *testing.T) {
	ctx := context.Background()
	repo, workspace, _ := initialized(t, ctx)
	actor := testKey(t)
	prepared, err := workspace.Prepare(host.Act{Schema: "test/move@0", Payload: []byte("e4"), IdempotencyKey: "tamper"})
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := workspace.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, commonDir, err := apphost.ResolveGitDirs(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	objectsBefore := looseObjects(t, commonDir)

	tamperedPayload := externallySign(t, prepared, actor)
	tamperedPayload.Prepared.Payload = []byte("d4")
	if _, err := workspace.AppendSigned(ctx, tamperedPayload); err == nil || !strings.Contains(err.Error(), "payload") {
		t.Fatalf("tampered payload = %v, want a payload refusal", err)
	}
	if got := looseObjects(t, commonDir); got != objectsBefore {
		t.Fatalf("tampered payload wrote %d loose objects, had %d", got, objectsBefore)
	}

	badSignature := externallySign(t, prepared, actor)
	badSignature.ActorSignature[0] ^= 0xff
	if _, err := workspace.AppendSigned(ctx, badSignature); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("tampered signature = %v, want a signature refusal", err)
	}

	actorMismatch := externallySign(t, prepared, actor)
	actorMismatch.ActorKey = testKey(t).Public().(ed25519.PublicKey)
	if _, err := workspace.AppendSigned(ctx, actorMismatch); err == nil || err.Error() != "invalid actor signature" {
		t.Fatalf("actor-key mismatch = %v, want invalid actor signature", err)
	}

	// Version zero is canonically encoded as 0x00. Its two-byte CBOR form has
	// the same value, but AppendSigned must still reject it as non-canonical.
	noncanonical := externallySign(t, prepared, actor)
	noncanonical.Prepared.Intent = make([]byte, 0, len(prepared.Intent)+1)
	noncanonical.Prepared.Intent = append(noncanonical.Prepared.Intent, prepared.Intent[0], 0x18, 0x00)
	noncanonical.Prepared.Intent = append(noncanonical.Prepared.Intent, prepared.Intent[2:]...)
	if _, err := workspace.AppendSigned(ctx, noncanonical); err == nil || !strings.Contains(err.Error(), "core-deterministic") {
		t.Fatalf("non-canonical intent = %v, want a canonical-encoding refusal", err)
	}

	decoded, err := intent.Decode(prepared.Intent)
	if err != nil {
		t.Fatal(err)
	}
	for name, test := range map[string]struct {
		mutate func(*intent.Intent)
		want   string
	}{
		"sequence": {
			mutate: func(value *intent.Intent) { value.Target = "git:sha1:" + strings.Repeat("f", 40) },
			want:   "signed act targets a different sequence",
		},
		"namespace": {
			mutate: func(value *intent.Intent) { value.IdempotencyNS = "another-application" },
			want:   "signed act uses a different idempotency namespace",
		},
		"schema": {
			mutate: func(value *intent.Intent) { value.Schema = "gitseq/app-binding@0" },
			want:   `schema "gitseq/app-binding@0" is reserved for the host binding`,
		},
		"envelope version": {
			mutate: func(value *intent.Intent) { value.EnvelopeVersion = 1 },
			want:   "signed act uses host-unsupported envelope fields",
		},
		"capability hash": {
			mutate: func(value *intent.Intent) { value.CapabilityHash = []byte{1} },
			want:   "signed act uses host-unsupported envelope fields",
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := decoded
			test.mutate(&changed)
			signed, err := intent.Sign(changed, actor)
			if err != nil {
				t.Fatal(err)
			}
			_, err = workspace.AppendSigned(ctx, host.SignedAct{
				Prepared: host.PreparedAct{Intent: signed.Intent, Payload: prepared.Payload},
				ActorKey: signed.ActorKey, ActorSignature: signed.Signature,
			})
			if err == nil || err.Error() != test.want {
				t.Fatalf("valid signature over mutated %s = %v, want %q", name, err, test.want)
			}
		})
	}
	after, err := workspace.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Head != baseline.Head || after.Depth != baseline.Depth || looseObjects(t, commonDir) != objectsBefore {
		t.Fatalf("rejected public inputs mutated the sequence: before=%d@%s after=%d@%s", baseline.Depth, baseline.Head, after.Depth, after.Head)
	}
}

// A cloned repository that already has the sequence refs is opened, not
// initialized. It gains no Gitseq config and no replacement genesis; explicit
// sequencer custody makes that attached sequence writable through the public
// package alone.
func TestOpenAttachedOpensAnExistingSequenceForWriting(t *testing.T) {
	ctx := context.Background()
	source, workspace, _ := initialized(t, ctx)
	before, err := workspace.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, sourceCommon, err := apphost.ResolveGitDirs(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	config, err := apphost.LoadConfig(apphost.MetaDir(sourceCommon))
	if err != nil {
		t.Fatal(err)
	}

	clone := testRepo(t)
	ref := "refs/seq/" + before.Genesis
	if output, err := exec.Command("git", "-C", clone, "fetch", "--no-tags", source, ref+":"+ref).CombinedOutput(); err != nil {
		t.Fatalf("fetch attached sequence: %v: %s", err, output)
	}
	_, cloneCommon, err := apphost.ResolveGitDirs(ctx, clone)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(apphost.MetaDir(cloneCommon), apphost.ConfigFile)
	if _, err := os.Stat(configPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fresh clone Gitseq config = %v, want absent", err)
	}

	attached, err := host.OpenAttached(ctx, clone, testApplication(), host.Attachment{
		Genesis: before.Genesis, SequencerKey: config.SequencerKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	actor := testKey(t)
	prepared, err := attached.Prepare(host.Act{Schema: "test/attached@0", Payload: []byte("written"), IdempotencyKey: "attached-1"})
	if err != nil {
		t.Fatal(err)
	}
	record, err := attached.AppendSigned(ctx, externallySign(t, prepared, actor))
	if err != nil {
		t.Fatal(err)
	}
	after, err := attached.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Genesis != before.Genesis || after.Depth != before.Depth+1 || after.Records[len(after.Records)-1].ID != record.ID {
		t.Fatalf("attached log = %+v, want existing genesis %s advanced once", after, before.Genesis)
	}
	if _, err := os.Stat(configPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("OpenAttached created configuration: %v", err)
	}
}

// The event identifier embeds the log's genesis, which is what keeps a
// reference from one repository unambiguous in another.
func TestRecordIdentifiersEmbedTheirGenesis(t *testing.T) {
	ctx := context.Background()
	_, workspace, key := initialized(t, ctx)
	record, err := workspace.Append(ctx, key, host.Act{Schema: "test/move@0", Payload: []byte("x")})
	if err != nil {
		t.Fatal(err)
	}
	log, err := workspace.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(record.ID, "git:") || !strings.Contains(record.ID, log.Genesis+"#git:") {
		t.Fatalf("record id %q does not embed genesis %s as a format-qualified object id", record.ID, log.Genesis)
	}
}

// Reading again after appending must not lose or reorder what came before.
func TestRecordsAdvanceIncrementallyWithoutLosingHistory(t *testing.T) {
	ctx := context.Background()
	_, workspace, key := initialized(t, ctx)
	if _, err := workspace.Append(ctx, key, host.Act{Schema: "test/a@0", Payload: []byte("1")}); err != nil {
		t.Fatal(err)
	}
	first, err := workspace.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	firstIDs := ids(first)
	if _, err := workspace.Append(ctx, key, host.Act{Schema: "test/a@0", Payload: []byte("2")}); err != nil {
		t.Fatal(err)
	}
	second, err := workspace.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	secondIDs := ids(second)
	if len(secondIDs) != len(firstIDs)+1 {
		t.Fatalf("read %d records after appending one to %d", len(secondIDs), len(firstIDs))
	}
	for index, id := range firstIDs {
		if secondIDs[index] != id {
			t.Fatalf("record %d changed from %s to %s across an incremental read", index, id, secondIDs[index])
		}
	}
	if second.Depth != first.Depth+1 || second.Head == first.Head {
		t.Fatalf("frontier %d@%s did not advance from %d@%s", second.Depth, second.Head, first.Depth, first.Head)
	}
}

// A retry is the same act. Without this an acknowledgement lost in transit
// would cost a duplicate move.
func TestRepeatedIdempotencyKeyAppendsOnce(t *testing.T) {
	ctx := context.Background()
	_, workspace, key := initialized(t, ctx)
	act := host.Act{Schema: "test/move@0", Payload: []byte(`{"m":"e4"}`), IdempotencyKey: "move-1"}
	first, err := workspace.Append(ctx, key, act)
	if err != nil {
		t.Fatal(err)
	}
	second, err := workspace.Append(ctx, key, act)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("retry produced %s, want the record %s that already exists", second.ID, first.ID)
	}
	log, err := workspace.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if log.Depth != 2 {
		t.Fatalf("log depth %d, want the binding and one act", log.Depth)
	}
}

// The binding is host vocabulary. An application that could write one through
// the ordinary act path could rebind the repository without ever going
// through Init.
func TestAppendRefusesTheReservedBindingSchema(t *testing.T) {
	ctx := context.Background()
	_, workspace, key := initialized(t, ctx)
	_, err := workspace.Append(ctx, key, host.Act{Schema: "gitseq/app-binding@0", Payload: []byte(`{"application":"other","fold_version":"x"}`)})
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("append = %v, want the reserved host schema refused", err)
	}
}

// The ceiling is fixed at genesis and cannot be raised later, so a deployment
// that sets a tight one must actually get it. It bounds the whole event, so
// the payload that fits is smaller than the ceiling itself.
func TestPayloadCeilingIsEnforcedAtTheBoundTheApplicationChose(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	key := testKey(t)
	workspace, err := host.Init(ctx, repo, testApplication(), key, host.Options{PayloadCeiling: 2048})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.Append(ctx, key, host.Act{Schema: "test/a@0", Payload: bytes.Repeat([]byte("x"), 512)}); err != nil {
		t.Fatalf("append within the ceiling = %v", err)
	}
	if _, err := workspace.Append(ctx, key, host.Act{Schema: "test/a@0", Payload: bytes.Repeat([]byte("x"), 4096)}); err == nil {
		t.Fatal("append above the ceiling was admitted")
	}
}

// The kernel validates before it writes, and Append must not get ahead of it.
// Writing the payload tree first left the objects of every refused act in the
// repository, unreachable and never collected, so a submit path open to anyone
// holding a key was also a way to fill a disk with content nothing references.
func TestAnActRefusedAboveTheCeilingWritesNoObjects(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	key := testKey(t)
	workspace, err := host.Init(ctx, repo, testApplication(), key, host.Options{PayloadCeiling: 2048})
	if err != nil {
		t.Fatal(err)
	}
	_, commonDir, err := apphost.ResolveGitDirs(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	before := looseObjects(t, commonDir)
	if _, err := workspace.Append(ctx, key, host.Act{Schema: "test/a@0", Payload: bytes.Repeat([]byte("x"), 4096)}); err == nil {
		t.Fatal("append above the ceiling was admitted")
	}
	if after := looseObjects(t, commonDir); after != before {
		t.Fatalf("a refused act left %d new objects in the repository, want none", after-before)
	}
}

// The genesis ceiling is unsigned and the blob-read limit that carries it is
// not. A ceiling above MaxInt64 once narrowed to a negative limit, and every
// binding record read as unreadable: the repository initialized without
// complaint and then opened as bound to nothing at all.
func TestARepositoryWhoseCeilingExceedsTheSignedReadLimitStillOpens(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	key := testKey(t)
	if _, err := host.Init(ctx, repo, testApplication(), key, host.Options{PayloadCeiling: math.MaxUint64}); err != nil {
		t.Fatal(err)
	}
	if _, err := host.Open(ctx, repo, testApplication()); err != nil {
		t.Fatalf("open = %v, want the binding read under a ceiling above the signed read limit", err)
	}
}

// looseObjects counts the objects in a repository. Nothing here packs or
// collects, so the count moves only when something writes.
func looseObjects(t *testing.T, commonDir string) int {
	t.Helper()
	output, err := exec.Command("git", "--git-dir", commonDir, "count-objects", "-v").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(output), "\n") {
		if rest, found := strings.CutPrefix(line, "count: "); found {
			count, err := strconv.Atoi(strings.TrimSpace(rest))
			if err != nil {
				t.Fatal(err)
			}
			return count
		}
	}
	t.Fatalf("git count-objects reported no object count: %s", output)
	return 0
}

// A repository is one application's for life, and the refusal says which one
// it is so a reader knows what would interpret it.
func TestOpenRefusesAnotherApplication(t *testing.T) {
	ctx := context.Background()
	repo, _, _ := initialized(t, ctx)
	other := host.Application{Name: "other-application", FoldVersion: testFold}
	_, err := host.Open(ctx, repo, other)
	if !errors.Is(err, host.ErrUninterpretable) {
		t.Fatalf("open = %v, want ErrUninterpretable", err)
	}
	if !strings.Contains(err.Error(), testName) {
		t.Fatalf("refusal %q does not name the application the repository is bound to", err)
	}
}

// A fold change is what invalidates every reader's understanding, so the
// version is checked as strictly as the name.
func TestOpenRefusesAnotherFoldVersion(t *testing.T) {
	ctx := context.Background()
	repo, _, _ := initialized(t, ctx)
	upgraded := host.Application{Name: testName, FoldVersion: testFold + "-next"}
	_, err := host.Open(ctx, repo, upgraded)
	if !errors.Is(err, host.ErrUninterpretable) {
		t.Fatalf("open = %v, want ErrUninterpretable", err)
	}
	if !strings.Contains(err.Error(), testFold) {
		t.Fatalf("refusal %q does not name the fold the repository is bound to", err)
	}
}

func TestInitializingActorReplacesTheBindingAndTheNewFoldOpens(t *testing.T) {
	ctx := context.Background()
	repo, _, initializer := initialized(t, ctx)
	incoming := testApplication()
	incoming.FoldVersion = testFold + "-next"

	replacement, err := host.ReplaceBinding(ctx, repo, incoming, initializer)
	if err != nil {
		t.Fatal(err)
	}
	if replacement.Genesis == "" || replacement.Application != testName ||
		replacement.OutgoingFoldVersion != testFold || replacement.IncomingFoldVersion != incoming.FoldVersion {
		t.Fatalf("replacement = %+v, want the exact genesis and fold transition", replacement)
	}
	decoded, err := apphost.DecodeBinding(replacement.Record.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Genesis != "git:sha1:"+replacement.Genesis || decoded.PreviousFoldVersion != testFold || decoded.FoldVersion != incoming.FoldVersion {
		t.Fatalf("recorded binding = %+v, want the exact genesis and fold transition", decoded)
	}
	if _, err := host.Open(ctx, repo, incoming); err != nil {
		t.Fatalf("new-version build could not open the replaced repository: %v", err)
	}
	if _, err := host.Open(ctx, repo, testApplication()); !errors.Is(err, host.ErrUninterpretable) {
		t.Fatalf("old-version open = %v, want the replacement to be in force", err)
	}
	if _, err := host.ReplaceBinding(ctx, repo, incoming, initializer); err == nil || !strings.Contains(err.Error(), "already in force") {
		t.Fatalf("identical replacement = %v, want a refusal", err)
	}
}

func TestBindingReplacementByAnotherActorLeavesThePreviousBindingInForce(t *testing.T) {
	ctx := context.Background()
	repo, workspace, _ := initialized(t, ctx)
	incoming := testApplication()
	incoming.FoldVersion = testFold + "-next"

	if _, err := host.ReplaceBinding(ctx, repo, incoming, testKey(t)); err == nil || !strings.Contains(err.Error(), "initializing actor") {
		t.Fatalf("unauthorized replacement = %v, want an initializing-actor refusal", err)
	}
	if _, err := host.Open(ctx, repo, testApplication()); err != nil {
		t.Fatalf("previous binding no longer opens: %v", err)
	}
	log, err := workspace.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if log.Depth != 1 {
		t.Fatalf("unauthorized replacement changed log depth to %d, want 1", log.Depth)
	}
}

func TestMalformedBindingShapedRecordLeavesThePreviousBindingInForce(t *testing.T) {
	ctx := context.Background()
	repo, _, initializer := initialized(t, ctx)
	appendRawBinding(t, ctx, repo, initializer, []byte(`{"application":"only-half-a-binding"}`), "malformed-binding")
	_, commonDir, err := apphost.ResolveGitDirs(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	config, err := apphost.LoadConfig(apphost.MetaDir(commonDir))
	if err != nil {
		t.Fatal(err)
	}
	wrongTransition, err := (apphost.Binding{
		Application: testName, FoldVersion: testFold + "-next",
		Genesis: "git:" + config.ObjectFormat + ":" + config.Genesis, PreviousFoldVersion: "some-other-fold@0",
	}).Payload()
	if err != nil {
		t.Fatal(err)
	}
	appendRawBinding(t, ctx, repo, initializer, wrongTransition, "wrong-outgoing-binding")

	if _, err := host.Open(ctx, repo, testApplication()); err != nil {
		t.Fatalf("previous binding no longer opens after malformed record: %v", err)
	}
	incoming := testApplication()
	incoming.FoldVersion = testFold + "-next"
	if _, err := host.Open(ctx, repo, incoming); !errors.Is(err, host.ErrUninterpretable) {
		t.Fatalf("new-version open = %v, want the previous binding to remain in force", err)
	}
}

func appendRawBinding(t *testing.T, ctx context.Context, repo string, signer ed25519.PrivateKey, payload []byte, key string) {
	t.Helper()
	_, commonDir, err := apphost.ResolveGitDirs(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	config, err := apphost.LoadConfig(apphost.MetaDir(commonDir))
	if err != nil {
		t.Fatal(err)
	}
	store := gitstore.Store{Repo: commonDir}
	tree, err := gitstore.HashPayloadTree(config.ObjectFormat, payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := intent.Sign(intent.Intent{
		Version: intent.Version, Target: "git:" + config.ObjectFormat + ":" + config.Genesis,
		Schema: apphost.BindingSchema, PayloadTree: "git:" + config.ObjectFormat + ":" + tree,
		IdempotencyNS: testName, IdempotencyKey: key,
	}, signer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kernel.Submit(ctx, store, kernel.Request{Signed: signed, Payload: payload}, kernel.Options{SigningKey: config.SequencerKey}); err != nil {
		t.Fatal(err)
	}
}

// Repositories older than host bindings declare none, and the compatibility
// rule for them is fixed: no binding means Workroom. An application from
// outside must refuse one rather than read someone else's log.
func TestOpenRefusesAnUnboundWorkroomRepository(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	if _, _, err := app.Init(ctx, repo, "human", 1<<20); err != nil {
		t.Fatal(err)
	}
	_, err := host.Open(ctx, repo, testApplication())
	if !errors.Is(err, host.ErrUninterpretable) {
		t.Fatalf("open = %v, want ErrUninterpretable", err)
	}
	if !strings.Contains(err.Error(), "workroom") {
		t.Fatalf("refusal %q does not name workroom, which an absent binding permanently means", err)
	}
}

func TestInitRefusesARepositoryThatAlreadyHoldsASequence(t *testing.T) {
	ctx := context.Background()
	repo, _, _ := initialized(t, ctx)
	if _, err := host.Init(ctx, repo, testApplication(), testKey(t), host.Options{}); err == nil {
		t.Fatal("init over an existing sequence was allowed")
	}
}

// An application that cannot say what it is cannot bind a repository to
// itself, and a binding missing either field would name nothing a later
// reader could check.
func TestInitAndOpenRequireTheApplicationToIdentifyItself(t *testing.T) {
	ctx := context.Background()
	for _, application := range []host.Application{
		{FoldVersion: testFold},
		{Name: testName},
	} {
		if _, err := host.Init(ctx, testRepo(t), application, testKey(t), host.Options{}); err == nil {
			t.Fatalf("init with application %+v was allowed", application)
		}
		if _, err := host.Open(ctx, testRepo(t), application); err == nil {
			t.Fatalf("open with application %+v was allowed", application)
		}
	}
}

func TestOpenRefusesARepositoryWithNoSequence(t *testing.T) {
	ctx := context.Background()
	if _, err := host.Open(ctx, testRepo(t), testApplication()); err == nil {
		t.Fatal("open of a repository holding no sequence was allowed")
	}
}

func ids(log host.Log) []string {
	out := make([]string, 0, len(log.Records))
	for _, record := range log.Records {
		out = append(out, record.ID)
	}
	return out
}

// Kernel verification outranks the binding refusal, and the order is a
// security property rather than a preference. A refusal from Open asserts that
// the repository verifies and only its meaning is missing, so a chain that
// does not verify must never come back as one: an attacker able to write the
// ref could otherwise dress an unverifiable history up as an application this
// build merely does not hold, and a reader would go looking for the wrong
// binary instead of noticing forged history.
func TestAnUnverifiableChainOutranksTheBindingRefusal(t *testing.T) {
	ctx := context.Background()
	repo, workspace, _ := initialized(t, ctx)
	log, err := workspace.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}

	_, commonDir, err := apphost.ResolveGitDirs(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	store := gitstore.Store{Repo: commonDir}
	ref := kernel.Ref(log.Genesis)
	head, err := store.Head(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	var tree, message string
	if err := store.WalkRevListMetadata(ctx, ref, func(commit gitstore.CommitMetadata) error {
		if commit.OID == head {
			tree, message = commit.Tree, commit.Message
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// A commit signed by a key that is not this repository's sequencer. The
	// binding still reads, because the binding read authenticates the actor
	// rather than the sequencer chain.
	wrongKey := filepath.Join(t.TempDir(), "wrong-sequencer")
	if _, err := gitstore.GenerateSSHKey(ctx, wrongKey); err != nil {
		t.Fatal(err)
	}
	forged, err := store.SignedCommit(ctx, tree, head, message, wrongKey, gitstore.CommitIdentity{
		AuthorName: "hostile", AuthorEmail: "hostile@example.invalid",
		CommitterName: "gitseq sequencer", CommitterEmail: "sequencer@gitseq.invalid",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateRef(ctx, ref, forged, head); err != nil {
		t.Fatal(err)
	}

	// The application this repository really is bound to is refused too: there
	// is no verified sequence to hand it.
	if _, err := host.Open(ctx, repo, testApplication()); err == nil {
		t.Fatal("open returned a workspace over an unverifiable chain")
	}
	// And an application it is not bound to hears about the chain, not about
	// a missing interpreter.
	other := host.Application{Name: "other-application", FoldVersion: testFold}
	err = func() error { _, err := host.Open(ctx, repo, other); return err }()
	if err == nil {
		t.Fatal("open by another application returned a workspace over an unverifiable chain")
	}
	if errors.Is(err, host.ErrUninterpretable) {
		t.Fatalf("open = %v, want the verification failure rather than a claim that the repository verifies", err)
	}
}

// A repository attached for reading holds no sequencer key. The refusal names
// that, rather than surfacing whatever the kernel says about an unsigned
// position.
func TestAppendRefusesAReadOnlyAttachment(t *testing.T) {
	ctx := context.Background()
	repo, _, key := initialized(t, ctx)
	_, commonDir, err := apphost.ResolveGitDirs(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	metaDir := apphost.MetaDir(commonDir)
	config, err := apphost.LoadConfig(metaDir)
	if err != nil {
		t.Fatal(err)
	}
	config.ReadOnly, config.SequencerKey = true, ""
	if err := apphost.SaveConfig(metaDir, config); err != nil {
		t.Fatal(err)
	}
	attached, err := host.Open(ctx, repo, testApplication())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := attached.Records(ctx); err != nil {
		t.Fatalf("read of an attached repository = %v, want the records it can still verify", err)
	}
	_, err = attached.Append(ctx, key, host.Act{Schema: "test/a@0", Payload: []byte("1")})
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("append = %v, want a refusal naming the missing sequencer custody", err)
	}
}
