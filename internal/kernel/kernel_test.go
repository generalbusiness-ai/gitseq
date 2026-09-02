package kernel

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/gitstore"
	"github.com/generalbusiness-ai/gitseq/internal/intent"
	"github.com/generalbusiness-ai/gitseq/internal/observe"
)

type auditObserver struct {
	measurements []observe.Measurement
}

func TestCheckpointEventCacheCompressesDepthAndRoundTrips(t *testing.T) {
	t.Parallel()
	events := make([]Event, checkpointChunkEvents+3)
	for index := range events {
		events[index].Payload = bytes.Repeat([]byte{byte(index)}, 32+index%3)
		if index%1000 == 0 {
			events[index].Attachments = map[string][]byte{"note": []byte(strconv.Itoa(index))}
		}
	}
	var cache checkpointEventCache
	cache.reset(events)
	if cache.err != nil || cache.count != len(events) || cache.chunks.count != 1 || len(cache.tail) != 3 {
		t.Fatalf("checkpoint cache = count %d chunks %d tail %d err %v", cache.count, cache.chunks.count, len(cache.tail), cache.err)
	}
	want := make([]checkpointEvent, len(events))
	for index, event := range events {
		want[index] = checkpointMaterial(event)
	}
	// Full chunks were borrowed only for the synchronous compression step and
	// the bounded tail was cloned. Neither may retain caller-owned bytes.
	events[0].Payload[0] ^= 0xff
	events[len(events)-1].Payload[0] ^= 0xff
	events[0].Attachments["note"][0] ^= 0xff
	stored := checkpoint{
		Schema: checkpointSchema, ObjectFormat: "sha1", Genesis: strings.Repeat("a", 40),
		Head: strings.Repeat("b", 40), Depth: len(events),
		EventCount: cache.count, Cached: true, CachedChunks: cache.chunks, CachedTail: cache.tail,
	}
	data, err := marshalCheckpoint(stored, maxCheckpointBytes)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeCompactCheckpoint(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Events) != len(events) {
		t.Fatalf("decoded events = %d, want %d", len(decoded.Events), len(events))
	}
	for index := range want {
		if !bytes.Equal(decoded.Events[index].Payload, want[index].Payload) || !reflect.DeepEqual(decoded.Events[index].Attachments, want[index].Attachments) {
			t.Fatalf("decoded event %d differs", index)
		}
	}
}

func TestCheckpointEventCacheOwnsTailAcrossBorrowedChunkBoundary(t *testing.T) {
	t.Parallel()
	const prefix = 3
	first := make([]Event, prefix)
	second := make([]Event, checkpointChunkEvents-prefix+checkpointChunkEvents+2)
	for index := range first {
		first[index].Payload = []byte{0x10, byte(index)}
	}
	for index := range second {
		second[index].Payload = []byte{0x20, byte(index), byte(index >> 8)}
	}
	second[0].Attachments = map[string][]byte{"old-tail": []byte("owned")}
	second[checkpointChunkEvents-prefix].Attachments = map[string][]byte{"boundary": []byte("borrowed")}
	second[len(second)-1].Attachments = map[string][]byte{"tail": []byte("owned")}

	all := append(append([]Event(nil), first...), second...)
	want := make([]checkpointEvent, len(all))
	for index, event := range all {
		want[index] = checkpointMaterial(event)
	}

	var cache checkpointEventCache
	cache.appendEvents(first)
	cache.appendEvents(second)
	if cache.err != nil || cache.count != len(all) || cache.chunks.count != 2 || len(cache.tail) != 2 {
		t.Fatalf("checkpoint cache = count %d chunks %d tail %d err %v", cache.count, cache.chunks.count, len(cache.tail), cache.err)
	}

	// The first call's partial tail, the second call's borrowed complete chunk,
	// and the final tail must all be independent once appendEvents returns.
	first[0].Payload[0] ^= 0xff
	second[0].Payload[0] ^= 0xff
	second[0].Attachments["old-tail"][0] ^= 0xff
	borrowed := checkpointChunkEvents - prefix
	second[borrowed].Payload[0] ^= 0xff
	second[borrowed].Attachments["boundary"][0] ^= 0xff
	second[len(second)-1].Payload[0] ^= 0xff
	second[len(second)-1].Attachments["tail"][0] ^= 0xff

	stored := checkpoint{
		Schema: checkpointSchema, ObjectFormat: "sha1", Genesis: strings.Repeat("a", 40),
		Head: strings.Repeat("b", 40), Depth: len(all),
		EventCount: cache.count, Cached: true, CachedChunks: cache.chunks, CachedTail: cache.tail,
	}
	data, err := marshalCheckpoint(stored, maxCheckpointBytes)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeCompactCheckpoint(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Events) != len(want) {
		t.Fatalf("decoded events = %d, want %d", len(decoded.Events), len(want))
	}
	for index := range want {
		if !bytes.Equal(decoded.Events[index].Payload, want[index].Payload) || !reflect.DeepEqual(decoded.Events[index].Attachments, want[index].Attachments) {
			t.Fatalf("decoded event %d differs", index)
		}
	}
}

func (o *auditObserver) Record(_ context.Context, measurement observe.Measurement) {
	o.measurements = append(o.measurements, measurement)
}

type fixtureState struct {
	ctx        context.Context
	store      gitstore.Store
	scratch    gitstore.Store
	signingKey string
	publicKey  string
	genesis    string
	format     string
}

type testCheckpointPointer struct {
	commit   string
	loadErr  error
	storeErr error
	writes   int
}

func (p *testCheckpointPointer) Load() (string, error) { return p.commit, p.loadErr }
func (p *testCheckpointPointer) Store(commit string) error {
	p.writes++
	if p.storeErr != nil {
		return p.storeErr
	}
	p.commit = commit
	return nil
}

func newFixture(t testing.TB, format string) fixtureState {
	return newFixtureWithCeiling(t, format, 1<<20)
}

func newFixtureWithCeiling(t testing.TB, format string, payloadCeiling uint64) fixtureState {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	store, err := gitstore.InitBare(ctx, filepath.Join(root, "domain.git"), format)
	if err != nil {
		t.Fatal(err)
	}
	scratch, err := gitstore.InitBare(ctx, filepath.Join(root, "scratch.git"), format)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(root, "keys", "sequencer")
	publicKey, err := gitstore.GenerateSSHKey(ctx, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	genesis, err := Create(ctx, store, GenesisDescriptor{
		Version: 0, ObjectFormat: format, PayloadCeiling: payloadCeiling,
		SequencerPublicKey: publicKey,
	}, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	return fixtureState{ctx: ctx, store: store, scratch: scratch, signingKey: keyPath, publicKey: publicKey, genesis: genesis, format: format}
}

func TestGenesisDescriptorRoundTripsOnlyCanonicalSequencerKey(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	desc, err := Descriptor(f.ctx, f.store, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := encodeGenesis(desc)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeGenesis(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.SequencerPublicKey != f.publicKey {
		t.Fatalf("decoded key = %q, want %q", decoded.SequencerPublicKey, f.publicKey)
	}

	attackerKey := filepath.Join(t.TempDir(), "attacker")
	attackerPublic, err := gitstore.GenerateSSHKey(f.ctx, attackerKey)
	if err != nil {
		t.Fatal(err)
	}
	malicious := desc
	malicious.SequencerPublicKey = f.publicKey + "\nsequencer " + attackerPublic
	if _, err := encodeGenesis(malicious); err == nil {
		t.Fatal("genesis creation accepted an injected allowed signer")
	}
	enc, _ := deterministicModes()
	maliciousBytes, err := enc.Marshal(malicious)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeGenesis(maliciousBytes); err == nil {
		t.Fatal("auditor decode accepted an injected allowed signer")
	}
}

func TestInjectedGenesisSignerCannotVerifyASequence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	store, err := gitstore.InitBare(ctx, filepath.Join(root, "domain.git"), "sha1")
	if err != nil {
		t.Fatal(err)
	}
	honestKey := filepath.Join(root, "honest")
	honestPublic, err := gitstore.GenerateSSHKey(ctx, honestKey)
	if err != nil {
		t.Fatal(err)
	}
	attackerKey := filepath.Join(root, "attacker")
	attackerPublic, err := gitstore.GenerateSSHKey(ctx, attackerKey)
	if err != nil {
		t.Fatal(err)
	}
	desc := GenesisDescriptor{
		Version: 0, ObjectFormat: "sha1", PayloadCeiling: 1 << 20,
		SequencerPublicKey: honestPublic + "\nsequencer " + attackerPublic,
	}
	enc, _ := deterministicModes()
	encoded, err := enc.Marshal(desc)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := store.EmptyTree(ctx)
	if err != nil {
		t.Fatal(err)
	}
	message := genesisMarker + "\nDescriptor: " + base64.RawURLEncoding.EncodeToString(encoded) + "\n"
	genesis, err := store.SignedCommit(ctx, tree, "", message, honestKey, gitstore.CommitIdentity{
		AuthorName: "genesis", AuthorEmail: "genesis@example.invalid",
		CommitterName: "genesis", CommitterEmail: "genesis@example.invalid",
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("forged")
	payloadTree, err := store.WritePayloadTree(ctx, payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	private := actor(t)
	signed, err := intent.Sign(intent.Intent{
		Version: intent.Version, Target: "git:sha1:" + genesis, Schema: "forged.v0",
		PayloadTree: "git:sha1:" + payloadTree, IdempotencyNS: "test", IdempotencyKey: "forged",
	}, private)
	if err != nil {
		t.Fatal(err)
	}
	forged, err := store.SignedCommit(ctx, payloadTree, genesis, intent.Envelope(signed, nil), attackerKey, gitstore.CommitIdentity{
		AuthorName: "attacker", AuthorEmail: "attacker@example.invalid",
		CommitterName: "attacker", CommitterEmail: "attacker@example.invalid",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateRef(ctx, Ref(genesis), forged, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(ctx, store, genesis); err == nil {
		t.Fatal("sequence signed by an injected second sequencer verified")
	}
}

func TestVerificationIgnoresReplacementGenesisAndEventObjects(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	result, err := Submit(f.ctx, f.store, f.request(t, private, "honest", []byte("honest"), nil), Options{SigningKey: f.signingKey})
	if err != nil {
		t.Fatal(err)
	}
	attackerKey := filepath.Join(t.TempDir(), "attacker")
	attackerPublic, err := gitstore.GenerateSSHKey(f.ctx, attackerKey)
	if err != nil {
		t.Fatal(err)
	}
	emptyTree, err := f.store.EmptyTree(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	attackerGenesisMessage, err := genesisMessage(GenesisDescriptor{
		Version: 0, ObjectFormat: "sha1", PayloadCeiling: 1 << 20, SequencerPublicKey: attackerPublic,
	})
	if err != nil {
		t.Fatal(err)
	}
	attackerGenesis, err := f.store.SignedCommit(f.ctx, emptyTree, "", attackerGenesisMessage, attackerKey, gitstore.CommitIdentity{
		AuthorName: "attacker", AuthorEmail: "attacker@example.invalid",
		CommitterName: "attacker", CommitterEmail: "attacker@example.invalid",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpdateRef(f.ctx, "refs/replace/"+f.genesis, attackerGenesis, ""); err != nil {
		t.Fatal(err)
	}

	honestMessage, err := f.store.CommitMessage(f.ctx, result.Commit)
	if err != nil {
		t.Fatal(err)
	}
	eventMetadata, err := f.store.RevListMetadata(f.ctx, result.Commit)
	if err != nil || len(eventMetadata) == 0 {
		t.Fatalf("read honest event metadata: %+v, %v", eventMetadata, err)
	}
	honestTree := eventMetadata[len(eventMetadata)-1].Tree
	attackerTree, err := f.store.WritePayloadTree(f.ctx, []byte("replacement payload"), nil)
	if err != nil {
		t.Fatal(err)
	}
	attackerEvent, err := f.store.SignedCommit(f.ctx, attackerTree, f.genesis, honestMessage, attackerKey, gitstore.CommitIdentity{
		AuthorName: "attacker", AuthorEmail: "attacker@example.invalid",
		CommitterName: "attacker", CommitterEmail: "attacker@example.invalid",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpdateRef(f.ctx, "refs/replace/"+result.Commit, attackerEvent, ""); err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpdateRef(f.ctx, "refs/replace/"+honestTree, attackerTree, ""); err != nil {
		t.Fatal(err)
	}

	verified, err := Verify(f.ctx, f.store, f.genesis)
	if err != nil {
		t.Fatalf("replacement refs changed sequence verification: %v", err)
	}
	if verified.Genesis != f.genesis || verified.Head != result.Head || verified.Events != 1 {
		t.Fatalf("replacement refs changed verified identity: %+v", verified)
	}
	loaded, err := NewReader(f.store, CheckpointOptions{}).Load(f.ctx, f.genesis)
	if err != nil {
		t.Fatalf("replacement refs changed event payload load: %v", err)
	}
	if len(loaded.Events) != 1 || string(loaded.Events[0].Payload) != "honest" {
		t.Fatalf("replacement refs changed event payload: %+v", loaded.Events)
	}
}

func TestReaderIgnoresReplacementCheckpointObjects(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	if _, err := Submit(f.ctx, f.store, f.request(t, actor(t), "one", []byte("one"), nil), Options{SigningKey: f.signingKey}); err != nil {
		t.Fatal(err)
	}
	checkpointOptions := CheckpointOptions{Enabled: true, SigningKey: f.signingKey}
	if _, err := NewReader(f.store, checkpointOptions).Load(f.ctx, f.genesis); err != nil {
		t.Fatal(err)
	}
	checkpointCommit, err := f.store.Head(f.ctx, CheckpointRef(f.genesis))
	if err != nil {
		t.Fatal(err)
	}
	checkpointMetadata, err := f.store.RevListMetadata(f.ctx, checkpointCommit)
	if err != nil || len(checkpointMetadata) != 1 {
		t.Fatalf("read checkpoint metadata: %+v, %v", checkpointMetadata, err)
	}
	checkpointTree := checkpointMetadata[0].Tree
	checkpointMessage, err := f.store.CommitMessage(f.ctx, checkpointCommit)
	if err != nil {
		t.Fatal(err)
	}
	attackerKey := filepath.Join(t.TempDir(), "checkpoint-attacker")
	if _, err := gitstore.GenerateSSHKey(f.ctx, attackerKey); err != nil {
		t.Fatal(err)
	}
	attackerTree, err := f.store.WriteSingleFileTree(f.ctx, checkpointFile, []byte("replacement checkpoint"))
	if err != nil {
		t.Fatal(err)
	}
	attackerCheckpoint, err := f.store.SignedCommit(f.ctx, attackerTree, "", checkpointMessage, attackerKey, gitstore.CommitIdentity{
		AuthorName: "attacker", AuthorEmail: "attacker@example.invalid",
		CommitterName: "attacker", CommitterEmail: "attacker@example.invalid",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpdateRef(f.ctx, "refs/replace/"+checkpointCommit, attackerCheckpoint, ""); err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpdateRef(f.ctx, "refs/replace/"+checkpointTree, attackerTree, ""); err != nil {
		t.Fatal(err)
	}

	loaded, err := NewReader(f.store, CheckpointOptions{Enabled: true}).Load(f.ctx, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Checkpoint || len(loaded.Events) != 1 || string(loaded.Events[0].Payload) != "one" {
		t.Fatalf("replacement ref displaced signed checkpoint: %+v", loaded)
	}
}

func (f fixtureState) request(t testing.TB, private ed25519.PrivateKey, key string, payload []byte, rests []string) Request {
	t.Helper()
	tree, err := f.scratch.WritePayloadTree(f.ctx, payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := intent.Sign(intent.Intent{
		Version: intent.Version,
		Target:  "git:" + f.format + ":" + f.genesis,
		Schema:  "spike.event.v0", PayloadTree: "git:" + f.format + ":" + tree,
		RestsOn: rests, IdempotencyNS: "test", IdempotencyKey: key,
	}, private)
	if err != nil {
		t.Fatal(err)
	}
	return Request{Signed: signed, Payload: payload}
}

func actor(t testing.TB) ed25519.PrivateKey {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return private
}

func checkpointState(t testing.TB, count int) (fixtureState, ed25519.PrivateKey, []Request, checkpoint) {
	t.Helper()
	f := newFixture(t, "sha1")
	private := actor(t)
	requests := make([]Request, count)
	for index := range requests {
		key := "cached-" + strconv.Itoa(index)
		requests[index] = f.request(t, private, key, []byte(key), nil)
		if _, err := Submit(f.ctx, f.store, requests[index], Options{SigningKey: f.signingKey}); err != nil {
			t.Fatal(err)
		}
	}
	log, err := scanHead(f.ctx, f.store, f.genesis, mustHead(t, f.store, Ref(f.genesis)), true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeCheckpoint(f.ctx, f.store, log, CheckpointOptions{Enabled: true, SigningKey: f.signingKey}); err != nil {
		t.Fatal(err)
	}
	commit := mustHead(t, f.store, CheckpointRef(f.genesis))
	data, err := f.store.ReadFileLimit(f.ctx, commit, checkpointFile, maxCheckpointBytes)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := decodeCheckpoint(data)
	if err != nil {
		t.Fatal(err)
	}
	// Keep the hostile-checkpoint mutation fixtures on the legacy format. The
	// production writer above still exercises compact encoding and decoding;
	// these fixtures then prove old v1 checkpoints remain fully authenticated.
	if stored.Schema == checkpointSchema {
		stored.Schema = legacyCheckpointSchema
		stored.Profile = "fold@1"
		stored.EventCount = 0
		for index, event := range log.Events {
			stored.Events[index].Commit = event.Commit
			stored.Events[index].Timestamp = event.Timestamp
			stored.Events[index].Signed = cloneSigned(event.Signed)
		}
	}
	return f, private, requests, stored
}

func mustHead(t testing.TB, store gitstore.Store, ref string) string {
	t.Helper()
	head, err := store.Head(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	return head
}

func cloneCheckpoint(t testing.TB, stored checkpoint) checkpoint {
	t.Helper()
	data, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	cloned, err := decodeCheckpoint(data)
	if err != nil {
		t.Fatal(err)
	}
	return cloned
}

func publishCheckpointBytes(t testing.TB, f fixtureState, data []byte, signingKey, parent, marker string) {
	t.Helper()
	tree, err := f.store.WriteSingleFileTree(f.ctx, checkpointFile, data)
	if err != nil {
		t.Fatal(err)
	}
	publishCheckpointTree(t, f, tree, signingKey, parent, marker)
}

func publishCheckpointTree(t testing.TB, f fixtureState, tree, signingKey, parent, marker string) {
	t.Helper()
	commit, err := f.store.SignedCommit(f.ctx, tree, parent, marker+"\n", signingKey, gitstore.CommitIdentity{
		AuthorName: "gitseq checkpoint", AuthorEmail: "checkpoint@gitseq.invalid",
		CommitterName: "gitseq sequencer", CommitterEmail: "sequencer@gitseq.invalid",
	})
	if err != nil {
		t.Fatal(err)
	}
	ref := CheckpointRef(f.genesis)
	old, _ := f.store.Head(f.ctx, ref)
	if err := f.store.UpdateRef(f.ctx, ref, commit, old); err != nil {
		t.Fatal(err)
	}
}

func publishStoredCheckpoint(t testing.TB, f fixtureState, stored checkpoint) {
	t.Helper()
	data, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	publishCheckpointBytes(t, f, data, f.signingKey, "", legacyCheckpointMarker)
}

func requireCheckpointFallback(t testing.TB, f fixtureState) LoadResult {
	t.Helper()
	reader := NewReader(f.store, CheckpointOptions{Enabled: true})
	loaded, err := reader.Load(f.ctx, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Checkpoint || !loaded.Full || reader.fullScans != 1 || reader.checkpointFallbacks != 1 {
		t.Fatalf("hostile checkpoint did not fall back: result=%+v scans=%d fallbacks=%d", loaded, reader.fullScans, reader.checkpointFallbacks)
	}
	return loaded
}

func appendExternalCommit(t *testing.T, f fixtureState, request Request, parent, signingKey string) string {
	t.Helper()
	decoded := mustVerifyIntent(t, request.Signed)
	tree, err := f.store.WritePayloadTree(f.ctx, request.Payload, request.Attachments)
	if err != nil {
		t.Fatal(err)
	}
	_, signedTree, err := gitstore.ParseTypedOID(decoded.PayloadTree)
	if err != nil {
		t.Fatal(err)
	}
	if tree != signedTree {
		t.Fatalf("external payload tree = %s, signed = %s", tree, signedTree)
	}
	commit, err := f.store.SignedCommit(f.ctx, tree, parent, intent.Envelope(request.Signed, decoded.RestsOn), signingKey, gitstore.CommitIdentity{
		AuthorName: "external sequencer", AuthorEmail: "external@example.invalid",
		CommitterName: "gitseq sequencer", CommitterEmail: "sequencer@gitseq.invalid",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpdateRef(f.ctx, Ref(f.genesis), commit, parent); err != nil {
		t.Fatal(err)
	}
	return commit
}

func appendExternalRotation(t *testing.T, f fixtureState, parent, successorPublicKey, signingKey string) string {
	t.Helper()
	message, err := rotationMessage(successorPublicKey)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := f.store.EmptyTree(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	commit, err := f.store.SignedCommit(f.ctx, tree, parent, message, signingKey, gitstore.CommitIdentity{
		AuthorName: "external rotation", AuthorEmail: "rotation@example.invalid",
		CommitterName: "gitseq sequencer", CommitterEmail: "sequencer@gitseq.invalid",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpdateRef(f.ctx, Ref(f.genesis), commit, parent); err != nil {
		t.Fatal(err)
	}
	return commit
}

func signedCommitWithParents(t *testing.T, f fixtureState, tree string, parents []string, message, signingKey string) string {
	t.Helper()
	arguments := []string{"--git-dir", f.store.Repo, "-c", "gpg.format=ssh", "-c", "user.signingKey=" + signingKey, "commit-tree", "-S", tree}
	for _, parent := range parents {
		arguments = append(arguments, "-p", parent)
	}
	command := exec.Command("git", arguments...)
	command.Stdin = strings.NewReader(message)
	command.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=hostile fixture", "GIT_AUTHOR_EMAIL=hostile@example.invalid", "GIT_AUTHOR_DATE=1700000000 +0000",
		"GIT_COMMITTER_NAME=gitseq sequencer", "GIT_COMMITTER_EMAIL=sequencer@gitseq.invalid", "GIT_COMMITTER_DATE=1700000000 +0000",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("signed commit-tree: %v: %s", err, output)
	}
	return strings.TrimSpace(string(output))
}

func rawTree(t *testing.T, store gitstore.Store, listing string) string {
	t.Helper()
	command := exec.Command("git", "--git-dir", store.Repo, "mktree")
	command.Stdin = strings.NewReader(listing)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("mktree: %v: %s", err, output)
	}
	return strings.TrimSpace(string(output))
}

func TestCreateRejectsSigningKeyThatDoesNotMatchDescriptor(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	store, err := gitstore.InitBare(ctx, filepath.Join(root, "domain.git"), "sha1")
	if err != nil {
		t.Fatal(err)
	}
	expectedKey := filepath.Join(root, "expected")
	expectedPublic, err := gitstore.GenerateSSHKey(ctx, expectedKey)
	if err != nil {
		t.Fatal(err)
	}
	wrongKey := filepath.Join(root, "wrong")
	if _, err := gitstore.GenerateSSHKey(ctx, wrongKey); err != nil {
		t.Fatal(err)
	}
	_, err = Create(ctx, store, GenesisDescriptor{
		Version: 0, ObjectFormat: "sha1", PayloadCeiling: 1 << 20,
		SequencerPublicKey: expectedPublic,
	}, wrongKey)
	if err == nil || !strings.Contains(err.Error(), "genesis sequencer signature") {
		t.Fatalf("Create with mismatched signing key = %v, want signature refusal", err)
	}
}

func TestCreateSubmitReplayVerifyObjectFormats(t *testing.T) {
	t.Parallel()
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			f := newFixture(t, format)
			request := f.request(t, actor(t), "one", []byte(`{"hello":"world"}`), nil)
			first, err := Submit(f.ctx, f.store, request, Options{SigningKey: f.signingKey})
			if err != nil {
				t.Fatal(err)
			}
			replay, err := Submit(f.ctx, f.store, request, Options{SigningKey: f.signingKey})
			if err != nil {
				t.Fatal(err)
			}
			if !replay.Replay || replay.Commit != first.Commit {
				t.Fatalf("replay = %#v, first = %#v", replay, first)
			}
			verified, err := Verify(f.ctx, f.store, f.genesis)
			if err != nil {
				t.Fatal(err)
			}
			if verified.Events != 1 || verified.Head != first.Commit {
				t.Fatalf("verification = %#v", verified)
			}
		})
	}
}

func TestSubmitRefusesWrongSequencerKeyBeforeCAS(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	wrongKey := filepath.Join(t.TempDir(), "wrong-sequencer")
	if _, err := gitstore.GenerateSSHKey(f.ctx, wrongKey); err != nil {
		t.Fatal(err)
	}
	request := f.request(t, actor(t), "wrong-sequencer", []byte("wrong-sequencer"), nil)
	before, err := f.store.Head(f.ctx, Ref(f.genesis))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Submit(f.ctx, f.store, request, Options{SigningKey: wrongKey}); err == nil {
		t.Fatal("submission signed by a non-descriptor sequencer was accepted")
	}
	after, err := f.store.Head(f.ctx, Ref(f.genesis))
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("rejected submission advanced ref: before=%s after=%s", before, after)
	}
}

func TestSequencerKeyRotationAcrossFullAndIncrementalVerification(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	reader := NewReader(f.store)
	first, err := Submit(f.ctx, f.store, f.request(t, private, "before-rotation", []byte("before"), nil), Options{SigningKey: f.signingKey})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Load(f.ctx, f.genesis); err != nil {
		t.Fatal(err)
	}

	nextKey := filepath.Join(t.TempDir(), "next-sequencer")
	nextPublic, err := gitstore.GenerateSSHKey(f.ctx, nextKey)
	if err != nil {
		t.Fatal(err)
	}
	rotation, err := Rotate(f.ctx, f.store, f.genesis, nextPublic, Options{SigningKey: f.signingKey})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Submit(f.ctx, f.store, f.request(t, private, "after-rotation", []byte("after"), nil), Options{SigningKey: nextKey})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := reader.Load(f.ctx, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Full || loaded.BaseHead != first.Head || len(loaded.Events) != 1 || loaded.Events[0].Commit != second.Commit {
		t.Fatalf("incremental load across rotation = %+v", loaded)
	}
	if loaded.Verification.Depth != 3 || loaded.Verification.Events != 2 || loaded.Verification.Head != second.Head {
		t.Fatalf("verification after mid-log rotation = %+v", loaded.Verification)
	}

	thirdKey := filepath.Join(t.TempDir(), "third-sequencer")
	thirdPublic, err := gitstore.GenerateSSHKey(f.ctx, thirdKey)
	if err != nil {
		t.Fatal(err)
	}
	headRotation, err := Rotate(f.ctx, f.store, f.genesis, thirdPublic, Options{SigningKey: nextKey})
	if err != nil {
		t.Fatal(err)
	}
	verified, err := Verify(f.ctx, f.store, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	if verified.Depth != 4 || verified.Events != 2 || verified.Head != headRotation.Head {
		t.Fatalf("verification with rotation at head = %+v", verified)
	}
	if rotation.Commit == first.Commit || headRotation.Commit == second.Commit {
		t.Fatal("rotation did not append its own kernel commit")
	}
}

func TestRetiredSequencerKeyIsRefusedAfterRotation(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	nextKey := filepath.Join(t.TempDir(), "next-sequencer")
	nextPublic, err := gitstore.GenerateSSHKey(f.ctx, nextKey)
	if err != nil {
		t.Fatal(err)
	}
	rotation, err := Rotate(f.ctx, f.store, f.genesis, nextPublic, Options{SigningKey: f.signingKey})
	if err != nil {
		t.Fatal(err)
	}
	request := f.request(t, actor(t), "retired-key", []byte("retired"), nil)
	if _, err := Submit(f.ctx, f.store, request, Options{SigningKey: f.signingKey}); err == nil || !strings.Contains(err.Error(), "sequencer signature") {
		t.Fatalf("submission under retired key error = %v", err)
	}
	head, err := f.store.Head(f.ctx, Ref(f.genesis))
	if err != nil {
		t.Fatal(err)
	}
	if head != rotation.Head {
		t.Fatalf("retired-key refusal moved ref: got=%s want=%s", head, rotation.Head)
	}

	forged := appendExternalCommit(t, f, request, rotation.Head, f.signingKey)
	if _, err := Verify(f.ctx, f.store, f.genesis); err == nil || !strings.Contains(err.Error(), forged+" sequencer signature") {
		t.Fatalf("audit of retired-key event error = %v", err)
	}
}

func TestRotationMustBeSignedByCurrentSequencerKey(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	nextKey := filepath.Join(t.TempDir(), "next-sequencer")
	nextPublic, err := gitstore.GenerateSSHKey(f.ctx, nextKey)
	if err != nil {
		t.Fatal(err)
	}
	attackerKey := filepath.Join(t.TempDir(), "attacker-sequencer")
	if _, err := gitstore.GenerateSSHKey(f.ctx, attackerKey); err != nil {
		t.Fatal(err)
	}
	before, err := f.store.Head(f.ctx, Ref(f.genesis))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Rotate(f.ctx, f.store, f.genesis, nextPublic, Options{SigningKey: attackerKey}); err == nil || !strings.Contains(err.Error(), "sequencer signature") {
		t.Fatalf("rotation under non-current key error = %v", err)
	}
	after, err := f.store.Head(f.ctx, Ref(f.genesis))
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("refused rotation moved ref: before=%s after=%s", before, after)
	}

	forged := appendExternalRotation(t, f, before, nextPublic, attackerKey)
	if _, err := Verify(f.ctx, f.store, f.genesis); err == nil || !strings.Contains(err.Error(), forged+" sequencer signature") {
		t.Fatalf("audit of forged rotation error = %v", err)
	}
}

func TestVerifierRejectsWrongSequencerSignatureOnColdAudit(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	wrongKey := filepath.Join(t.TempDir(), "wrong-sequencer")
	if _, err := gitstore.GenerateSSHKey(f.ctx, wrongKey); err != nil {
		t.Fatal(err)
	}
	appendExternalCommit(t, f, f.request(t, actor(t), "wrong-sequencer-cold", []byte("wrong"), nil), f.genesis, wrongKey)
	if _, err := Verify(f.ctx, f.store, f.genesis); err == nil || !strings.Contains(err.Error(), "sequencer signature") {
		t.Fatalf("wrong sequencer cold-audit error = %v", err)
	}
}

func TestSubmitterReusesExactHeadAndRebuildsAfterExternalAdvance(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	submitter := NewSubmitter(f.store, Options{SigningKey: f.signingKey})

	first := f.request(t, private, "first", []byte("first"), nil)
	if _, err := submitter.Submit(f.ctx, first); err != nil {
		t.Fatal(err)
	}
	second := f.request(t, private, "second", []byte("second"), nil)
	if _, err := submitter.Submit(f.ctx, second); err != nil {
		t.Fatal(err)
	}
	if submitter.cache.fullScans != 1 || submitter.cache.cacheHits != 1 {
		t.Fatalf("warm cache counts = scans %d hits %d, want 1 and 1", submitter.cache.fullScans, submitter.cache.cacheHits)
	}
	first.Signed.Intent[0] ^= 0xff
	firstReplay := f.request(t, private, "first", []byte("first"), nil)
	if replay, err := submitter.Submit(f.ctx, firstReplay); err != nil || !replay.Replay {
		t.Fatalf("caller mutation corrupted cached intent: replay=%+v err=%v", replay, err)
	}

	external := f.request(t, private, "external", []byte("external"), nil)
	if _, err := Submit(f.ctx, f.store, external, Options{SigningKey: f.signingKey}); err != nil {
		t.Fatal(err)
	}
	afterExternal := f.request(t, private, "after-external", []byte("after-external"), nil)
	if _, err := submitter.Submit(f.ctx, afterExternal); err != nil {
		t.Fatal(err)
	}
	if submitter.cache.fullScans != 1 || submitter.cache.deltaScans != 1 {
		t.Fatalf("external advance scans = full %d delta %d, want 1 and 1", submitter.cache.fullScans, submitter.cache.deltaScans)
	}

	replay, err := submitter.Submit(f.ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	if !replay.Replay {
		t.Fatal("warm submitter did not preserve idempotent replay")
	}
	conflict := f.request(t, private, "second", []byte("different"), nil)
	if _, err := submitter.Submit(f.ctx, conflict); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("cached idempotency conflict = %v", err)
	}
	verified, err := Verify(f.ctx, f.store, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	if verified.Events != 4 || verified.Head != submitter.cache.head {
		t.Fatalf("verification = %+v cache head = %s", verified, submitter.cache.head)
	}
}

func TestSubmitterRebuildsAndRetriesAfterCASLoss(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	arrived := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	submitter := NewSubmitter(f.store, Options{SigningKey: f.signingKey, Failpoint: func(name string) {
		if name == "before_ref_cas" {
			once.Do(func() {
				close(arrived)
				<-release
			})
		}
	}})

	type outcome struct {
		result Result
		err    error
	}
	completed := make(chan outcome, 1)
	residentRequest := f.request(t, private, "resident", []byte("resident"), nil)
	go func() {
		result, err := submitter.Submit(f.ctx, residentRequest)
		completed <- outcome{result: result, err: err}
	}()
	<-arrived
	if _, err := Submit(f.ctx, f.store, f.request(t, private, "external", []byte("external"), nil), Options{SigningKey: f.signingKey}); err != nil {
		t.Fatal(err)
	}
	close(release)
	out := <-completed
	if out.err != nil {
		t.Fatal(out.err)
	}
	if out.result.CASRetries != 1 {
		t.Fatalf("CAS retries = %d, want 1", out.result.CASRetries)
	}
	if submitter.cache.fullScans != 1 || submitter.cache.deltaScans != 1 {
		t.Fatalf("CAS loss scans = full %d delta %d, want 1 and 1", submitter.cache.fullScans, submitter.cache.deltaScans)
	}
	verified, err := Verify(f.ctx, f.store, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	if verified.Events != 2 || verified.Head != out.result.Head {
		t.Fatalf("verification = %+v result = %+v", verified, out.result)
	}
}

func TestPostDedupRunsOncePerNewSubmissionAndNeverForAReplay(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	var seen []Application
	submitter := NewSubmitter(f.store, Options{
		SigningKey: f.signingKey,
		PostDedup: func(_ context.Context, application Application) error {
			seen = append(seen, application)
			return nil
		},
	})
	first := f.request(t, private, "guarded", []byte("first"), nil)
	result, err := submitter.Submit(f.ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 {
		t.Fatalf("post-dedup hook calls = %d, want 1", len(seen))
	}
	if string(seen[0].Payload) != "first" || seen[0].Head != result.BaseHead || seen[0].Intent.IdempotencyKey != "guarded" {
		t.Fatalf("post-dedup application = %+v", seen[0])
	}
	replay, err := submitter.Submit(f.ctx, first)
	if err != nil || !replay.Replay {
		t.Fatalf("exact retry = %+v err %v, want a replay", replay, err)
	}
	if len(seen) != 1 {
		t.Fatalf("replay ran the post-dedup hook again: %d calls", len(seen))
	}
	conflict := f.request(t, private, "guarded", []byte("different"), nil)
	if _, err := submitter.Submit(f.ctx, conflict); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed-intent retry error = %v, want ErrIdempotencyConflict", err)
	}
	if len(seen) != 1 {
		t.Fatalf("conflict ran the post-dedup hook: %d calls", len(seen))
	}
	refused := f.request(t, private, "refused", []byte("refused"), nil)
	blocker := errors.New("no workroom semantics survive this")
	blocked := NewSubmitter(f.store, Options{SigningKey: f.signingKey, PostDedup: func(context.Context, Application) error { return blocker }})
	if _, err := blocked.Submit(f.ctx, refused); !errors.Is(err, blocker) {
		t.Fatalf("refused submission error = %v, want %v", err, blocker)
	}
	verified, err := Verify(f.ctx, f.store, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	if verified.Events != 1 {
		t.Fatalf("a refused submission was sequenced anyway: %d events", verified.Events)
	}
}

func TestCheckReplayDistinguishesAbsentExactAndConflictingIntents(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	request := f.request(t, private, "check-replay", []byte("first"), nil)
	if replay, err := CheckReplay(f.ctx, f.store, request); err != nil || replay {
		t.Fatalf("absent replay = %v, %v", replay, err)
	}
	if _, err := Submit(f.ctx, f.store, request, Options{SigningKey: f.signingKey}); err != nil {
		t.Fatal(err)
	}
	if replay, err := CheckReplay(f.ctx, f.store, request); err != nil || !replay {
		t.Fatalf("exact replay = %v, %v", replay, err)
	}
	conflict := f.request(t, private, "check-replay", []byte("different"), nil)
	if replay, err := CheckReplay(f.ctx, f.store, conflict); replay || !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflicting replay = %v, %v", replay, err)
	}
}

// The hook runs inside the compare-and-swap loop, so a log that moved between
// the head read and the ref update makes it judge the newer world before the
// retried commit is written against it.
func TestPostDedupReevaluatesAgainstTheNewHeadAfterCASLoss(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	var mu sync.Mutex
	var heads []string
	arrived := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	submitter := NewSubmitter(f.store, Options{SigningKey: f.signingKey,
		Failpoint: func(name string) {
			if name == "before_ref_cas" {
				once.Do(func() {
					mu.Lock()
					recorded := append([]string(nil), heads...)
					mu.Unlock()
					if len(recorded) != 1 {
						t.Errorf("hook ran %d times before the first CAS, want 1", len(recorded))
						close(arrived)
						close(release)
						return
					}
					close(arrived)
					<-release
				})
			}
		},
		PostDedup: func(_ context.Context, application Application) error {
			mu.Lock()
			heads = append(heads, application.Head)
			mu.Unlock()
			return nil
		},
	})
	type outcome struct {
		result Result
		err    error
	}
	completed := make(chan outcome, 1)
	residentRequest := f.request(t, private, "resident", []byte("resident"), nil)
	go func() {
		result, err := submitter.Submit(f.ctx, residentRequest)
		completed <- outcome{result: result, err: err}
	}()
	<-arrived
	external, err := Submit(f.ctx, f.store, f.request(t, private, "external", []byte("external"), nil), Options{SigningKey: f.signingKey})
	if err != nil {
		t.Fatal(err)
	}
	close(release)
	out := <-completed
	if out.err != nil {
		t.Fatal(out.err)
	}
	if out.result.CASRetries != 1 {
		t.Fatalf("CAS retries = %d, want 1", out.result.CASRetries)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(heads) != 2 {
		t.Fatalf("post-dedup hook ran %d times, want once per attempt", len(heads))
	}
	if heads[0] == heads[1] || heads[1] != external.Head {
		t.Fatalf("hook heads = [%s, %s], want the second attempt to see the external head %s", heads[0], heads[1], external.Head)
	}
}

func BenchmarkSubmitSequence(b *testing.B) {
	for _, resident := range []bool{false, true} {
		name := "stateless"
		if resident {
			name = "resident"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			f := newFixture(b, "sha1")
			private := actor(b)
			requests := make([]Request, b.N)
			for index := range requests {
				key := "event-" + strconv.Itoa(index)
				requests[index] = f.request(b, private, key, []byte(key), nil)
			}
			var submitter *Submitter
			if resident {
				submitter = NewSubmitter(f.store, Options{SigningKey: f.signingKey})
			}
			b.ResetTimer()
			for _, request := range requests {
				var err error
				if resident {
					_, err = submitter.Submit(f.ctx, request)
				} else {
					_, err = Submit(f.ctx, f.store, request, Options{SigningKey: f.signingKey})
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkColdAudit(b *testing.B) {
	f := newFixture(b, "sha1")
	private := actor(b)
	submitter := NewSubmitter(f.store, Options{SigningKey: f.signingKey})
	heads := make(map[int]string)
	for index := 1; index <= 100; index++ {
		key := "cold-" + strconv.Itoa(index)
		result, err := submitter.Submit(f.ctx, f.request(b, private, key, []byte(key), nil))
		if err != nil {
			b.Fatal(err)
		}
		if index == 1 || index == 10 || index == 100 {
			heads[index] = result.Head
		}
	}
	for _, count := range []int{1, 10, 100} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := scanHead(f.ctx, f.store, f.genesis, heads[count], true, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestColdAuditUsesConstantGitProcesses(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	submitter := NewSubmitter(f.store, Options{SigningKey: f.signingKey})
	for index := 0; index < 10; index++ {
		key := "constant-process-" + strconv.Itoa(index)
		if _, err := submitter.Submit(f.ctx, f.request(t, private, key, []byte(key), nil)); err != nil {
			t.Fatal(err)
		}
	}
	observer := &auditObserver{}
	f.store.Observer = observer
	verified, err := Verify(f.ctx, f.store, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	if verified.Events != 10 {
		t.Fatalf("verified events = %d, want 10", verified.Events)
	}
	var refs, scans, signatures int
	for _, measurement := range observer.measurements {
		if measurement.Operation != observe.OperationGit {
			continue
		}
		switch measurement.Path {
		case observe.PathRef:
			refs++
		case observe.PathScan:
			scans++
		case observe.PathSignature:
			signatures++
		}
	}
	if refs != 1 || scans != 2 || signatures != 0 {
		t.Fatalf("cold audit Git processes: refs=%d scans=%d signatures=%d, want 1/2/0", refs, scans, signatures)
	}
}

func TestDeltaAuditUsesConstantGitProcesses(t *testing.T) {
	t.Parallel()
	for _, listed := range []bool{false, true} {
		route := "streamed"
		if listed {
			route = "listed"
		}
		for _, count := range []int{1, 100} {
			t.Run(route+"/tail-"+strconv.Itoa(count), func(t *testing.T) {
				f := newFixture(t, "sha1")
				private := actor(t)
				submitter := NewSubmitter(f.store, Options{SigningKey: f.signingKey})
				baseHead, err := f.store.Head(f.ctx, Ref(f.genesis))
				if err != nil {
					t.Fatal(err)
				}
				base, err := scanHead(f.ctx, f.store, f.genesis, baseHead, true, nil)
				if err != nil {
					t.Fatal(err)
				}
				head := baseHead
				for index := 0; index < count; index++ {
					key := route + "-constant-process-" + strconv.Itoa(index)
					result, err := submitter.Submit(f.ctx, f.request(t, private, key, []byte(key), nil))
					if err != nil {
						t.Fatal(err)
					}
					head = result.Head
				}
				expected, err := scanHead(f.ctx, f.store, f.genesis, head, true, nil)
				if err != nil {
					t.Fatal(err)
				}
				var commits []gitstore.CommitMetadata
				if listed {
					err := f.store.WalkRevListMetadataAfter(f.ctx, baseHead, head, func(commit gitstore.CommitMetadata) error {
						commits = append(commits, commit)
						return nil
					})
					if err != nil {
						t.Fatal(err)
					}
				}

				observer := &auditObserver{}
				f.store.Observer = observer
				var got scannedLog
				if listed {
					got, err = scanListedAfter(f.ctx, f.store, base, head, commits, true)
				} else {
					got, err = scanAfter(f.ctx, f.store, base, head, true)
				}
				if err != nil {
					t.Fatal(err)
				}
				if got.Verification != expected.Verification || !reflect.DeepEqual(got.Events, expected.Events[len(base.Events):]) || !reflect.DeepEqual(got.Dedup, expected.Dedup) {
					t.Fatalf("delta differs from cold audit:\ndelta=%+v\ncold=%+v", got.Verification, expected.Verification)
				}
				var refs, scans, signatures int
				for _, measurement := range observer.measurements {
					if measurement.Operation != observe.OperationGit {
						continue
					}
					switch measurement.Path {
					case observe.PathRef:
						refs++
					case observe.PathScan:
						scans++
					case observe.PathSignature:
						signatures++
					}
				}
				if refs != 0 || scans != 2 || signatures != 0 {
					t.Fatalf("delta audit Git processes: refs=%d scans=%d signatures=%d, want 0/2/0", refs, scans, signatures)
				}
			})
		}
	}
}

func TestListedDeltaUsesOnlyEnumeratedObjectIDs(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	submitter := NewSubmitter(f.store, Options{SigningKey: f.signingKey})
	baseHead, err := f.store.Head(f.ctx, Ref(f.genesis))
	if err != nil {
		t.Fatal(err)
	}
	freshBase := func() scannedLog {
		t.Helper()
		log, err := scanHead(f.ctx, f.store, f.genesis, baseHead, true, nil)
		if err != nil {
			t.Fatal(err)
		}
		return log
	}
	head := baseHead
	for index := 0; index < 6; index++ {
		key := "listed-object-id-" + strconv.Itoa(index)
		result, err := submitter.Submit(f.ctx, f.request(t, private, key, []byte(key), nil))
		if err != nil {
			t.Fatal(err)
		}
		head = result.Head
	}
	var honest []gitstore.CommitMetadata
	if err := f.store.WalkRevListMetadataAfter(f.ctx, baseHead, head, func(commit gitstore.CommitMetadata) error {
		honest = append(honest, commit)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(honest) != 6 {
		t.Fatalf("listed tail has %d records, want 6", len(honest))
	}

	if _, err := scanListedAfter(f.ctx, f.store, freshBase(), head, cloneCommitMetadata(honest), true); err != nil {
		t.Fatalf("honest listed tail refused: %v", err)
	}

	tests := []struct {
		name    string
		mutate  func([]gitstore.CommitMetadata) []gitstore.CommitMetadata
		wantErr bool
	}{
		{"reordered", func(commits []gitstore.CommitMetadata) []gitstore.CommitMetadata {
			commits[2], commits[3] = commits[3], commits[2]
			return commits
		}, true},
		{"dropped-middle", func(commits []gitstore.CommitMetadata) []gitstore.CommitMetadata {
			return append(commits[:2:2], commits[3:]...)
		}, true},
		{"dropped-last", func(commits []gitstore.CommitMetadata) []gitstore.CommitMetadata {
			return commits[:len(commits)-1]
		}, true},
		{"duplicated", func(commits []gitstore.CommitMetadata) []gitstore.CommitMetadata {
			return append(append(commits[:3:3], commits[2]), commits[3:]...)
		}, true},
		{"empty-list-nonmatching-head", func([]gitstore.CommitMetadata) []gitstore.CommitMetadata {
			return nil
		}, true},
		{"forged-metadata-fields", func(commits []gitstore.CommitMetadata) []gitstore.CommitMetadata {
			for index := range commits {
				commits[index].Parents = []string{baseHead}
				commits[index].Tree = "0000000000000000000000000000000000000000"
				commits[index].Message = "forged rotation\n\nGitseq-Rotate-To: ssh-ed25519 AAAA\n"
				commits[index].Timestamp = 1
			}
			return commits
		}, false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := scanListedAfter(f.ctx, f.store, freshBase(), head, test.mutate(cloneCommitMetadata(honest)), true)
			if test.wantErr {
				if err == nil {
					t.Fatalf("hostile listed tail %q was accepted", test.name)
				}
				return
			}
			if err != nil {
				t.Fatalf("forged manifest metadata changed verification: %v", err)
			}
			if got.Verification.Head != head || got.Verification.Events != 6 {
				t.Fatalf("forged manifest metadata altered the result: %+v", got.Verification)
			}
		})
	}
}

func cloneCommitMetadata(commits []gitstore.CommitMetadata) []gitstore.CommitMetadata {
	cloned := make([]gitstore.CommitMetadata, len(commits))
	copy(cloned, commits)
	return cloned
}

// Run with -benchtime=1x. Setup deliberately constructs an ordinary 20,000
// event history through the signed resident submission path; only restart is
// timed.
func BenchmarkCheckpointRestartAtDepth20000(b *testing.B) {
	if b.N != 1 {
		b.Skip("run with -benchtime=1x")
	}
	f := newFixture(b, "sha1")
	private := actor(b)
	options := Options{SigningKey: f.signingKey, CheckpointEnabled: true}
	submitter := NewSubmitter(f.store, options)
	for index := 0; index < 20000; index++ {
		key := "checkpoint-20k-" + strconv.Itoa(index)
		if _, err := submitter.Submit(f.ctx, f.request(b, private, key, []byte(key), nil)); err != nil {
			b.Fatal(err)
		}
	}
	checkpointCommit, err := f.store.Head(f.ctx, CheckpointRef(f.genesis))
	if err != nil {
		b.Fatal(err)
	}
	data, err := f.store.ReadFileLimit(f.ctx, checkpointCommit, checkpointFile, maxCheckpointBytes)
	if err != nil {
		b.Fatal(err)
	}
	stored, err := decodeCheckpoint(data)
	if err != nil {
		b.Fatal(err)
	}
	if behind := 20000 - stored.Depth; behind < 0 || behind >= checkpointInterval {
		b.Fatalf("checkpoint depth %d leaves unbounded delta %d", stored.Depth, behind)
	}
	b.ReportAllocs()
	b.ResetTimer()
	reader := NewReader(f.store, CheckpointOptions{Enabled: true, SigningKey: f.signingKey})
	loaded, err := reader.Load(f.ctx, f.genesis)
	if err != nil {
		b.Fatal(err)
	}
	b.StopTimer()
	b.ReportMetric(float64(stored.Depth), "checkpoint_events")
	b.ReportMetric(float64(20000-stored.Depth), "delta_events")
	if !loaded.Checkpoint || loaded.Verification.Events != 20000 || reader.fullScans != 0 {
		b.Fatalf("20k restart = %+v, full scans=%d", loaded, reader.fullScans)
	}
}

// Run with -benchtime=1x. The result deliberately reports the checkpoint tail:
// restart cost includes one complete local metadata enumeration to bind cached
// events to their immutable commits, plus full verification of that tail.
func BenchmarkCheckpointRestartAtDepth1000(b *testing.B) {
	if b.N != 1 {
		b.Skip("run with -benchtime=1x")
	}
	f := newFixture(b, "sha1")
	private := actor(b)
	options := Options{SigningKey: f.signingKey, CheckpointEnabled: true}
	submitter := NewSubmitter(f.store, options)
	for index := 0; index < 1000; index++ {
		key := "checkpoint-1k-" + strconv.Itoa(index)
		if _, err := submitter.Submit(f.ctx, f.request(b, private, key, []byte(key), nil)); err != nil {
			b.Fatal(err)
		}
	}
	checkpointCommit, err := f.store.Head(f.ctx, CheckpointRef(f.genesis))
	if err != nil {
		b.Fatal(err)
	}
	data, err := f.store.ReadFileLimit(f.ctx, checkpointCommit, checkpointFile, maxCheckpointBytes)
	if err != nil {
		b.Fatal(err)
	}
	stored, err := decodeCheckpoint(data)
	if err != nil {
		b.Fatal(err)
	}
	head := mustHead(b, f.store, Ref(f.genesis))
	b.Run("checkpoint", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, _, err := loadCheckpoint(f.ctx, f.store, f.genesis, head, CheckpointOptions{Enabled: true}); err != nil {
				b.Fatal(err)
			}
		}
		b.ReportMetric(float64(stored.Depth), "checkpoint_events")
		b.ReportMetric(float64(1000-stored.Depth), "delta_events")
	})
	b.Run("cold", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := scanHead(f.ctx, f.store, f.genesis, head, true, nil); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// Run with -benchtime=1x. The 480-byte bodies are deliberately
// incompressible: together with the compact framing they model a serialized
// cost above the 450 bytes/event measured from the live Workroom payload mix.
func BenchmarkCompactCheckpointSerializationAtDepth500000(b *testing.B) {
	if b.N != 1 {
		b.Skip("run with -benchtime=1x")
	}
	const (
		eventCount  = 500000
		payloadSize = 480
	)
	payloads := make([]byte, eventCount*payloadSize)
	state := uint64(0x9e3779b97f4a7c15)
	for index := range payloads {
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		payloads[index] = byte(state)
	}
	stored := checkpoint{
		Schema: checkpointSchema, ObjectFormat: "sha1",
		Genesis: strings.Repeat("a", 40), Head: strings.Repeat("b", 40),
		Depth: eventCount, EventCount: eventCount,
		Events: make([]checkpointEvent, eventCount),
	}
	for index := range stored.Events {
		start := index * payloadSize
		stored.Events[index].Payload = payloads[start : start+payloadSize]
	}
	b.ReportAllocs()
	b.ResetTimer()
	data, err := marshalCheckpoint(stored, maxCheckpointBytes)
	if err != nil {
		b.Fatal(err)
	}
	stored.Events = nil
	payloads = nil
	runtime.GC()
	decoded, err := decodeCheckpoint(data)
	if err != nil {
		b.Fatal(err)
	}
	b.StopTimer()
	b.ReportMetric(float64(len(data))/(1<<20), "checkpoint_MiB")
	b.ReportMetric(float64(len(data))/eventCount, "bytes/event")
	if decoded.EventCount != eventCount || len(decoded.Events) != eventCount {
		b.Fatalf("decoded events = %d/%d, want %d", decoded.EventCount, len(decoded.Events), eventCount)
	}
}

func TestConcurrentCASProducesOneLinearChain(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	requests := []Request{
		f.request(t, private, "a", []byte("A"), nil),
		f.request(t, private, "b", []byte("B"), nil),
	}

	release := make(chan struct{})
	var arrivals atomic.Int32
	hook := func(name string) {
		if name != "before_ref_cas" {
			return
		}
		arrival := arrivals.Add(1)
		if arrival <= 2 {
			if arrival == 2 {
				close(release)
			}
			<-release
		}
	}

	var wg sync.WaitGroup
	results := make([]Result, 2)
	errs := make([]error, 2)
	for index := range requests {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			results[index], errs[index] = Submit(f.ctx, f.store, requests[index], Options{SigningKey: f.signingKey, Failpoint: hook})
		}(index)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if results[0].Commit == results[1].Commit {
		t.Fatal("concurrent distinct requests produced the same commit")
	}
	verified, err := Verify(f.ctx, f.store, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	if verified.Events != 2 {
		t.Fatalf("expected two events, got %#v", verified)
	}
	if results[0].CASRetries+results[1].CASRetries < 1 {
		t.Fatal("race did not exercise a CAS retry")
	}
}

func TestCrashBoundariesRecoverFromLog(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)

	before := f.request(t, private, "before", []byte("before"), nil)
	func() {
		defer func() { _ = recover() }()
		_, _ = Submit(f.ctx, f.store, before, Options{SigningKey: f.signingKey, Failpoint: panicAt("after_commit_written")})
	}()
	result, err := Submit(f.ctx, f.store, before, Options{SigningKey: f.signingKey})
	if err != nil || result.Replay {
		t.Fatalf("retry before CAS = %#v, %v", result, err)
	}

	after := f.request(t, private, "after", []byte("after"), nil)
	func() {
		defer func() { _ = recover() }()
		_, _ = Submit(f.ctx, f.store, after, Options{SigningKey: f.signingKey, Failpoint: panicAt("after_ref_cas")})
	}()
	replay, err := Submit(f.ctx, f.store, after, Options{SigningKey: f.signingKey})
	if err != nil || !replay.Replay {
		t.Fatalf("retry after CAS = %#v, %v", replay, err)
	}
	verified, err := Verify(f.ctx, f.store, f.genesis)
	if err != nil || verified.Events != 2 {
		t.Fatalf("verify = %#v, %v", verified, err)
	}
}

func panicAt(selected string) func(string) {
	return func(name string) {
		if name == selected {
			panic("simulated process death at " + name)
		}
	}
}

func TestIdempotencyConflict(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	first := f.request(t, private, "same", []byte("first"), nil)
	second := f.request(t, private, "same", []byte("second"), nil)
	if _, err := Submit(f.ctx, f.store, first, Options{SigningKey: f.signingKey}); err != nil {
		t.Fatal(err)
	}
	if _, err := Submit(f.ctx, f.store, second, Options{SigningKey: f.signingKey}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("expected idempotency conflict, got %v", err)
	}
}

func TestSizeCeilingAndEnvelopeOnlyAdmissionHook(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	tooLarge := f.request(t, private, "large", make([]byte, (1<<20)+1), nil)
	if _, err := Submit(f.ctx, f.store, tooLarge, Options{SigningKey: f.signingKey}); err == nil {
		t.Fatal("payload above the genesis ceiling was appended")
	}

	proof := []byte("profile capability")
	digest := sha256.Sum256(proof)
	tree, err := f.scratch.WritePayloadTree(f.ctx, []byte("admitted"), nil)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := intent.Sign(intent.Intent{
		Version: intent.Version, Target: "git:" + f.format + ":" + f.genesis, Schema: "admission.v0",
		PayloadTree: "git:" + f.format + ":" + tree, IdempotencyNS: "hook", IdempotencyKey: "one", CapabilityHash: digest[:],
	}, private)
	if err != nil {
		t.Fatal(err)
	}
	request := Request{Signed: signed, Payload: []byte("admitted"), CapabilityProof: proof}
	hookCalled := false
	_, err = Submit(f.ctx, f.store, request, Options{SigningKey: f.signingKey, PreAppend: func(_ context.Context, admission Admission) error {
		hookCalled = true
		if string(admission.CapabilityProof) != string(proof) || admission.Intent.IdempotencyKey != "one" {
			return errors.New("hook received wrong envelope")
		}
		return errors.New("policy refusal")
	}})
	if err == nil || !hookCalled {
		t.Fatalf("envelope-only hook did not refuse: called=%v err=%v", hookCalled, err)
	}
	verification, err := Verify(f.ctx, f.store, f.genesis)
	if err != nil || verification.Events != 0 {
		t.Fatalf("refused admissions changed the log: %#v, %v", verification, err)
	}
}

func TestSubmitChargesPayloadAndAllAttachmentsToOneCeiling(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	payload := make([]byte, 600<<10)
	attachments := map[string][]byte{"first.bin": make([]byte, 220<<10), "second.bin": make([]byte, 220<<10)}
	tree, err := f.scratch.WritePayloadTree(f.ctx, payload, attachments)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := intent.Sign(intent.Intent{Version: intent.Version, Target: "git:" + f.format + ":" + f.genesis, Schema: "ceiling.v0", PayloadTree: "git:" + f.format + ":" + tree, IdempotencyNS: "ceiling", IdempotencyKey: "combined-attachments"}, private)
	if err != nil {
		t.Fatal(err)
	}
	before, err := f.store.Head(f.ctx, Ref(f.genesis))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Submit(f.ctx, f.store, Request{Signed: signed, Payload: payload, Attachments: attachments}, Options{SigningKey: f.signingKey}); err == nil || !strings.Contains(err.Error(), "event exceeds genesis ceiling") {
		t.Fatalf("combined payload and attachments error = %v", err)
	}
	after, err := f.store.Head(f.ctx, Ref(f.genesis))
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("combined ceiling refusal moved sequence: before=%s after=%s", before, after)
	}
}

func TestValidateRequestSizeMatchesSubmitAccountingWithoutWriting(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	request := f.request(t, actor(t), "validate-size", []byte("payload"), nil)
	decoded, err := intent.Verify(request.Signed)
	if err != nil {
		t.Fatal(err)
	}
	exact := uint64(len(intent.Envelope(request.Signed, decoded.RestsOn)) + len(request.Payload))
	before, err := f.store.Head(f.ctx, Ref(f.genesis))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateRequestSize(request, exact); err != nil {
		t.Fatalf("exact request size rejected: %v", err)
	}
	if err := ValidateRequestSize(request, exact-1); err == nil || !strings.Contains(err.Error(), "event exceeds genesis ceiling") {
		t.Fatalf("oversized request validation error = %v", err)
	}
	after, err := f.store.Head(f.ctx, Ref(f.genesis))
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("read-only size validation moved sequence: before=%s after=%s", before, after)
	}
	if _, err := Submit(f.ctx, f.store, request, Options{SigningKey: f.signingKey}); err != nil {
		t.Fatalf("Submit disagreed with successful validation: %v", err)
	}
}

func TestOversizedEnvelopeIsRefusedWithoutPoisoningTheLog(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	ref := strings.Repeat("r", intent.MaxStringBytes)
	large := f.request(t, private, "large-valid-envelope", []byte("valid"), []string{ref, ref, ref})
	if _, err := Submit(f.ctx, f.store, large, Options{SigningKey: f.signingKey}); err != nil {
		t.Fatal(err)
	}
	before, err := f.store.Head(f.ctx, Ref(f.genesis))
	if err != nil {
		t.Fatal(err)
	}

	tooManyBytes := make([]string, 33)
	for index := range tooManyBytes {
		tooManyBytes[index] = ref
	}
	oversized := f.request(t, private, "oversized-envelope", []byte("refuse"), tooManyBytes)
	if _, err := Submit(f.ctx, f.store, oversized, Options{SigningKey: f.signingKey}); err == nil || !strings.Contains(err.Error(), "event exceeds genesis ceiling") {
		t.Fatalf("oversized envelope error = %v", err)
	}
	after, err := f.store.Head(f.ctx, Ref(f.genesis))
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("refused envelope moved sequence: before=%s after=%s", before, after)
	}
	verified, err := Verify(f.ctx, f.store, f.genesis)
	if err != nil || verified.Events != 1 || verified.Head != before {
		t.Fatalf("log after refused envelope = %+v, %v", verified, err)
	}
}

func TestVerifierAppliesCeilingToEnvelopeAndPayloadTogether(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	ref := strings.Repeat("r", intent.MaxStringBytes)
	request := f.request(t, private, "combined-overflow", make([]byte, 900<<10), []string{ref, ref, ref})
	decoded := mustVerifyIntent(t, request.Signed)
	tree, err := f.store.WritePayloadTree(f.ctx, request.Payload, request.Attachments)
	if err != nil {
		t.Fatal(err)
	}
	_, signedTree, err := gitstore.ParseTypedOID(decoded.PayloadTree)
	if err != nil || tree != signedTree {
		t.Fatalf("payload tree = %s, signed = %s, err=%v", tree, signedTree, err)
	}
	message := intent.Envelope(request.Signed, decoded.RestsOn)
	if len(message)+len(request.Payload) <= 1<<20 {
		t.Fatalf("test event is only %d bytes", len(message)+len(request.Payload))
	}
	commit, err := f.store.SignedCommit(f.ctx, tree, f.genesis, message, f.signingKey, gitstore.CommitIdentity{
		AuthorName: "malicious sequencer", AuthorEmail: "sequencer@example.invalid",
		CommitterName: "malicious sequencer", CommitterEmail: "sequencer@example.invalid",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpdateRef(f.ctx, Ref(f.genesis), commit, f.genesis); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(f.ctx, f.store, f.genesis); err == nil || !strings.Contains(err.Error(), "payload exceeds genesis ceiling") {
		t.Fatalf("combined envelope and payload verification error = %v", err)
	}
}

func TestVerifierRejectsIntentReboundToAnotherLog(t *testing.T) {
	t.Parallel()
	fA := newFixture(t, "sha1")
	fB := newFixture(t, "sha1")
	private := actor(t)
	request := fA.request(t, private, "one", []byte("claim"), []string{"git:sha1:external#git:sha1:event"})
	if _, err := Submit(fA.ctx, fA.store, request, Options{SigningKey: fA.signingKey}); err != nil {
		t.Fatal(err)
	}

	// A malicious sequencer for B can copy the actor-signed envelope, but B's
	// verifier must reject the target binding.
	headB, err := fB.store.Head(fB.ctx, Ref(fB.genesis))
	if err != nil {
		t.Fatal(err)
	}
	_, signedTree, _ := gitstore.ParseTypedOID(mustVerifyIntent(t, request.Signed).PayloadTree)
	writtenTreeB, err := fB.store.WritePayloadTree(fB.ctx, request.Payload, request.Attachments)
	if err != nil {
		t.Fatal(err)
	}
	if writtenTreeB != signedTree {
		t.Fatalf("cross-repository tree mismatch: %s != %s", writtenTreeB, signedTree)
	}
	message := intent.Envelope(request.Signed, mustVerifyIntent(t, request.Signed).RestsOn)
	malicious, err := fB.store.SignedCommit(fB.ctx, signedTree, headB, message, fB.signingKey, gitstore.CommitIdentity{
		AuthorName: "attacker", AuthorEmail: "attacker@example.invalid",
		CommitterName: "gitseq sequencer", CommitterEmail: "sequencer@gitseq.invalid",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := fB.store.UpdateRef(fB.ctx, Ref(fB.genesis), malicious, headB); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(fB.ctx, fB.store, fB.genesis); err == nil {
		t.Fatal("verifier accepted an intent rebound into another log")
	}
}

func TestVerifierRejectsAlteredCausalTrailersWithFreshIdentity(t *testing.T) {
	t.Parallel()
	fA := newFixture(t, "sha1")
	private := actor(t)
	request := fA.request(t, private, "altered-trailers-fresh", []byte("claim"), []string{"git:sha1:external#git:sha1:event"})
	decoded := mustVerifyIntent(t, request.Signed)
	writtenTree, err := fA.store.WritePayloadTree(fA.ctx, request.Payload, request.Attachments)
	if err != nil {
		t.Fatal(err)
	}
	_, signedTree, err := gitstore.ParseTypedOID(decoded.PayloadTree)
	if err != nil || writtenTree != signedTree {
		t.Fatalf("payload tree = %s, signed = %s, err=%v", writtenTree, signedTree, err)
	}
	headA, _ := fA.store.Head(fA.ctx, Ref(fA.genesis))
	mutatedMessage := intent.Envelope(request.Signed, []string{"git:sha1:altered#git:sha1:event"})
	mutated, err := fA.store.SignedCommit(fA.ctx, signedTree, headA, mutatedMessage, fA.signingKey, gitstore.CommitIdentity{
		AuthorName: "attacker", AuthorEmail: "attacker@example.invalid",
		CommitterName: "gitseq sequencer", CommitterEmail: "sequencer@gitseq.invalid",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := fA.store.UpdateRef(fA.ctx, Ref(fA.genesis), mutated, headA); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(fA.ctx, fA.store, fA.genesis); err == nil || !strings.Contains(err.Error(), "causal trailers differ") {
		t.Fatalf("altered causal trailer error = %v", err)
	}
}

func TestSequenceBoundsPinNamedGenesisAndHeadIndependently(t *testing.T) {
	t.Parallel()
	commits := []string{"genesis", "event", "head"}
	if err := validateSequenceBounds(commits, "genesis", "head"); err != nil {
		t.Fatalf("valid sequence bounds rejected: %v", err)
	}
	if err := validateSequenceBounds(commits, "other-genesis", "head"); err == nil || !strings.Contains(err.Error(), "named genesis") {
		t.Fatalf("wrong genesis boundary error = %v", err)
	}
	if err := validateSequenceBounds(commits, "genesis", "other-head"); err == nil || !strings.Contains(err.Error(), "named head") {
		t.Fatalf("wrong head boundary error = %v", err)
	}
}

func TestMetadataScanPreservesEstablishedCommitMessageNormalization(t *testing.T) {
	t.Parallel()
	const ceiling = uint64(4096)
	f := newFixtureWithCeiling(t, "sha1", ceiling)
	reader := NewReader(f.store)
	if _, err := reader.Load(f.ctx, f.genesis); err != nil {
		t.Fatal(err)
	}

	request := f.request(t, actor(t), "metadata-message", []byte("payload"), nil)
	decoded := mustVerifyIntent(t, request.Signed)
	tree, err := f.store.WritePayloadTree(f.ctx, request.Payload, request.Attachments)
	if err != nil {
		t.Fatal(err)
	}
	establishedMessage := intent.Envelope(request.Signed, decoded.RestsOn)
	message := establishedMessage + strings.Repeat(" ", int(ceiling)-len(establishedMessage)+64) + "\n"
	commit, err := f.store.SignedCommit(f.ctx, tree, f.genesis, message, f.signingKey, gitstore.CommitIdentity{
		AuthorName: "external sequencer", AuthorEmail: "external@example.invalid",
		CommitterName: "gitseq sequencer", CommitterEmail: "sequencer@gitseq.invalid",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpdateRef(f.ctx, Ref(f.genesis), commit, f.genesis); err != nil {
		t.Fatal(err)
	}

	var metadata []gitstore.CommitMetadata
	err = f.store.WalkRevListMetadataAfter(f.ctx, f.genesis, commit, func(commit gitstore.CommitMetadata) error {
		metadata = append(metadata, commit)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata) != 1 {
		t.Fatalf("metadata suffix length = %d, want 1", len(metadata))
	}
	wantMessage, wantTimestamp, err := f.store.CommitMessageWithTimestamp(f.ctx, commit)
	if err != nil {
		t.Fatal(err)
	}
	if got := normalizeEventMessage(metadata[0].Message); got != wantMessage {
		t.Fatalf("normalized metadata message differs from established scan bytes\ngot:  %q\nwant: %q", got, wantMessage)
	}
	if uint64(len(metadata[0].Message)) <= ceiling || uint64(len(wantMessage)+len(request.Payload)) > ceiling {
		t.Fatalf("test does not cross only the raw metadata ceiling: raw=%d normalized+payload=%d ceiling=%d", len(metadata[0].Message), len(wantMessage)+len(request.Payload), ceiling)
	}
	if metadata[0].Timestamp != wantTimestamp {
		t.Fatalf("metadata timestamp = %d, want %d", metadata[0].Timestamp, wantTimestamp)
	}
	if _, err := reader.Load(f.ctx, f.genesis); err != nil {
		t.Fatalf("delta scan rejected normalized event: %v", err)
	}
	if _, err := Verify(f.ctx, f.store, f.genesis); err != nil {
		t.Fatalf("cold scan rejected normalized event: %v", err)
	}
}

func TestChainParentGuardsPinGenesisAndEventsIndependently(t *testing.T) {
	t.Parallel()
	if err := validateChainParents(0, nil, ""); err != nil {
		t.Fatalf("parentless genesis rejected: %v", err)
	}
	if err := validateChainParents(0, []string{"ancestor"}, ""); err == nil || !strings.Contains(err.Error(), "genesis has a parent") {
		t.Fatalf("parented genesis error = %v", err)
	}
	if err := validateChainParents(1, []string{"prior"}, "prior"); err != nil {
		t.Fatalf("single-parent event rejected: %v", err)
	}
	if err := validateChainParents(1, []string{"prior", "other"}, "prior"); err == nil || !strings.Contains(err.Error(), "single-parent") {
		t.Fatalf("merge event error = %v", err)
	}
	if err := validateChainParents(1, []string{"other"}, "prior"); err == nil || !strings.Contains(err.Error(), "single-parent") {
		t.Fatalf("wrong-parent event error = %v", err)
	}
}

func TestVerifierRejectsSequencerSignedEventWithInvalidActorSignature(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	request := f.request(t, actor(t), "invalid-actor-signature", []byte("payload"), nil)
	decoded := mustVerifyIntent(t, request.Signed)
	tree, err := f.store.WritePayloadTree(f.ctx, request.Payload, request.Attachments)
	if err != nil {
		t.Fatal(err)
	}
	request.Signed.Signature[0] ^= 0xff
	commit, err := f.store.SignedCommit(f.ctx, tree, f.genesis, intent.Envelope(request.Signed, decoded.RestsOn), f.signingKey, gitstore.CommitIdentity{
		AuthorName: "hostile", AuthorEmail: "hostile@example.invalid", CommitterName: "gitseq sequencer", CommitterEmail: "sequencer@gitseq.invalid",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpdateRef(f.ctx, Ref(f.genesis), commit, f.genesis); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(f.ctx, f.store, f.genesis); err == nil || !strings.Contains(err.Error(), "invalid actor signature") {
		t.Fatalf("invalid actor signature error = %v", err)
	}
}

func TestVerifierRejectsCommitTreeDifferentFromSignedPayloadTree(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	request := f.request(t, actor(t), "tree-substitution", []byte("signed"), nil)
	decoded := mustVerifyIntent(t, request.Signed)
	substitutedTree, err := f.store.WritePayloadTree(f.ctx, []byte("substituted"), nil)
	if err != nil {
		t.Fatal(err)
	}
	commit, err := f.store.SignedCommit(f.ctx, substitutedTree, f.genesis, intent.Envelope(request.Signed, decoded.RestsOn), f.signingKey, gitstore.CommitIdentity{
		AuthorName: "hostile", AuthorEmail: "hostile@example.invalid", CommitterName: "gitseq sequencer", CommitterEmail: "sequencer@gitseq.invalid",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpdateRef(f.ctx, Ref(f.genesis), commit, f.genesis); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(f.ctx, f.store, f.genesis); err == nil || !strings.Contains(err.Error(), "commit tree differs from signed intent") {
		t.Fatalf("substituted tree error = %v", err)
	}
}

func TestVerifierRejectsExtraPayloadTreeEntry(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	eventOID, err := f.store.WriteBlob(f.ctx, []byte("event"))
	if err != nil {
		t.Fatal(err)
	}
	extraOID, err := f.store.WriteBlob(f.ctx, []byte("extra"))
	if err != nil {
		t.Fatal(err)
	}
	tree := rawTree(t, f.store, "100644 blob "+eventOID+"\tevent\n100644 blob "+extraOID+"\textra\n")
	signed, err := intent.Sign(intent.Intent{Version: intent.Version, Target: "git:" + f.format + ":" + f.genesis, Schema: "shape.v0", PayloadTree: "git:" + f.format + ":" + tree, IdempotencyNS: "shape", IdempotencyKey: "extra-entry"}, private)
	if err != nil {
		t.Fatal(err)
	}
	commit, err := f.store.SignedCommit(f.ctx, tree, f.genesis, intent.Envelope(signed, nil), f.signingKey, gitstore.CommitIdentity{AuthorName: "hostile", AuthorEmail: "hostile@example.invalid", CommitterName: "gitseq sequencer", CommitterEmail: "sequencer@gitseq.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpdateRef(f.ctx, Ref(f.genesis), commit, f.genesis); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(f.ctx, f.store, f.genesis); err == nil || !strings.Contains(err.Error(), "payload shape") {
		t.Fatalf("extra payload entry error = %v", err)
	}
}

func TestVerifierRejectsInvalidAttachmentName(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	eventOID, err := f.store.WriteBlob(f.ctx, []byte("event"))
	if err != nil {
		t.Fatal(err)
	}
	attachmentOID, err := f.store.WriteBlob(f.ctx, []byte("evidence"))
	if err != nil {
		t.Fatal(err)
	}
	attachments := rawTree(t, f.store, "100644 blob "+attachmentOID+"\t.hidden\n")
	tree := rawTree(t, f.store, "100644 blob "+eventOID+"\tevent\n040000 tree "+attachments+"\tattachments\n")
	signed, err := intent.Sign(intent.Intent{Version: intent.Version, Target: "git:" + f.format + ":" + f.genesis, Schema: "shape.v0", PayloadTree: "git:" + f.format + ":" + tree, IdempotencyNS: "shape", IdempotencyKey: "bad-attachment"}, private)
	if err != nil {
		t.Fatal(err)
	}
	commit, err := f.store.SignedCommit(f.ctx, tree, f.genesis, intent.Envelope(signed, nil), f.signingKey, gitstore.CommitIdentity{AuthorName: "hostile", AuthorEmail: "hostile@example.invalid", CommitterName: "gitseq sequencer", CommitterEmail: "sequencer@gitseq.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpdateRef(f.ctx, Ref(f.genesis), commit, f.genesis); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(f.ctx, f.store, f.genesis); err == nil || !strings.Contains(err.Error(), "invalid payload path") {
		t.Fatalf("invalid attachment name error = %v", err)
	}
}

func TestVerifierRejectsExternallySequencedDedupConflict(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	first, err := Submit(f.ctx, f.store, f.request(t, private, "conflict", []byte("first"), nil), Options{SigningKey: f.signingKey})
	if err != nil {
		t.Fatal(err)
	}
	appendExternalCommit(t, f, f.request(t, private, "conflict", []byte("second"), nil), first.Head, f.signingKey)
	if _, err := Verify(f.ctx, f.store, f.genesis); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("external dedup conflict error = %v", err)
	}
}

func TestVerifierRejectsSequencerSignedMergeEvent(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	first, err := Submit(f.ctx, f.store, f.request(t, private, "first-parent", []byte("first"), nil), Options{SigningKey: f.signingKey})
	if err != nil {
		t.Fatal(err)
	}
	request := f.request(t, private, "merge-event", []byte("merge"), nil)
	decoded := mustVerifyIntent(t, request.Signed)
	tree, err := f.store.WritePayloadTree(f.ctx, request.Payload, request.Attachments)
	if err != nil {
		t.Fatal(err)
	}
	commit := signedCommitWithParents(t, f, tree, []string{first.Head, f.genesis}, intent.Envelope(request.Signed, decoded.RestsOn), f.signingKey)
	if err := f.store.UpdateRef(f.ctx, Ref(f.genesis), commit, first.Head); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(f.ctx, f.store, f.genesis); err == nil || !strings.Contains(err.Error(), "single-parent chained") {
		t.Fatalf("merge event error = %v", err)
	}
}

func TestLoadReturnsVerifiedPayloadAndAttachments(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	request := f.request(t, private, "load", []byte("payload"), nil)
	request.Attachments = map[string][]byte{"evidence.json": []byte(`{"signed":true}`)}
	tree, err := f.scratch.WritePayloadTree(f.ctx, request.Payload, request.Attachments)
	if err != nil {
		t.Fatal(err)
	}
	decoded := mustVerifyIntent(t, request.Signed)
	request.Signed, err = intent.Sign(intent.Intent{
		Version: decoded.Version, Target: decoded.Target, Schema: decoded.Schema,
		PayloadTree:   "git:" + f.format + ":" + tree,
		IdempotencyNS: decoded.IdempotencyNS, IdempotencyKey: decoded.IdempotencyKey,
	}, private)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Submit(f.ctx, f.store, request, Options{SigningKey: f.signingKey}); err != nil {
		t.Fatal(err)
	}
	events, verification, err := Load(f.ctx, f.store, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	if verification.Events != 1 || len(events) != 1 || string(events[0].Payload) != "payload" || string(events[0].Attachments["evidence.json"]) != `{"signed":true}` {
		t.Fatalf("unexpected load: verification=%+v events=%+v", verification, events)
	}
}

func TestReaderLoadsColdThenVerifiesDescendantDeltas(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	submitter := NewSubmitter(f.store, Options{SigningKey: f.signingKey})
	reader := NewReader(f.store)

	cold, err := reader.Load(f.ctx, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	if !cold.Full || len(cold.Events) != 0 || cold.Verification.Events != 0 || reader.fullScans != 1 {
		t.Fatalf("cold load = %+v, scans = %d", cold, reader.fullScans)
	}
	exact, err := reader.Load(f.ctx, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	if exact.Full || len(exact.Events) != 0 || exact.BaseHead != cold.Verification.Head || reader.cacheHits != 1 {
		t.Fatalf("exact load = %+v, hits = %d", exact, reader.cacheHits)
	}

	total := 0
	for _, count := range []int{1, 10, 100} {
		base := reader.head
		for index := 0; index < count; index++ {
			key := "delta-" + strconv.Itoa(total+index)
			if _, err := submitter.Submit(f.ctx, f.request(t, private, key, []byte(key), nil)); err != nil {
				t.Fatal(err)
			}
		}
		total += count
		loaded, err := reader.Load(f.ctx, f.genesis)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.Full || loaded.BaseHead != base || len(loaded.Events) != count || loaded.Verification.Events != total {
			t.Fatalf("delta %d load = %+v", count, loaded)
		}
		for index, event := range loaded.Events {
			want := "delta-" + strconv.Itoa(total-count+index)
			if string(event.Payload) != want {
				t.Fatalf("delta %d event %d payload = %q, want %q", count, index, event.Payload, want)
			}
		}
	}
	if reader.fullScans != 1 || reader.deltaScans != 3 {
		t.Fatalf("reader scans = full %d delta %d, want 1 and 3", reader.fullScans, reader.deltaScans)
	}
	if reader.log.Verification.Events != total || reader.log.Verification.Head != reader.head {
		t.Fatalf("reader verification = %+v, head = %s", reader.log.Verification, reader.head)
	}
}

func TestReaderRestartsFromSignedCheckpointAndAuditsDescendantDelta(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	for index := 0; index < 3; index++ {
		key := "checkpoint-" + strconv.Itoa(index)
		if _, err := Submit(f.ctx, f.store, f.request(t, private, key, []byte(key), nil), Options{SigningKey: f.signingKey}); err != nil {
			t.Fatal(err)
		}
	}
	options := CheckpointOptions{Enabled: true, SigningKey: f.signingKey}
	writer := NewReader(f.store, options)
	full, err := writer.Load(f.ctx, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	if full.Checkpoint || writer.fullScans != 1 || writer.checkpointWrites != 1 {
		t.Fatalf("initial load = %+v, scans=%d writes=%d", full, writer.fullScans, writer.checkpointWrites)
	}
	checkpointHead, err := f.store.Head(f.ctx, CheckpointRef(f.genesis))
	if err != nil {
		t.Fatal(err)
	}

	restarted := NewReader(f.store, options)
	cached, err := restarted.Load(f.ctx, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	if !cached.Full || !cached.Checkpoint || restarted.fullScans != 0 || restarted.checkpointLoads != 1 || len(cached.Events) != 3 {
		t.Fatalf("checkpoint load = %+v, full=%d loads=%d", cached, restarted.fullScans, restarted.checkpointLoads)
	}
	if !reflect.DeepEqual(cached.Events, full.Events) || cached.Verification != full.Verification {
		t.Fatalf("checkpoint fold differs from cold fold:\ncheckpoint=%+v\ncold=%+v", cached, full)
	}
	if afterRestart, err := f.store.Head(f.ctx, CheckpointRef(f.genesis)); err != nil || afterRestart != checkpointHead {
		t.Fatalf("exact checkpoint restart rewrote ref: before=%s after=%s err=%v", checkpointHead, afterRestart, err)
	}
	for index, event := range cached.Events {
		if got, want := string(event.Payload), "checkpoint-"+strconv.Itoa(index); got != want {
			t.Fatalf("event %d payload = %q, want %q", index, got, want)
		}
		if event.Timestamp == 0 || event.Timestamp != full.Events[index].Timestamp {
			t.Fatalf("event %d timestamp = %d, want %d", index, event.Timestamp, full.Events[index].Timestamp)
		}
	}

	result, err := Submit(f.ctx, f.store, f.request(t, private, "after-checkpoint", []byte("after-checkpoint"), nil), Options{SigningKey: f.signingKey})
	if err != nil {
		t.Fatal(err)
	}
	deltaRestart := NewReader(f.store, options)
	advanced, err := deltaRestart.Load(f.ctx, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	if !advanced.Checkpoint || advanced.Verification.Head != result.Head || len(advanced.Events) != 4 || string(advanced.Events[3].Payload) != "after-checkpoint" {
		t.Fatalf("checkpoint delta load = %+v", advanced)
	}
}

func TestReaderPersistsSignedCheckpointPointerAcrossRestart(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	if _, err := Submit(f.ctx, f.store, f.request(t, private, "persisted", []byte("persisted"), nil), Options{SigningKey: f.signingKey}); err != nil {
		t.Fatal(err)
	}
	pointer := &testCheckpointPointer{}
	options := CheckpointOptions{Enabled: true, SigningKey: f.signingKey, Pointer: pointer}
	written, err := NewReader(f.store, options).Load(f.ctx, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	commit := pointer.commit
	if err := f.store.UpdateRef(f.ctx, CheckpointRef(f.genesis), f.genesis, commit); err != nil {
		t.Fatal(err)
	}
	restarted := NewReader(f.store, CheckpointOptions{Enabled: true, Pointer: pointer})
	loaded, err := restarted.Load(f.ctx, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Checkpoint || restarted.fullScans != 0 || !reflect.DeepEqual(loaded.Events, written.Events) || loaded.Verification != written.Verification {
		t.Fatalf("persisted checkpoint restart differs: written=%+v loaded=%+v full=%d", written, loaded, restarted.fullScans)
	}
	if repaired, err := f.store.Head(f.ctx, CheckpointRef(f.genesis)); err != nil || repaired != commit {
		t.Fatalf("local pointer did not repair reachability ref: head=%s want=%s err=%v", repaired, commit, err)
	}
	if pointer.writes != 1 {
		t.Fatalf("unchanged pointer writes = %d, want 1 initial write only", pointer.writes)
	}
}

func TestCheckpointPointerFailureStillPublishesReachableRef(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Submit(f.ctx, f.store, f.request(t, private, "reachable", []byte("reachable"), nil), Options{SigningKey: f.signingKey}); err != nil {
		t.Fatal(err)
	}
	pointer := &testCheckpointPointer{storeErr: errors.New("local disk unavailable")}
	options := CheckpointOptions{Enabled: true, SigningKey: f.signingKey, Pointer: pointer}
	loaded, err := NewReader(f.store, options).Load(f.ctx, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	checkpointCommit, err := f.store.Head(f.ctx, CheckpointRef(f.genesis))
	if err != nil || checkpointCommit == f.genesis {
		t.Fatalf("checkpoint ref was not refreshed: commit=%s err=%v", checkpointCommit, err)
	}
	command := exec.Command("git", "--git-dir", f.store.Repo, "gc", "--prune=now")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git gc: %v: %s", err, output)
	}
	if message, err := f.store.CommitMessage(f.ctx, checkpointCommit); err != nil || strings.TrimSpace(message) != checkpointMarker {
		t.Fatalf("checkpoint object was not retained by its ref: message=%q err=%v", message, err)
	}
	restarted, err := NewReader(f.store, CheckpointOptions{Enabled: true}).Load(f.ctx, f.genesis)
	if err != nil || !restarted.Checkpoint || !reflect.DeepEqual(restarted.Events, loaded.Events) {
		t.Fatalf("ref recovery after pointer failure = %+v, err=%v", restarted, err)
	}
}

func TestReaderRejectsTamperedLocalCheckpointPointerAndUsesSignedRef(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	if _, err := Submit(f.ctx, f.store, f.request(t, private, "pointer-fallback", []byte("pointer-fallback"), nil), Options{SigningKey: f.signingKey}); err != nil {
		t.Fatal(err)
	}
	pointer := &testCheckpointPointer{}
	options := CheckpointOptions{Enabled: true, SigningKey: f.signingKey, Pointer: pointer}
	if _, err := NewReader(f.store, options).Load(f.ctx, f.genesis); err != nil {
		t.Fatal(err)
	}
	pointer.commit = f.genesis
	restarted := NewReader(f.store, CheckpointOptions{Enabled: true, Pointer: pointer})
	loaded, err := restarted.Load(f.ctx, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Checkpoint || restarted.fullScans != 0 {
		t.Fatalf("signed ref fallback = %+v, full scans=%d", loaded, restarted.fullScans)
	}
	if pointer.commit == f.genesis {
		t.Fatalf("local pointer was not repaired from signed ref: commit=%s", pointer.commit)
	}
	pointer.commit = "--help"
	malicious := NewReader(f.store, CheckpointOptions{Enabled: true, Pointer: pointer})
	if loaded, err := malicious.Load(f.ctx, f.genesis); err != nil || !loaded.Checkpoint || malicious.fullScans != 0 {
		t.Fatalf("option-shaped pointer did not safely fall back: loaded=%+v err=%v full=%d", loaded, err, malicious.fullScans)
	}
}

func TestReaderCheckpointContinuationCarriesRotatedSequencerKey(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	if _, err := Submit(f.ctx, f.store, f.request(t, private, "before-rotation", []byte("before-rotation"), nil), Options{SigningKey: f.signingKey}); err != nil {
		t.Fatal(err)
	}
	checkpoint := CheckpointOptions{Enabled: true, SigningKey: f.signingKey}
	if _, err := NewReader(f.store, checkpoint).Load(f.ctx, f.genesis); err != nil {
		t.Fatal(err)
	}

	nextKey := filepath.Join(t.TempDir(), "next-sequencer")
	nextPublic, err := gitstore.GenerateSSHKey(f.ctx, nextKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Rotate(f.ctx, f.store, f.genesis, nextPublic, Options{SigningKey: f.signingKey}); err != nil {
		t.Fatal(err)
	}
	afterRotation, err := Submit(f.ctx, f.store, f.request(t, private, "after-rotation", []byte("after-rotation"), nil), Options{SigningKey: nextKey})
	if err != nil {
		t.Fatal(err)
	}

	restarted := NewReader(f.store, CheckpointOptions{Enabled: true})
	loaded, err := restarted.Load(f.ctx, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Checkpoint || loaded.Verification.Head != afterRotation.Head || loaded.Verification.Depth != 3 || loaded.Verification.Events != 2 || len(loaded.Events) != 2 {
		t.Fatalf("checkpoint continuation across rotation = %+v", loaded)
	}

	final, err := Submit(f.ctx, f.store, f.request(t, private, "resident-after-rotation", []byte("resident-after-rotation"), nil), Options{SigningKey: nextKey})
	if err != nil {
		t.Fatal(err)
	}
	delta, err := restarted.Load(f.ctx, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	if delta.Full || delta.BaseHead != afterRotation.Head || delta.Verification.Head != final.Head || len(delta.Events) != 1 || string(delta.Events[0].Payload) != "resident-after-rotation" {
		t.Fatalf("resident continuation under rotated key = %+v", delta)
	}
}

func TestReaderRestartsFromCheckpointWrittenAfterRotation(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	if _, err := Submit(f.ctx, f.store, f.request(t, private, "before-rotation-checkpoint", []byte("before"), nil), Options{SigningKey: f.signingKey}); err != nil {
		t.Fatal(err)
	}
	nextKey := filepath.Join(t.TempDir(), "next-sequencer")
	nextPublic, err := gitstore.GenerateSSHKey(f.ctx, nextKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Rotate(f.ctx, f.store, f.genesis, nextPublic, Options{SigningKey: f.signingKey}); err != nil {
		t.Fatal(err)
	}
	afterRotation, err := Submit(f.ctx, f.store, f.request(t, private, "after-rotation-checkpoint", []byte("after"), nil), Options{SigningKey: nextKey})
	if err != nil {
		t.Fatal(err)
	}

	checkpoint := CheckpointOptions{Enabled: true, SigningKey: nextKey}
	writer := NewReader(f.store, checkpoint)
	written, err := writer.Load(f.ctx, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	if written.Checkpoint || writer.fullScans != 1 || writer.checkpointWrites != 1 || written.Verification.Depth != 3 || written.Verification.Events != 2 {
		t.Fatalf("post-rotation checkpoint write = %+v scans=%d writes=%d", written, writer.fullScans, writer.checkpointWrites)
	}
	checkpointCommit := mustHead(t, f.store, CheckpointRef(f.genesis))
	if err := f.store.VerifySSHCommit(f.ctx, checkpointCommit, "sequencer", nextPublic); err != nil {
		t.Fatalf("checkpoint not signed by current key: %v", err)
	}
	if err := f.store.VerifySSHCommit(f.ctx, checkpointCommit, "sequencer", f.publicKey); err == nil {
		t.Fatal("post-rotation checkpoint verified under retired key")
	}
	checkpointData, err := f.store.ReadFileLimit(f.ctx, checkpointCommit, checkpointFile, maxCheckpointBytes)
	if err != nil {
		t.Fatal(err)
	}

	restarted := NewReader(f.store, CheckpointOptions{Enabled: true})
	cached, err := restarted.Load(f.ctx, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	if !cached.Checkpoint || restarted.fullScans != 0 || restarted.checkpointLoads != 1 || cached.Verification.Head != afterRotation.Head || cached.Verification.Depth != 3 || cached.Verification.Events != 2 || len(cached.Events) != 2 {
		t.Fatalf("post-rotation checkpoint restart = %+v scans=%d loads=%d", cached, restarted.fullScans, restarted.checkpointLoads)
	}

	final, err := Submit(f.ctx, f.store, f.request(t, private, "resident-after-post-rotation-checkpoint", []byte("resident"), nil), Options{SigningKey: nextKey})
	if err != nil {
		t.Fatal(err)
	}
	delta, err := restarted.Load(f.ctx, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	if delta.Full || delta.BaseHead != afterRotation.Head || delta.Verification.Head != final.Head || len(delta.Events) != 1 || string(delta.Events[0].Payload) != "resident" {
		t.Fatalf("resident delta after post-rotation checkpoint = %+v", delta)
	}

	// A checkpoint for the rotated prefix is authenticated only by the key
	// current at that frontier. Re-signing the same trusted bytes with the
	// retired key must be a cache miss, followed by an ordinary full audit.
	publishCheckpointBytes(t, f, checkpointData, f.signingKey, "", checkpointMarker)
	fallback := NewReader(f.store, CheckpointOptions{Enabled: true})
	loaded, err := fallback.Load(f.ctx, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Checkpoint || fallback.fullScans != 1 || fallback.checkpointFallbacks != 1 || loaded.Verification.Head != final.Head || loaded.Verification.Events != 3 {
		t.Fatalf("retired-key checkpoint fallback = %+v scans=%d fallbacks=%d", loaded, fallback.fullScans, fallback.checkpointFallbacks)
	}
}

func TestCheckpointCannotTrustSuccessorFromUnverifiedRotation(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	first, err := Submit(f.ctx, f.store, f.request(t, private, "before-forged-rotation-checkpoint", []byte("before"), nil), Options{SigningKey: f.signingKey})
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := CheckpointOptions{Enabled: true, SigningKey: f.signingKey}
	if _, err := NewReader(f.store, checkpoint).Load(f.ctx, f.genesis); err != nil {
		t.Fatal(err)
	}
	checkpointCommit := mustHead(t, f.store, CheckpointRef(f.genesis))
	data, err := f.store.ReadFileLimit(f.ctx, checkpointCommit, checkpointFile, maxCheckpointBytes)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := decodeCheckpoint(data)
	if err != nil {
		t.Fatal(err)
	}

	successorKey := filepath.Join(t.TempDir(), "successor")
	successorPublic, err := gitstore.GenerateSSHKey(f.ctx, successorKey)
	if err != nil {
		t.Fatal(err)
	}
	attackerKey := filepath.Join(t.TempDir(), "attacker")
	if _, err := gitstore.GenerateSSHKey(f.ctx, attackerKey); err != nil {
		t.Fatal(err)
	}
	forgedRotation := appendExternalRotation(t, f, first.Head, successorPublic, attackerKey)
	stored.Head = forgedRotation
	stored.Depth = 2
	data, err = marshalCheckpoint(stored, maxCheckpointBytes)
	if err != nil {
		t.Fatal(err)
	}
	publishCheckpointBytes(t, f, data, successorKey, "", checkpointMarker)

	if _, _, err := loadCheckpoint(f.ctx, f.store, f.genesis, forgedRotation, CheckpointOptions{Enabled: true}); err == nil || !errors.Is(err, ErrNoUsableCheckpoint) || !strings.Contains(err.Error(), forgedRotation+" sequencer signature") {
		t.Fatalf("checkpoint trusted successor from unverified rotation: %v", err)
	}
}

func TestCheckpointWriterRejectsRetiredSigningKeyAfterRotation(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	if _, err := Submit(f.ctx, f.store, f.request(t, private, "before-retired-checkpoint-writer", []byte("before"), nil), Options{SigningKey: f.signingKey}); err != nil {
		t.Fatal(err)
	}
	nextKey := filepath.Join(t.TempDir(), "next-sequencer")
	nextPublic, err := gitstore.GenerateSSHKey(f.ctx, nextKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Rotate(f.ctx, f.store, f.genesis, nextPublic, Options{SigningKey: f.signingKey}); err != nil {
		t.Fatal(err)
	}
	if _, err := Submit(f.ctx, f.store, f.request(t, private, "after-retired-checkpoint-writer", []byte("after"), nil), Options{SigningKey: nextKey}); err != nil {
		t.Fatal(err)
	}
	head := mustHead(t, f.store, Ref(f.genesis))
	log, err := scanHead(f.ctx, f.store, f.genesis, head, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeCheckpoint(f.ctx, f.store, log, CheckpointOptions{Enabled: true, SigningKey: f.signingKey}); err == nil || !strings.Contains(err.Error(), "checkpoint signature does not match current sequencer key") {
		t.Fatalf("retired checkpoint signer error = %v", err)
	}
	if _, err := f.store.Head(f.ctx, CheckpointRef(f.genesis)); err == nil {
		t.Fatal("retired signer published an unusable checkpoint")
	}
	if err := writeCheckpoint(f.ctx, f.store, log, CheckpointOptions{Enabled: true, SigningKey: nextKey}); err != nil {
		t.Fatal(err)
	}
}

func TestReaderCheckpointMismatchCorruptionAndNonDescendantFallBack(t *testing.T) {
	t.Parallel()
	t.Run("application profile is not kernel identity", func(t *testing.T) {
		f := newFixture(t, "sha1")
		private := actor(t)
		if _, err := Submit(f.ctx, f.store, f.request(t, private, "one", []byte("one"), nil), Options{SigningKey: f.signingKey}); err != nil {
			t.Fatal(err)
		}
		if _, err := NewReader(f.store, CheckpointOptions{Enabled: true, SigningKey: f.signingKey}).Load(f.ctx, f.genesis); err != nil {
			t.Fatal(err)
		}
		reader := NewReader(f.store, CheckpointOptions{Enabled: true})
		loaded, err := reader.Load(f.ctx, f.genesis)
		if err != nil {
			t.Fatal(err)
		}
		if !loaded.Checkpoint || reader.fullScans != 0 || reader.checkpointFallbacks != 0 {
			t.Fatalf("profile-independent checkpoint = %+v, scans=%d fallbacks=%d", loaded, reader.fullScans, reader.checkpointFallbacks)
		}
	})

	t.Run("legacy profile is ignored after authentication", func(t *testing.T) {
		f, _, _, stored := checkpointState(t, 1)
		stored.Schema = legacyCheckpointSchema
		stored.Profile = "historical-fold@1"
		data, err := json.Marshal(stored)
		if err != nil {
			t.Fatal(err)
		}
		publishCheckpointBytes(t, f, data, f.signingKey, "", legacyCheckpointMarker)
		reader := NewReader(f.store, CheckpointOptions{Enabled: true})
		loaded, err := reader.Load(f.ctx, f.genesis)
		if err != nil {
			t.Fatal(err)
		}
		if !loaded.Checkpoint || reader.fullScans != 0 || reader.checkpointFallbacks != 0 {
			t.Fatalf("legacy checkpoint did not bridge across profile: %+v scans=%d fallbacks=%d", loaded, reader.fullScans, reader.checkpointFallbacks)
		}
	})

	t.Run("profiled compact checkpoint is ignored after authentication", func(t *testing.T) {
		f, _, _, stored := checkpointState(t, 1)
		stored.Schema = profiledCompactCheckpointSchema
		stored.Profile = "historical-fold@2"
		stored.EventCount = len(stored.Events)
		data, err := marshalCheckpoint(stored, maxCheckpointBytes)
		if err != nil {
			t.Fatal(err)
		}
		publishCheckpointBytes(t, f, data, f.signingKey, "", profiledCompactCheckpointMarker)
		reader := NewReader(f.store, CheckpointOptions{Enabled: true})
		loaded, err := reader.Load(f.ctx, f.genesis)
		if err != nil {
			t.Fatal(err)
		}
		if !loaded.Checkpoint || reader.fullScans != 0 || reader.checkpointFallbacks != 0 {
			t.Fatalf("profiled compact checkpoint did not bridge across profile: %+v scans=%d fallbacks=%d", loaded, reader.fullScans, reader.checkpointFallbacks)
		}
	})

	t.Run("profiled compact checkpoint still requires its historical profile field", func(t *testing.T) {
		f, _, _, stored := checkpointState(t, 1)
		stored.Schema = profiledCompactCheckpointSchema
		stored.Profile = ""
		stored.EventCount = len(stored.Events)
		data, err := marshalCheckpoint(stored, maxCheckpointBytes)
		if err != nil {
			t.Fatal(err)
		}
		publishCheckpointBytes(t, f, data, f.signingKey, "", profiledCompactCheckpointMarker)
		requireCheckpointFallback(t, f)
	})

	t.Run("legacy checkpoint still requires its historical profile field", func(t *testing.T) {
		f, _, _, stored := checkpointState(t, 1)
		stored.Schema = legacyCheckpointSchema
		stored.Profile = ""
		data, err := json.Marshal(stored)
		if err != nil {
			t.Fatal(err)
		}
		publishCheckpointBytes(t, f, data, f.signingKey, "", legacyCheckpointMarker)
		requireCheckpointFallback(t, f)
	})

	t.Run("payload", func(t *testing.T) {
		f := newFixture(t, "sha1")
		private := actor(t)
		result, err := Submit(f.ctx, f.store, f.request(t, private, "one", []byte("one"), nil), Options{SigningKey: f.signingKey})
		if err != nil {
			t.Fatal(err)
		}
		log, err := scanHead(f.ctx, f.store, f.genesis, result.Head, true, nil)
		if err != nil {
			t.Fatal(err)
		}
		log.Events[0].Payload = []byte("substituted")
		if err := writeCheckpoint(f.ctx, f.store, log, CheckpointOptions{Enabled: true, SigningKey: f.signingKey}); err != nil {
			t.Fatal(err)
		}
		reader := NewReader(f.store, CheckpointOptions{Enabled: true})
		loaded, err := reader.Load(f.ctx, f.genesis)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.Checkpoint || string(loaded.Events[0].Payload) != "one" || reader.fullScans != 1 {
			t.Fatalf("substituted checkpoint did not fall back: %+v", loaded)
		}
	})

	t.Run("non-descendant", func(t *testing.T) {
		f := newFixture(t, "sha1")
		private := actor(t)
		first, err := Submit(f.ctx, f.store, f.request(t, private, "first", []byte("first"), nil), Options{SigningKey: f.signingKey})
		if err != nil {
			t.Fatal(err)
		}
		options := CheckpointOptions{Enabled: true, SigningKey: f.signingKey}
		if _, err := NewReader(f.store, options).Load(f.ctx, f.genesis); err != nil {
			t.Fatal(err)
		}
		if err := f.store.UpdateRef(f.ctx, Ref(f.genesis), f.genesis, first.Head); err != nil {
			t.Fatal(err)
		}
		alternate, err := Submit(f.ctx, f.store, f.request(t, private, "alternate", []byte("alternate"), nil), Options{SigningKey: f.signingKey})
		if err != nil {
			t.Fatal(err)
		}
		reader := NewReader(f.store, CheckpointOptions{Enabled: true})
		loaded, err := reader.Load(f.ctx, f.genesis)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.Checkpoint || loaded.Verification.Head != alternate.Head || string(loaded.Events[0].Payload) != "alternate" || reader.fullScans != 1 {
			t.Fatalf("non-descendant checkpoint did not fall back: %+v", loaded)
		}
	})
}

func TestCheckpointEnvelopeGuardsFallBackToFullAudit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		publish func(*testing.T, fixtureState, checkpoint)
	}{
		{name: "sequencer signature", publish: func(t *testing.T, f fixtureState, stored checkpoint) {
			wrongKey := filepath.Join(t.TempDir(), "wrong-checkpoint-signer")
			if _, err := gitstore.GenerateSSHKey(f.ctx, wrongKey); err != nil {
				t.Fatal(err)
			}
			data, _ := json.Marshal(stored)
			publishCheckpointBytes(t, f, data, wrongKey, "", legacyCheckpointMarker)
		}},
		{name: "parentless", publish: func(t *testing.T, f fixtureState, stored checkpoint) {
			data, _ := json.Marshal(stored)
			publishCheckpointBytes(t, f, data, f.signingKey, f.genesis, legacyCheckpointMarker)
		}},
		{name: "marker", publish: func(t *testing.T, f fixtureState, stored checkpoint) {
			data, _ := json.Marshal(stored)
			publishCheckpointBytes(t, f, data, f.signingKey, "", "not-a-checkpoint")
		}},
		{name: "tree shape", publish: func(t *testing.T, f fixtureState, _ checkpoint) {
			tree, err := f.store.EmptyTree(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			publishCheckpointTree(t, f, tree, f.signingKey, "", legacyCheckpointMarker)
		}},
		{name: "unknown field", publish: func(t *testing.T, f fixtureState, stored checkpoint) {
			data, _ := json.Marshal(stored)
			data = append(data[:len(data)-1], []byte(`,"authority":"forged"}`)...)
			publishCheckpointBytes(t, f, data, f.signingKey, "", legacyCheckpointMarker)
		}},
		{name: "noncanonical json", publish: func(t *testing.T, f fixtureState, stored checkpoint) {
			data, _ := json.Marshal(stored)
			publishCheckpointBytes(t, f, append([]byte("\n"), data...), f.signingKey, "", legacyCheckpointMarker)
		}},
		{name: "trailing json", publish: func(t *testing.T, f fixtureState, stored checkpoint) {
			data, _ := json.Marshal(stored)
			publishCheckpointBytes(t, f, append(data, []byte(`{}`)...), f.signingKey, "", legacyCheckpointMarker)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f, _, _, stored := checkpointState(t, 1)
			test.publish(t, f, stored)
			requireCheckpointFallback(t, f)
		})
	}
}

func TestCheckpointIdentityAndCachedEventGuardsFallBackToFullAudit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*testing.T, fixtureState, ed25519.PrivateKey, *checkpoint)
	}{
		{name: "schema", mutate: func(_ *testing.T, _ fixtureState, _ ed25519.PrivateKey, stored *checkpoint) {
			stored.Schema = "gitseq-checkpoint@99"
		}},
		{name: "object format", mutate: func(_ *testing.T, _ fixtureState, _ ed25519.PrivateKey, stored *checkpoint) {
			stored.ObjectFormat = "sha256"
		}},
		{name: "genesis", mutate: func(_ *testing.T, _ fixtureState, _ ed25519.PrivateKey, stored *checkpoint) {
			stored.Genesis = strings.Repeat("0", 40)
		}},
		{name: "depth count", mutate: func(_ *testing.T, _ fixtureState, _ ed25519.PrivateKey, stored *checkpoint) { stored.Depth-- }},
		{name: "head is final event", mutate: func(_ *testing.T, f fixtureState, _ ed25519.PrivateKey, stored *checkpoint) { stored.Head = f.genesis }},
		{name: "dropped event", mutate: func(_ *testing.T, _ fixtureState, _ ed25519.PrivateKey, stored *checkpoint) {
			stored.Events = append([]checkpointEvent(nil), stored.Events[1:]...)
			stored.Depth = len(stored.Events)
		}},
		{name: "reordered events", mutate: func(_ *testing.T, _ fixtureState, _ ed25519.PrivateKey, stored *checkpoint) {
			stored.Events[0], stored.Events[1] = stored.Events[1], stored.Events[0]
		}},
		{name: "swapped commit ids", mutate: func(_ *testing.T, _ fixtureState, _ ed25519.PrivateKey, stored *checkpoint) {
			stored.Events[0].Commit, stored.Events[1].Commit = stored.Events[1].Commit, stored.Events[0].Commit
		}},
		{name: "swapped cached event contents", mutate: func(_ *testing.T, _ fixtureState, _ ed25519.PrivateKey, stored *checkpoint) {
			firstCommit, secondCommit := stored.Events[0].Commit, stored.Events[1].Commit
			stored.Events[0], stored.Events[1] = stored.Events[1], stored.Events[0]
			stored.Events[0].Commit, stored.Events[1].Commit = firstCommit, secondCommit
		}},
		{name: "fabricated commit id", mutate: func(_ *testing.T, _ fixtureState, _ ed25519.PrivateKey, stored *checkpoint) {
			stored.Events[0].Commit = strings.Repeat("a", 40)
		}},
		{name: "wrong-length commit id", mutate: func(_ *testing.T, _ fixtureState, _ ed25519.PrivateKey, stored *checkpoint) {
			stored.Events[0].Commit = strings.Repeat("a", 39)
		}},
		{name: "nonhex commit id", mutate: func(_ *testing.T, _ fixtureState, _ ed25519.PrivateKey, stored *checkpoint) {
			stored.Events[0].Commit = strings.Repeat("z", 40)
		}},
		{name: "duplicate commit id", mutate: func(_ *testing.T, _ fixtureState, _ ed25519.PrivateKey, stored *checkpoint) {
			stored.Events[1].Commit = stored.Events[0].Commit
		}},
		{name: "actor signature", mutate: func(_ *testing.T, _ fixtureState, _ ed25519.PrivateKey, stored *checkpoint) {
			stored.Events[0].Signed.Signature[0] ^= 0xff
		}},
		{name: "target", mutate: func(t *testing.T, _ fixtureState, private ed25519.PrivateKey, stored *checkpoint) {
			decoded := mustVerifyIntent(t, stored.Events[0].Signed)
			decoded.Target = "git:sha1:" + strings.Repeat("f", 40)
			stored.Events[0].Signed = mustSignIntent(t, decoded, private)
		}},
		{name: "payload ceiling", mutate: func(_ *testing.T, _ fixtureState, _ ed25519.PrivateKey, stored *checkpoint) {
			stored.Events[0].Payload = make([]byte, (1<<20)+1)
		}},
		{name: "payload tree", mutate: func(_ *testing.T, _ fixtureState, _ ed25519.PrivateKey, stored *checkpoint) {
			stored.Events[0].Payload = []byte("substituted")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f, private, _, original := checkpointState(t, 3)
			stored := cloneCheckpoint(t, original)
			test.mutate(t, f, private, &stored)
			publishStoredCheckpoint(t, f, stored)
			loaded := requireCheckpointFallback(t, f)
			if loaded.Verification.Events != 3 || len(loaded.Events) != 3 {
				t.Fatalf("fallback did not recover complete sequence: %+v", loaded)
			}
		})
	}

	t.Run("current compact profile", func(t *testing.T) {
		f, _, _, stored := checkpointState(t, 3)
		stored.Schema = checkpointSchema
		stored.Profile = "forged-fold@3"
		stored.EventCount = len(stored.Events)
		data, err := marshalCheckpoint(stored, maxCheckpointBytes)
		if err != nil {
			t.Fatal(err)
		}
		publishCheckpointBytes(t, f, data, f.signingKey, "", checkpointMarker)
		loaded := requireCheckpointFallback(t, f)
		if loaded.Verification.Events != 3 || len(loaded.Events) != 3 {
			t.Fatalf("fallback did not recover complete sequence: %+v", loaded)
		}
	})
}

func TestCheckpointMetadataRequiresOneExactParentAtEveryPosition(t *testing.T) {
	t.Parallel()
	f, _, _, stored := checkpointState(t, 3)
	sequence, err := f.store.RevListMetadata(f.ctx, stored.Head)
	if err != nil {
		t.Fatal(err)
	}
	sequence[2].Parents = append(sequence[2].Parents, strings.Repeat("f", 40))
	desc, err := Descriptor(f.ctx, f.store, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := validateCheckpoint(stored, desc, sequence); err == nil {
		t.Fatal("checkpoint accepted a merge in the cached sequence")
	}
}

func TestCheckpointSequencePositionGuards(t *testing.T) {
	t.Parallel()
	t.Run("named head", func(t *testing.T) {
		f, _, _, stored := checkpointState(t, 1)
		sequence, err := f.store.RevListMetadata(f.ctx, stored.Head)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateNamedSequence(stored.Head, sequence); err != nil {
			t.Fatalf("valid named sequence rejected: %v", err)
		}
		if err := validateNamedSequence(strings.Repeat("f", 40), sequence); err == nil {
			t.Fatal("sequence ending elsewhere accepted as the named head")
		}
	})

	t.Run("named genesis", func(t *testing.T) {
		f, _, _, stored := checkpointState(t, 1)
		sequence, err := f.store.RevListMetadata(f.ctx, stored.Head)
		if err != nil {
			t.Fatal(err)
		}
		desc, err := Descriptor(f.ctx, f.store, f.genesis)
		if err != nil {
			t.Fatal(err)
		}
		sequence[0].OID = strings.Repeat("f", 40)
		if _, err := validateCheckpoint(stored, desc, sequence); err == nil || !strings.Contains(err.Error(), "does not begin the named sequence") {
			t.Fatalf("checkpoint beginning outside the named sequence error = %v", err)
		}
	})

	t.Run("parentless genesis", func(t *testing.T) {
		f, _, _, stored := checkpointState(t, 1)
		sequence, err := f.store.RevListMetadata(f.ctx, stored.Head)
		if err != nil {
			t.Fatal(err)
		}
		desc, err := Descriptor(f.ctx, f.store, f.genesis)
		if err != nil {
			t.Fatal(err)
		}
		sequence[0].Parents = []string{strings.Repeat("f", 40)}
		if _, err := validateCheckpoint(stored, desc, sequence); err == nil || !strings.Contains(err.Error(), "genesis has a parent") {
			t.Fatalf("parented genesis error = %v", err)
		}
	})

	t.Run("empty frontier", func(t *testing.T) {
		f, _, _, stored := checkpointState(t, 0)
		sequence, err := f.store.RevListMetadata(f.ctx, stored.Head)
		if err != nil {
			t.Fatal(err)
		}
		desc, err := Descriptor(f.ctx, f.store, f.genesis)
		if err != nil {
			t.Fatal(err)
		}
		stored.Head = strings.Repeat("f", 40)
		if _, err := validateCheckpoint(stored, desc, sequence); err == nil || !strings.Contains(err.Error(), "empty checkpoint head is not genesis") {
			t.Fatalf("empty non-genesis frontier error = %v", err)
		}
	})
}

func TestCheckpointMetadataRejectsSelfConsistentDuplicateDedupKeys(t *testing.T) {
	t.Parallel()
	f, private, _, original := checkpointState(t, 2)
	originalSequence, err := f.store.RevListMetadata(f.ctx, original.Head)
	if err != nil {
		t.Fatal(err)
	}
	desc, err := Descriptor(f.ctx, f.store, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := validateCheckpoint(original, desc, originalSequence); err != nil {
		t.Fatalf("valid checkpoint rejected: %v", err)
	}

	t.Run("exact duplicate", func(t *testing.T) {
		stored := cloneCheckpoint(t, original)
		sequence := append([]gitstore.CommitMetadata(nil), originalSequence...)
		stored.Events[1].Signed = cloneSigned(stored.Events[0].Signed)
		stored.Events[1].Payload = bytes.Clone(stored.Events[0].Payload)
		stored.Events[1].Attachments = cloneByteMap(stored.Events[0].Attachments)
		decoded := mustVerifyIntent(t, stored.Events[1].Signed)
		sequence[2].Message = intent.Envelope(stored.Events[1].Signed, decoded.RestsOn)
		sequence[2].Tree = sequence[1].Tree
		if _, err := validateCheckpoint(stored, desc, sequence); err == nil || !strings.Contains(err.Error(), "duplicates idempotent event") {
			t.Fatalf("self-consistent duplicate checkpoint error = %v", err)
		}
	})

	t.Run("conflict", func(t *testing.T) {
		stored := cloneCheckpoint(t, original)
		sequence := append([]gitstore.CommitMetadata(nil), originalSequence...)
		first := mustVerifyIntent(t, stored.Events[0].Signed)
		second := mustVerifyIntent(t, stored.Events[1].Signed)
		second.IdempotencyNS = first.IdempotencyNS
		second.IdempotencyKey = first.IdempotencyKey
		stored.Events[1].Signed = mustSignIntent(t, second, private)
		sequence[2].Message = intent.Envelope(stored.Events[1].Signed, second.RestsOn)
		if _, err := validateCheckpoint(stored, desc, sequence); !errors.Is(err, ErrIdempotencyConflict) {
			t.Fatalf("self-consistent dedup conflict error = %v", err)
		}
	})
}

func TestCheckpointObjectIDValidationCoversBothFormats(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		format string
		length int
	}{{format: "sha1", length: 40}, {format: "sha256", length: 64}} {
		if err := validateObjectID(test.format, strings.Repeat("a", test.length)); err != nil {
			t.Fatalf("valid %s object ID rejected: %v", test.format, err)
		}
	}
	if err := validateObjectID("sha1", strings.Repeat("a", 39)); err == nil || !strings.Contains(err.Error(), "length") {
		t.Fatalf("wrong-length object ID error = %v", err)
	}
	if err := validateObjectID("sha1", strings.Repeat("z", 40)); err == nil || !strings.Contains(err.Error(), "hexadecimal") {
		t.Fatalf("nonhex object ID error = %v", err)
	}
}

func TestCheckpointDecoderRejectsTrailingJSONAtItsBoundary(t *testing.T) {
	t.Parallel()
	stored := checkpoint{Schema: legacyCheckpointSchema, Profile: "fold@1"}
	data, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeCheckpoint(data); err != nil {
		t.Fatalf("canonical checkpoint rejected: %v", err)
	}
	if _, err := decodeCheckpoint(append(data, []byte(`{}`)...)); err == nil || !strings.Contains(err.Error(), "trailing JSON") {
		t.Fatalf("trailing JSON error = %v", err)
	}
}

func TestCompactCheckpointRoundTripIsDeterministicAndBounded(t *testing.T) {
	t.Parallel()
	stored := checkpoint{
		Schema: checkpointSchema, ObjectFormat: "sha1",
		Genesis: strings.Repeat("a", 40), Head: strings.Repeat("b", 40),
		Depth: 2, EventCount: 2,
		Events: []checkpointEvent{
			{Payload: []byte(`{"kind":"first"}`), Attachments: map[string][]byte{"z.txt": []byte("last"), "a.txt": []byte("first")}},
			{Payload: []byte(`{"kind":"second"}`)},
		},
	}
	first, err := marshalCheckpoint(stored, maxCheckpointBytes)
	if err != nil {
		t.Fatal(err)
	}
	second, err := marshalCheckpoint(stored, maxCheckpointBytes)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("compact checkpoint encoding is not deterministic")
	}
	if !bytes.HasPrefix(first, []byte(checkpointContainer)) {
		t.Fatal("compact checkpoint has no versioned container marker")
	}
	decoded, err := decodeCheckpoint(first)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, stored) {
		t.Fatalf("compact checkpoint round trip differs:\n got: %#v\nwant: %#v", decoded, stored)
	}
	if _, err := marshalCheckpoint(stored, len(first)-1); !errors.Is(err, errCheckpointTooLarge) {
		t.Fatalf("compact checkpoint over limit error = %v", err)
	}
}

func TestOversizedCheckpointDoesNotRetryOnEveryAppend(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	submitter := NewSubmitter(f.store, Options{SigningKey: f.signingKey, CheckpointEnabled: true})
	if _, err := submitter.Submit(f.ctx, f.request(t, private, "before-oversize", []byte("before"), nil)); err != nil {
		t.Fatal(err)
	}
	submitter.cache.recordCheckpointFailure(errors.Join(errors.New("encode compact checkpoint"), errCheckpointTooLarge))
	writes, failures := submitter.cache.checkpointWrites, submitter.cache.checkpointFailures
	if !submitter.cache.checkpointOversized || submitter.cache.checkpointEvents.count != 0 {
		t.Fatalf("terminal checkpoint failure was not recorded: %+v", submitter.cache)
	}

	// Include an external append so both the delta-advance and local-append
	// retention paths see the terminal state.
	if _, err := Submit(f.ctx, f.store, f.request(t, private, "external", []byte("external"), nil), Options{SigningKey: f.signingKey}); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 3; index++ {
		key := "after-oversize-" + strconv.Itoa(index)
		if _, err := submitter.Submit(f.ctx, f.request(t, private, key, []byte(key), nil)); err != nil {
			t.Fatal(err)
		}
	}
	if submitter.cache.checkpointFailures != failures || submitter.cache.checkpointWrites != writes {
		t.Fatalf("terminal checkpoint failure retried: failures=%d->%d writes=%d->%d", failures, submitter.cache.checkpointFailures, writes, submitter.cache.checkpointWrites)
	}
	if submitter.cache.checkpointEvents.count != 0 || len(submitter.cache.checkpointEvents.tail) != 0 || submitter.cache.checkpointEvents.chunks.count != 0 {
		t.Fatalf("terminal checkpoint retained write material: %+v", submitter.cache.checkpointEvents)
	}
}

func TestOversizedCheckpointRetriesOnceAfterFullRebuild(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	submitter := NewSubmitter(f.store, Options{SigningKey: f.signingKey, CheckpointEnabled: true})
	first, err := submitter.Submit(f.ctx, f.request(t, private, "before-rebuild", []byte("before"), nil))
	if err != nil {
		t.Fatal(err)
	}
	submitter.cache.recordCheckpointFailure(errors.Join(errors.New("encode compact checkpoint"), errCheckpointTooLarge))
	writes, failures := submitter.cache.checkpointWrites, submitter.cache.checkpointFailures
	if !submitter.cache.checkpointOversized || submitter.cache.checkpointEvents.count != 0 {
		t.Fatalf("terminal checkpoint failure was not recorded: %+v", submitter.cache)
	}

	second, err := submitter.Submit(f.ctx, f.request(t, private, "discarded", []byte("discarded"), nil))
	if err != nil {
		t.Fatal(err)
	}
	checkpointCommit := mustHead(t, f.store, CheckpointRef(f.genesis))
	if err := f.store.UpdateRef(f.ctx, CheckpointRef(f.genesis), f.genesis, checkpointCommit); err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpdateRef(f.ctx, Ref(f.genesis), first.Head, second.Head); err != nil {
		t.Fatal(err)
	}

	afterRebuild := f.request(t, private, "after-rebuild", []byte("after"), nil)
	result, err := submitter.Submit(f.ctx, afterRebuild)
	if err != nil {
		t.Fatal(err)
	}
	if submitter.cache.checkpointOversized {
		t.Fatal("full verified rebuild did not clear the oversized checkpoint latch")
	}
	if submitter.cache.checkpointWrites != writes+1 || submitter.cache.checkpointFailures != failures {
		t.Fatalf("rebuild checkpoint attempts: writes=%d->%d failures=%d->%d", writes, submitter.cache.checkpointWrites, failures, submitter.cache.checkpointFailures)
	}
	if head := mustHead(t, f.store, Ref(f.genesis)); head != result.Head || head == first.Head {
		t.Fatalf("rebuilt ref head = %s, result=%s prior=%s", head, result.Head, first.Head)
	}
	loaded, err := NewReader(f.store, CheckpointOptions{Enabled: true}).Load(f.ctx, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Checkpoint || loaded.Verification.Head != result.Head {
		t.Fatalf("rebuilt checkpoint load = %+v, want head %s", loaded, result.Head)
	}

	writes, failures = submitter.cache.checkpointWrites, submitter.cache.checkpointFailures
	if replay, err := submitter.Submit(f.ctx, afterRebuild); err != nil || !replay.Replay {
		t.Fatalf("post-rebuild cache replay = %+v, %v", replay, err)
	}
	if submitter.cache.checkpointWrites != writes || submitter.cache.checkpointFailures != failures {
		t.Fatalf("cache hit retried checkpoint: writes=%d->%d failures=%d->%d", writes, submitter.cache.checkpointWrites, failures, submitter.cache.checkpointFailures)
	}
}

func TestReaderAcceptsLegacyCheckpointAfterCompactUpgrade(t *testing.T) {
	t.Parallel()
	f, _, _, stored := checkpointState(t, 3)
	publishStoredCheckpoint(t, f, stored)
	reader := NewReader(f.store, CheckpointOptions{Enabled: true})
	loaded, err := reader.Load(f.ctx, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Checkpoint || reader.fullScans != 0 || loaded.Verification.Events != 3 || len(loaded.Events) != 3 {
		t.Fatalf("legacy checkpoint after compact upgrade = %+v, full scans=%d", loaded, reader.fullScans)
	}
}

func TestCompactCheckpointAuthenticatesBeforeDecompression(t *testing.T) {
	t.Parallel()
	f, _, _, _ := checkpointState(t, 1)
	checkpointCommit := mustHead(t, f.store, CheckpointRef(f.genesis))
	data, err := f.store.ReadFileLimit(f.ctx, checkpointCommit, checkpointFile, maxCheckpointBytes)
	if err != nil {
		t.Fatal(err)
	}
	_, payload, err := decodeCompactCheckpointManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	malformed := append(bytes.Clone(data[:len(data)-len(payload)]), []byte("not a gzip stream")...)
	wrongKey := filepath.Join(t.TempDir(), "wrong-checkpoint-signer")
	if _, err := gitstore.GenerateSSHKey(f.ctx, wrongKey); err != nil {
		t.Fatal(err)
	}
	publishCheckpointBytes(t, f, malformed, wrongKey, "", checkpointMarker)
	malformedCommit := mustHead(t, f.store, CheckpointRef(f.genesis))
	desc, err := Descriptor(f.ctx, f.store, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	sequence, err := f.store.RevListMetadata(f.ctx, mustHead(t, f.store, Ref(f.genesis)))
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := readCheckpointCandidate(f.ctx, f.store, desc, f.format, f.genesis, malformedCommit)
	if err != nil {
		t.Fatalf("compact manifest should parse without inflating its payload: %v", err)
	}
	if _, err := authenticateCheckpointCandidate(f.ctx, f.store, candidate, desc, sequence); err == nil || !strings.Contains(err.Error(), "signature") || strings.Contains(err.Error(), "gzip") {
		t.Fatalf("unauthenticated compressed payload error = %v", err)
	}
}

func TestCompactCheckpointRejectsAuthenticatedPayloadCorruption(t *testing.T) {
	t.Parallel()
	f, _, _, _ := checkpointState(t, 1)
	checkpointCommit := mustHead(t, f.store, CheckpointRef(f.genesis))
	data, err := f.store.ReadFileLimit(f.ctx, checkpointCommit, checkpointFile, maxCheckpointBytes)
	if err != nil {
		t.Fatal(err)
	}
	_, payload, err := decodeCompactCheckpointManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	malformed := append(bytes.Clone(data[:len(data)-len(payload)]), []byte("not a gzip stream")...)
	publishCheckpointBytes(t, f, malformed, f.signingKey, "", checkpointMarker)
	requireCheckpointFallback(t, f)
}

func TestCompactCheckpointBindsPayloadsToSequenceOrder(t *testing.T) {
	t.Parallel()
	f, _, _, stored := checkpointState(t, 2)
	stored.Schema = checkpointSchema
	stored.Profile = ""
	stored.EventCount = len(stored.Events)
	stored.Events[0], stored.Events[1] = stored.Events[1], stored.Events[0]
	data, err := marshalCheckpoint(stored, maxCheckpointBytes)
	if err != nil {
		t.Fatal(err)
	}
	publishCheckpointBytes(t, f, data, f.signingKey, "", checkpointMarker)
	requireCheckpointFallback(t, f)
}

func TestCheckpointManifestBoundsCannotOverflowSequenceIndex(t *testing.T) {
	t.Parallel()
	stored := checkpoint{
		Schema: checkpointSchema, Genesis: strings.Repeat("a", 40),
		Head: strings.Repeat("a", 40), Depth: maxIntValue(), EventCount: 0,
	}
	sequence := []gitstore.CommitMetadata{{OID: stored.Genesis}}
	if _, err := checkpointEventPositions(stored, stored.EventCount, GenesisDescriptor{}, sequence); err == nil || !strings.Contains(err.Error(), "named sequence") {
		t.Fatalf("oversized checkpoint depth error = %v", err)
	}
}

func TestCompactCheckpointRejectsTrailingCompressedBytes(t *testing.T) {
	t.Parallel()
	stored := checkpoint{
		Schema: checkpointSchema, ObjectFormat: "sha1",
		Genesis: strings.Repeat("a", 40), Head: strings.Repeat("b", 40),
		Depth: 1, EventCount: 1,
		Events: []checkpointEvent{{Payload: []byte("payload")}},
	}
	data, err := marshalCheckpoint(stored, maxCheckpointBytes)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeCheckpoint(append(data, 0)); err == nil || !strings.Contains(err.Error(), "trailing compressed bytes") {
		t.Fatalf("trailing compact bytes error = %v", err)
	}
}

func TestCheckpointCachedPayloadSharesEnvelopeCeiling(t *testing.T) {
	t.Parallel()
	f, _, _, stored := checkpointState(t, 1)
	sequence, err := f.store.RevListMetadata(f.ctx, stored.Head)
	if err != nil {
		t.Fatal(err)
	}
	desc, err := Descriptor(f.ctx, f.store, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	combined := len(sequence[1].Message) + len(stored.Events[0].Payload)
	if combined <= len(sequence[1].Message) || combined <= len(stored.Events[0].Payload) {
		t.Fatal("fixture does not separate envelope and payload sizes")
	}
	desc.PayloadCeiling = uint64(combined - 1)
	if _, err := validateCheckpoint(stored, desc, sequence); err == nil {
		t.Fatal("checkpoint accepted payload that only fits when the envelope is not charged")
	}
}

func TestCheckpointPayloadAndAttachmentsShareOneCeiling(t *testing.T) {
	t.Parallel()
	payload := []byte("1234")
	attachments := map[string][]byte{"first": []byte("123"), "second": []byte("456")}
	if err := payloadWithinCeiling(payload, attachments, 10); err != nil {
		t.Fatalf("exact combined ceiling rejected: %v", err)
	}
	if err := payloadWithinCeiling(payload, attachments, 9); err == nil {
		t.Fatal("attachments individually under but jointly over the remaining ceiling were accepted")
	}
}

func TestCheckpointMarshalEnforcesDocumentedSizeLimit(t *testing.T) {
	t.Parallel()
	if maxCheckpointBytes != 256<<20 {
		t.Fatalf("checkpoint limit = %d, want 256 MiB", maxCheckpointBytes)
	}
	stored := checkpoint{Schema: legacyCheckpointSchema, Profile: "fold@1"}
	data, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := marshalCheckpoint(stored, len(data)); err != nil {
		t.Fatalf("checkpoint at limit rejected: %v", err)
	}
	if _, err := marshalCheckpoint(stored, len(data)-1); err == nil {
		t.Fatal("checkpoint over configured size limit was accepted")
	}
}

func TestWrittenCheckpointContainsOnlyKernelVerifiedMaterial(t *testing.T) {
	t.Parallel()
	f, _, _, _ := checkpointState(t, 1)
	commit := mustHead(t, f.store, CheckpointRef(f.genesis))
	data, err := f.store.ReadFileLimit(f.ctx, commit, checkpointFile, maxCheckpointBytes)
	if err != nil {
		t.Fatal(err)
	}
	manifestStart := len(checkpointContainer) + 4
	if len(data) < manifestStart {
		t.Fatalf("compact checkpoint is truncated: %d bytes", len(data))
	}
	manifestEnd := manifestStart + int(binary.BigEndian.Uint32(data[len(checkpointContainer):manifestStart]))
	if manifestEnd > len(data) {
		t.Fatalf("compact checkpoint manifest exceeds payload: end=%d size=%d", manifestEnd, len(data))
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data[manifestStart:manifestEnd], &fields); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"schema", "object_format", "genesis", "head", "depth", "events"} {
		if _, ok := fields[name]; !ok {
			t.Fatalf("kernel checkpoint omitted %q: %s", name, data)
		}
	}
	for _, forbidden := range []string{"profile", "projection", "vocabulary", "fold"} {
		if _, ok := fields[forbidden]; ok {
			t.Fatalf("kernel checkpoint carried application field %q: %s", forbidden, data)
		}
	}
	if len(fields) != 6 {
		t.Fatalf("kernel checkpoint fields = %v, want only verified identity and events", fields)
	}
}

func TestCheckpointMetadataBindsEnvelopeTrailersAndTreeIndependently(t *testing.T) {
	t.Parallel()
	f, private, _, original := checkpointState(t, 1)
	originalSequence, err := f.store.RevListMetadata(f.ctx, original.Head)
	if err != nil {
		t.Fatal(err)
	}
	desc, err := Descriptor(f.ctx, f.store, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		want   string
		mutate func(*checkpoint, []gitstore.CommitMetadata)
	}{
		{name: "envelope", want: "cached intent differs", mutate: func(stored *checkpoint, _ []gitstore.CommitMetadata) {
			decoded := mustVerifyIntent(t, stored.Events[0].Signed)
			decoded.Schema += ".forged"
			stored.Events[0].Signed = mustSignIntent(t, decoded, private)
		}},
		{name: "causal trailers", want: "causal trailers differ", mutate: func(stored *checkpoint, sequence []gitstore.CommitMetadata) {
			decoded := mustVerifyIntent(t, stored.Events[0].Signed)
			trailers := append(append([]string(nil), decoded.RestsOn...), "git:sha1:"+strings.Repeat("f", 40))
			sequence[1].Message = intent.Envelope(stored.Events[0].Signed, trailers)
		}},
		{name: "commit tree", want: "commit tree differs", mutate: func(_ *checkpoint, sequence []gitstore.CommitMetadata) {
			sequence[1].Tree = strings.Repeat("f", 40)
		}},
		{name: "commit timestamp", want: "cached timestamp differs", mutate: func(stored *checkpoint, _ []gitstore.CommitMetadata) {
			stored.Events[0].Timestamp++
		}},
		{name: "commit envelope", want: "commit envelope", mutate: func(_ *checkpoint, sequence []gitstore.CommitMetadata) {
			sequence[1].Message = "not a signed envelope"
		}},
		{name: "target names checkpoint genesis", want: "target does not name checkpoint genesis", mutate: func(stored *checkpoint, sequence []gitstore.CommitMetadata) {
			decoded := mustVerifyIntent(t, stored.Events[0].Signed)
			decoded.Target = "git:sha1:" + strings.Repeat("f", 40)
			stored.Events[0].Signed = mustSignIntent(t, decoded, private)
			sequence[1].Message = intent.Envelope(stored.Events[0].Signed, decoded.RestsOn)
		}},
		{name: "payload tree identity", want: "payload tree identity", mutate: func(stored *checkpoint, sequence []gitstore.CommitMetadata) {
			decoded := mustVerifyIntent(t, stored.Events[0].Signed)
			decoded.PayloadTree = "not-a-typed-object-id"
			stored.Events[0].Signed = mustSignIntent(t, decoded, private)
			sequence[1].Message = intent.Envelope(stored.Events[0].Signed, decoded.RestsOn)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stored := cloneCheckpoint(t, original)
			sequence := append([]gitstore.CommitMetadata(nil), originalSequence...)
			test.mutate(&stored, sequence)
			if _, err := validateCheckpoint(stored, desc, sequence); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("checkpoint metadata mutation error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestTruncatedCheckpointCannotPoisonSubmitterDedup(t *testing.T) {
	t.Parallel()
	f, _, requests, stored := checkpointState(t, 3)
	stored.Events = append([]checkpointEvent(nil), stored.Events[1:]...)
	stored.Depth = len(stored.Events)
	publishStoredCheckpoint(t, f, stored)
	before := mustHead(t, f.store, Ref(f.genesis))
	submitter := NewSubmitter(f.store, Options{SigningKey: f.signingKey, CheckpointEnabled: true})
	replay, err := submitter.Submit(f.ctx, requests[0])
	if err != nil {
		t.Fatal(err)
	}
	if !replay.Replay || submitter.cache.checkpointLoads != 0 || submitter.cache.checkpointFallbacks != 1 || submitter.cache.fullScans != 1 {
		t.Fatalf("truncated checkpoint replay = %+v cache=%+v", replay, submitter.cache)
	}
	if after := mustHead(t, f.store, Ref(f.genesis)); after != before {
		t.Fatalf("replay after hostile checkpoint appended duplicate: before=%s after=%s", before, after)
	}
}

func TestStatelessSubmitIgnoresCheckpointEnablementWithoutWritingOrPanicking(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	request := f.request(t, actor(t), "stateless-profile", []byte("stateless-profile"), nil)
	result, err := Submit(f.ctx, f.store, request, Options{SigningKey: f.signingKey, CheckpointEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Head == "" {
		t.Fatal("stateless submission returned no head")
	}
	if _, err := f.store.Head(f.ctx, CheckpointRef(f.genesis)); err == nil {
		t.Fatal("stateless submission published a checkpoint")
	}
}

func TestReaderRetriesCheckpointWriteAfterFailure(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	if _, err := Submit(f.ctx, f.store, f.request(t, private, "one", []byte("one"), nil), Options{SigningKey: f.signingKey}); err != nil {
		t.Fatal(err)
	}
	reader := NewReader(f.store, CheckpointOptions{Enabled: true, SigningKey: filepath.Join(t.TempDir(), "missing-key")})
	if _, err := reader.Load(f.ctx, f.genesis); err != nil {
		t.Fatal(err)
	}
	if reader.checkpointFailures != 1 || reader.checkpointWrites != 0 {
		t.Fatalf("first failed checkpoint write: failures=%d writes=%d", reader.checkpointFailures, reader.checkpointWrites)
	}
	if _, err := Submit(f.ctx, f.store, f.request(t, private, "two", []byte("two"), nil), Options{SigningKey: f.signingKey}); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Load(f.ctx, f.genesis); err != nil {
		t.Fatal(err)
	}
	if reader.checkpointFailures != 2 || reader.checkpointWrites != 0 {
		t.Fatalf("failed write was postponed by cadence: failures=%d writes=%d", reader.checkpointFailures, reader.checkpointWrites)
	}
	if _, err := Submit(f.ctx, f.store, f.request(t, private, "three", []byte("three"), nil), Options{SigningKey: f.signingKey}); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Load(f.ctx, f.genesis); err != nil {
		t.Fatal(err)
	}
	if reader.checkpointFailures != 3 || reader.checkpointWrites != 0 {
		t.Fatalf("gated failure advanced cadence: failures=%d writes=%d", reader.checkpointFailures, reader.checkpointWrites)
	}
}

func TestSubmitterColdStartUsesSignedCheckpointDedup(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	prior := f.request(t, private, "prior", []byte("prior"), nil)
	if _, err := Submit(f.ctx, f.store, prior, Options{SigningKey: f.signingKey}); err != nil {
		t.Fatal(err)
	}
	checkpoint := CheckpointOptions{Enabled: true, SigningKey: f.signingKey}
	if _, err := NewReader(f.store, checkpoint).Load(f.ctx, f.genesis); err != nil {
		t.Fatal(err)
	}
	submitter := NewSubmitter(f.store, Options{SigningKey: f.signingKey, CheckpointEnabled: true})
	if replay, err := submitter.Submit(f.ctx, prior); err != nil || !replay.Replay {
		t.Fatalf("checkpoint-backed replay = %+v, %v", replay, err)
	}
	if submitter.cache.checkpointLoads != 1 || submitter.cache.fullScans != 0 {
		t.Fatalf("submitter checkpoint loads=%d full scans=%d", submitter.cache.checkpointLoads, submitter.cache.fullScans)
	}
}

func TestSubmitterRefreshesCheckpointOnBoundedCadence(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	prior := f.request(t, private, "prior", []byte("prior"), nil)
	if _, err := Submit(f.ctx, f.store, prior, Options{SigningKey: f.signingKey}); err != nil {
		t.Fatal(err)
	}
	checkpoint := CheckpointOptions{Enabled: true, SigningKey: f.signingKey}
	if _, err := NewReader(f.store, checkpoint).Load(f.ctx, f.genesis); err != nil {
		t.Fatal(err)
	}
	before, err := f.store.Head(f.ctx, CheckpointRef(f.genesis))
	if err != nil {
		t.Fatal(err)
	}
	submitter := NewSubmitter(f.store, Options{SigningKey: f.signingKey, CheckpointEnabled: true})
	if replay, err := submitter.Submit(f.ctx, prior); err != nil || !replay.Replay {
		t.Fatalf("load checkpoint = %+v, %v", replay, err)
	}
	// Move only the cadence counter; the next real append must publish the
	// complete valid event set retained from the checkpoint.
	submitter.cache.checkpointAttempt = submitter.cache.log.Verification.Depth - checkpointInterval + 1
	next := f.request(t, private, "next", []byte("next"), nil)
	result, err := submitter.Submit(f.ctx, next)
	if err != nil {
		t.Fatal(err)
	}
	after, err := f.store.Head(f.ctx, CheckpointRef(f.genesis))
	if err != nil {
		t.Fatal(err)
	}
	if after == before || submitter.cache.checkpointWrites != 1 {
		t.Fatalf("checkpoint was not refreshed: before=%s after=%s writes=%d", before, after, submitter.cache.checkpointWrites)
	}
	reader := NewReader(f.store, CheckpointOptions{Enabled: true})
	loaded, err := reader.Load(f.ctx, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Checkpoint || loaded.Verification.Head != result.Head || len(loaded.Events) != 2 {
		t.Fatalf("refreshed checkpoint = %+v", loaded)
	}
}

func TestSubmitterDefersDueCheckpointUntilItsCASAppend(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	prior := f.request(t, private, "prior", []byte("prior"), nil)
	if _, err := Submit(f.ctx, f.store, prior, Options{SigningKey: f.signingKey}); err != nil {
		t.Fatal(err)
	}
	checkpoint := CheckpointOptions{Enabled: true, SigningKey: f.signingKey}
	if _, err := NewReader(f.store, checkpoint).Load(f.ctx, f.genesis); err != nil {
		t.Fatal(err)
	}
	submitter := NewSubmitter(f.store, Options{SigningKey: f.signingKey, CheckpointEnabled: true})
	if replay, err := submitter.Submit(f.ctx, prior); err != nil || !replay.Replay {
		t.Fatalf("load checkpoint = %+v, %v", replay, err)
	}
	submitter.cache.checkpointAttempt = submitter.cache.log.Verification.Depth - checkpointInterval + 1
	if _, err := Submit(f.ctx, f.store, f.request(t, private, "external", []byte("external"), nil), Options{SigningKey: f.signingKey}); err != nil {
		t.Fatal(err)
	}
	result, err := submitter.Submit(f.ctx, f.request(t, private, "resident", []byte("resident"), nil))
	if err != nil {
		t.Fatal(err)
	}
	if submitter.cache.checkpointWrites != 1 || submitter.cache.checkpointFailures != 0 {
		t.Fatalf("checkpoint writes=%d failures=%d, want one post-CAS write", submitter.cache.checkpointWrites, submitter.cache.checkpointFailures)
	}
	loaded, err := NewReader(f.store, CheckpointOptions{Enabled: true}).Load(f.ctx, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Checkpoint || loaded.Verification.Head != result.Head || len(loaded.Events) != 3 {
		t.Fatalf("post-CAS checkpoint = %+v, result = %+v", loaded, result)
	}
}

func TestReaderFallsBackToFullAuditAfterRefRewind(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	reader := NewReader(f.store)
	first, err := Submit(f.ctx, f.store, f.request(t, private, "first", []byte("first"), nil), Options{SigningKey: f.signingKey})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Load(f.ctx, f.genesis); err != nil {
		t.Fatal(err)
	}
	second, err := Submit(f.ctx, f.store, f.request(t, private, "second", []byte("second"), nil), Options{SigningKey: f.signingKey})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Load(f.ctx, f.genesis); err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpdateRef(f.ctx, Ref(f.genesis), first.Head, second.Head); err != nil {
		t.Fatal(err)
	}
	rewound, err := reader.Load(f.ctx, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	if !rewound.Full || len(rewound.Events) != 1 || string(rewound.Events[0].Payload) != "first" {
		t.Fatalf("rewound load = %+v", rewound)
	}
	if reader.fullScans != 2 || reader.deltaScans != 1 {
		t.Fatalf("rewind scans = full %d delta %d, want 2 and 1", reader.fullScans, reader.deltaScans)
	}
}

func TestReaderFallsBackToFullAuditAfterSiblingAdvance(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	first, err := Submit(f.ctx, f.store, f.request(t, private, "fork-first", []byte("first"), nil), Options{SigningKey: f.signingKey})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Submit(f.ctx, f.store, f.request(t, private, "fork-second", []byte("second"), nil), Options{SigningKey: f.signingKey})
	if err != nil {
		t.Fatal(err)
	}
	reader := NewReader(f.store)
	if _, err := reader.Load(f.ctx, f.genesis); err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpdateRef(f.ctx, Ref(f.genesis), first.Head, second.Head); err != nil {
		t.Fatal(err)
	}
	sibling := appendExternalCommit(t, f, f.request(t, private, "fork-sibling", []byte("sibling"), nil), first.Head, f.signingKey)
	loaded, err := reader.Load(f.ctx, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Full || loaded.Verification.Head != sibling || len(loaded.Events) != 2 || string(loaded.Events[1].Payload) != "sibling" {
		t.Fatalf("sibling load did not force an exact full audit: %+v", loaded)
	}
	if reader.fullScans != 2 || reader.deltaScans != 0 {
		t.Fatalf("sibling scans = full %d delta %d, want 2 and 0", reader.fullScans, reader.deltaScans)
	}
}

func TestReaderDeltaRejectsExternalReplayLikeColdAudit(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	request := f.request(t, private, "external-replay", []byte("once"), nil)
	first, err := Submit(f.ctx, f.store, request, Options{SigningKey: f.signingKey})
	if err != nil {
		t.Fatal(err)
	}
	reader := NewReader(f.store)
	if _, err := reader.Load(f.ctx, f.genesis); err != nil {
		t.Fatal(err)
	}
	duplicate := appendExternalCommit(t, f, request, first.Head, f.signingKey)
	if _, err := reader.Load(f.ctx, f.genesis); err == nil || !strings.Contains(err.Error(), "duplicates idempotent event") {
		t.Fatalf("resident delta accepted external replay %s: %v", duplicate, err)
	}
	if _, err := Verify(f.ctx, f.store, f.genesis); err == nil || !strings.Contains(err.Error(), "duplicates idempotent event") {
		t.Fatalf("cold audit did not reject the same replay: %v", err)
	}
	if reader.head != first.Head || reader.deltaScans != 0 {
		t.Fatalf("rejected replay mutated resident frontier: head=%s delta=%d", reader.head, reader.deltaScans)
	}
}

func TestFailedMultiCommitDeltaDoesNotLeakDedupAdditions(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	base, err := Submit(f.ctx, f.store, f.request(t, private, "leak-base", []byte("base"), nil), Options{SigningKey: f.signingKey})
	if err != nil {
		t.Fatal(err)
	}
	reader := NewReader(f.store)
	if _, err := reader.Load(f.ctx, f.genesis); err != nil {
		t.Fatal(err)
	}
	trustedDedup := len(reader.log.Dedup)
	valid := appendExternalCommit(t, f, f.request(t, private, "leak-valid", []byte("valid"), nil), base.Head, f.signingKey)
	wrongKey := filepath.Join(t.TempDir(), "wrong-sequencer")
	if _, err := gitstore.GenerateSSHKey(f.ctx, wrongKey); err != nil {
		t.Fatal(err)
	}
	invalid := appendExternalCommit(t, f, f.request(t, private, "leak-invalid", []byte("invalid"), nil), valid, wrongKey)
	if _, err := reader.Load(f.ctx, f.genesis); err == nil || !strings.Contains(err.Error(), "sequencer signature") {
		t.Fatalf("reader accepted failed two-commit delta: %v", err)
	}
	if reader.head != base.Head || len(reader.log.Dedup) != trustedDedup || reader.deltaScans != 0 {
		t.Fatalf("failed delta leaked state: head=%s dedup=%d delta=%d", reader.head, len(reader.log.Dedup), reader.deltaScans)
	}
	if err := f.store.UpdateRef(f.ctx, Ref(f.genesis), valid, invalid); err != nil {
		t.Fatal(err)
	}
	loaded, err := reader.Load(f.ctx, f.genesis)
	if err != nil {
		t.Fatalf("valid prefix was poisoned by failed suffix: %v", err)
	}
	if loaded.Full || len(loaded.Events) != 1 || loaded.Events[0].Commit != valid || len(reader.log.Dedup) != trustedDedup+1 {
		t.Fatalf("valid prefix catch-up = %+v, dedup=%d", loaded, len(reader.log.Dedup))
	}
}

func TestReaderRejectsInvalidDeltaWithoutMutatingVerifiedCache(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	reader := NewReader(f.store)
	valid, err := Submit(f.ctx, f.store, f.request(t, private, "valid", []byte("valid"), nil), Options{SigningKey: f.signingKey})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Load(f.ctx, f.genesis); err != nil {
		t.Fatal(err)
	}
	trusted := reader.log.Verification
	request := f.request(t, private, "invalid", []byte("invalid"), []string{"git:sha1:external#git:sha1:event"})
	decoded := mustVerifyIntent(t, request.Signed)
	_, signedTree, err := gitstore.ParseTypedOID(decoded.PayloadTree)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := f.store.WritePayloadTree(f.ctx, request.Payload, request.Attachments)
	if err != nil {
		t.Fatal(err)
	}
	if tree != signedTree {
		t.Fatalf("copied payload tree = %s, signed = %s", tree, signedTree)
	}
	malicious, err := f.store.SignedCommit(f.ctx, tree, valid.Head, intent.Envelope(request.Signed, []string{"git:sha1:altered#git:sha1:event"}), f.signingKey, gitstore.CommitIdentity{
		AuthorName: "attacker", AuthorEmail: "attacker@example.invalid",
		CommitterName: "gitseq sequencer", CommitterEmail: "sequencer@gitseq.invalid",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpdateRef(f.ctx, Ref(f.genesis), malicious, valid.Head); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Load(f.ctx, f.genesis); err == nil {
		t.Fatal("reader accepted altered causal trailers in delta")
	}
	if reader.head != trusted.Head || reader.log.Verification != trusted || reader.deltaScans != 0 {
		t.Fatalf("failed delta mutated reader: head=%s verification=%+v scans=%d", reader.head, reader.log.Verification, reader.deltaScans)
	}
	if err := f.store.UpdateRef(f.ctx, Ref(f.genesis), valid.Head, malicious); err != nil {
		t.Fatal(err)
	}
	exact, err := reader.Load(f.ctx, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	if exact.Verification != trusted || reader.cacheHits != 1 {
		t.Fatalf("restored exact load = %+v, hits=%d", exact, reader.cacheHits)
	}
}

func TestReaderRejectsWrongSequencerDeltaWithoutMutatingVerifiedCache(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	reader := NewReader(f.store)
	valid, err := Submit(f.ctx, f.store, f.request(t, private, "valid-sequencer", []byte("valid"), nil), Options{SigningKey: f.signingKey})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Load(f.ctx, f.genesis); err != nil {
		t.Fatal(err)
	}
	trustedHead := reader.head
	trustedTarget := reader.target
	trustedVerification := reader.log.Verification
	trustedDedup := make(map[string]Event, len(reader.log.Dedup))
	for key, event := range reader.log.Dedup {
		trustedDedup[key] = event
	}

	request := f.request(t, private, "wrong-sequencer-delta", []byte("invalid"), nil)
	decoded := mustVerifyIntent(t, request.Signed)
	tree, err := f.store.WritePayloadTree(f.ctx, request.Payload, request.Attachments)
	if err != nil {
		t.Fatal(err)
	}
	wrongKey := filepath.Join(t.TempDir(), "wrong-sequencer")
	if _, err := gitstore.GenerateSSHKey(f.ctx, wrongKey); err != nil {
		t.Fatal(err)
	}
	commit, err := f.store.SignedCommit(f.ctx, tree, valid.Head, intent.Envelope(request.Signed, decoded.RestsOn), wrongKey, gitstore.CommitIdentity{
		AuthorName: "wrong sequencer", AuthorEmail: "wrong@example.invalid",
		CommitterName: "wrong sequencer", CommitterEmail: "wrong@example.invalid",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpdateRef(f.ctx, Ref(f.genesis), commit, valid.Head); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Load(f.ctx, f.genesis); err == nil {
		t.Fatal("reader accepted a delta signed by a non-descriptor sequencer")
	}
	if reader.head != trustedHead || reader.target != trustedTarget || reader.log.Verification != trustedVerification || reader.deltaScans != 0 {
		t.Fatalf("failed delta mutated reader: head=%s target=%s verification=%+v scans=%d", reader.head, reader.target, reader.log.Verification, reader.deltaScans)
	}
	if len(reader.log.Dedup) != len(trustedDedup) {
		t.Fatalf("failed delta changed dedup size: got=%d want=%d", len(reader.log.Dedup), len(trustedDedup))
	}
	for key, want := range trustedDedup {
		got, ok := reader.log.Dedup[key]
		if !ok || got.Commit != want.Commit || !got.Signed.Equal(want.Signed) {
			t.Fatalf("failed delta changed dedup entry %q", key)
		}
	}
}

func TestContinuationBindsVerifiedSealedFrontier(t *testing.T) {
	t.Parallel()
	predecessor := newFixture(t, "sha1")
	request := predecessor.request(t, actor(t), "seal", []byte("seal"), nil)
	if _, err := Submit(predecessor.ctx, predecessor.store, request, Options{SigningKey: predecessor.signingKey}); err != nil {
		t.Fatal(err)
	}
	sealed, err := Verify(predecessor.ctx, predecessor.store, predecessor.genesis)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	successorStore, err := gitstore.InitBare(predecessor.ctx, filepath.Join(root, "successor.git"), "sha1")
	if err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(root, "sequencer")
	publicKey, err := gitstore.GenerateSSHKey(predecessor.ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	successor, err := Create(predecessor.ctx, successorStore, GenesisDescriptor{Version: 0, ObjectFormat: "sha1", PayloadCeiling: 1 << 20, SequencerPublicKey: publicKey, PredecessorGenesis: sealed.Genesis, SealedHead: sealed.Head}, key)
	if err != nil {
		t.Fatal(err)
	}
	verification, err := VerifyContinuation(predecessor.ctx, predecessor.store, predecessor.genesis, successorStore, successor)
	if err != nil {
		t.Fatal(err)
	}
	if verification.Predecessor.Head != sealed.Head || verification.Successor.Genesis != successor {
		t.Fatalf("unexpected continuation: %+v", verification)
	}
}

func TestScanHeadPinsTheApprovedFrontier(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	if _, err := Submit(f.ctx, f.store, f.request(t, private, "one", []byte("one"), nil), Options{SigningKey: f.signingKey}); err != nil {
		t.Fatal(err)
	}
	approved, err := Verify(f.ctx, f.store, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Submit(f.ctx, f.store, f.request(t, private, "two", []byte("two"), nil), Options{SigningKey: f.signingKey}); err != nil {
		t.Fatal(err)
	}
	log, err := scanHead(f.ctx, f.store, approved.Genesis, approved.Head, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	events, loaded := log.Events, log.Verification
	if loaded.Head != approved.Head || len(events) != 1 || string(events[0].Payload) != "one" {
		t.Fatalf("load crossed verified frontier: approved=%+v loaded=%+v events=%+v", approved, loaded, events)
	}
}

func mustVerifyIntent(t *testing.T, signed intent.Signed) intent.Intent {
	t.Helper()
	decoded, err := intent.Verify(signed)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func mustSignIntent(t *testing.T, decoded intent.Intent, private ed25519.PrivateKey) intent.Signed {
	t.Helper()
	signed, err := intent.Sign(decoded, private)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

// The audit is the slowest thing this package does and, before this, the least
// able to say so: Load holds r.mu for its whole duration, so anything that
// waited for that lock could only ever answer once there was nothing left to
// report. This proves the counter is readable *while* the scan runs, and moves.
//
// The poller is deliberately never joined. If Snapshot needed r.mu it would
// block there for the whole audit, and joining would turn a wrong design into
// a hung test instead of a failing one.
func TestColdAuditReportsItsProgressWhileHoldingTheLock(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	for index := range 12 {
		key := "progress-" + strconv.Itoa(index)
		if _, err := Submit(f.ctx, f.store, f.request(t, private, key, []byte(key), nil), Options{SigningKey: f.signingKey}); err != nil {
			t.Fatal(err)
		}
	}

	reader := NewReader(f.store)
	report := &AuditProgress{}
	var samples, highest, inconsistent atomic.Int64
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			if p := report.Snapshot(); p.Started && p.Total > 0 {
				samples.Add(1)
				if int64(p.Verified) > highest.Load() {
					highest.Store(int64(p.Verified))
				}
				if p.Verified > p.Total {
					inconsistent.Add(1)
				}
			}
			runtime.Gosched()
		}
	}()

	if _, err := reader.LoadWithProgress(f.ctx, f.genesis, report); err != nil {
		t.Fatal(err)
	}
	close(stop)

	if samples.Load() == 0 {
		t.Fatal("no progress was observable while the cold audit ran; a reader waiting on it could not be told why")
	}
	if highest.Load() == 0 {
		t.Error("progress never advanced past zero")
	}
	if n := inconsistent.Load(); n > 0 {
		t.Errorf("%d samples claimed more verified than there were to verify", n)
	}
	// Retained afterwards so an application can describe checkpoint writing
	// and projection work without inventing its own verification counter.
	if p := report.Snapshot(); !p.Started || p.Total == 0 || p.Verified != p.Total {
		t.Errorf("final progress was not retained at N/N: %+v", p)
	}
}

func TestMatchingCheckpointDoesNotStartColdAuditProgress(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	if _, err := Submit(f.ctx, f.store, f.request(t, private, "checkpoint-progress", []byte("value"), nil), Options{SigningKey: f.signingKey}); err != nil {
		t.Fatal(err)
	}
	options := CheckpointOptions{Enabled: true, SigningKey: f.signingKey}
	if _, err := NewReader(f.store, options).Load(f.ctx, f.genesis); err != nil {
		t.Fatal(err)
	}

	warm := NewReader(f.store, options)
	warmReport := &AuditProgress{}
	if _, err := warm.LoadWithProgress(f.ctx, f.genesis, warmReport); err != nil {
		t.Fatal(err)
	}
	if p := warmReport.Snapshot(); p.Started || p.Total != 0 || p.Verified != 0 {
		t.Errorf("matching checkpoint started cold-audit progress: %+v", p)
	}
}

func TestReaderStreamsColdFullLoadWithoutRetainingTransportEvents(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	want := make([]string, 6)
	for index := range want {
		want[index] = "stream-" + strconv.Itoa(index)
		if _, err := Submit(f.ctx, f.store, f.request(t, private, want[index], []byte(want[index]), nil), Options{SigningKey: f.signingKey}); err != nil {
			t.Fatal(err)
		}
	}

	reader := NewReader(f.store)
	var got []string
	loaded, err := reader.LoadWithProgressStream(f.ctx, f.genesis, nil, func(event Event) error {
		got = append(got, string(event.Payload))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Full || loaded.Checkpoint || len(loaded.Events) != 0 {
		t.Fatalf("streamed load = full %v checkpoint %v events %d", loaded.Full, loaded.Checkpoint, len(loaded.Events))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("streamed payloads = %q, want %q", got, want)
	}
	if loaded.Verification.Events != len(want) || len(reader.log.Dedup) != len(want) || len(reader.log.Events) != 0 {
		t.Fatalf("verified stream state = verification %+v dedup %d retained %d", loaded.Verification, len(reader.log.Dedup), len(reader.log.Events))
	}

	callbackCalls := 0
	cached, err := reader.LoadWithProgressStream(f.ctx, f.genesis, nil, func(Event) error {
		callbackCalls++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if cached.Full || len(cached.Events) != 0 || callbackCalls != 0 || reader.cacheHits != 1 {
		t.Fatalf("cached stream = %+v callback calls %d cache hits %d", cached, callbackCalls, reader.cacheHits)
	}
}

func TestReaderStreamFailureDoesNotPublishVerifiedCache(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	for index := range 4 {
		key := "stream-failure-" + strconv.Itoa(index)
		if _, err := Submit(f.ctx, f.store, f.request(t, private, key, []byte(key), nil), Options{SigningKey: f.signingKey}); err != nil {
			t.Fatal(err)
		}
	}

	reader := NewReader(f.store)
	refusal := errors.New("provisional consumer refused")
	calls := 0
	if _, err := reader.LoadWithProgressStream(f.ctx, f.genesis, nil, func(Event) error {
		calls++
		if calls == 3 {
			return refusal
		}
		return nil
	}); !errors.Is(err, refusal) {
		t.Fatalf("stream failure = %v, want %v", err, refusal)
	}
	if calls != 3 {
		t.Fatalf("callback calls = %d, want 3", calls)
	}
	if reader.target != "" || reader.head != "" || reader.log.Verification != (Verification{}) || len(reader.log.Dedup) != 0 || reader.fullScans != 0 {
		t.Fatalf("failed stream published reader cache: target=%q head=%q verification=%+v dedup=%d scans=%d", reader.target, reader.head, reader.log.Verification, len(reader.log.Dedup), reader.fullScans)
	}

	loaded, err := reader.Load(f.ctx, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Full || len(loaded.Events) != 4 || loaded.Verification.Events != 4 || reader.fullScans != 1 {
		t.Fatalf("recovery load = %+v scans=%d", loaded, reader.fullScans)
	}
}

func TestReaderStreamsAuthenticatedCheckpointAndVerifiedSuffix(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	want := make([]string, 6)
	for index := range 4 {
		want[index] = "checkpoint-stream-" + strconv.Itoa(index)
		if _, err := Submit(f.ctx, f.store, f.request(t, private, want[index], []byte(want[index]), nil), Options{SigningKey: f.signingKey}); err != nil {
			t.Fatal(err)
		}
	}
	writer := NewReader(f.store, CheckpointOptions{Enabled: true, SigningKey: f.signingKey})
	if _, err := writer.Load(f.ctx, f.genesis); err != nil {
		t.Fatal(err)
	}
	for index := 4; index < len(want); index++ {
		want[index] = "checkpoint-stream-" + strconv.Itoa(index)
		if _, err := Submit(f.ctx, f.store, f.request(t, private, want[index], []byte(want[index]), nil), Options{SigningKey: f.signingKey}); err != nil {
			t.Fatal(err)
		}
	}

	reader := NewReader(f.store, CheckpointOptions{Enabled: true})
	var got []string
	loaded, err := reader.LoadWithProgressStream(f.ctx, f.genesis, nil, func(event Event) error {
		got = append(got, string(event.Payload))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Full || !loaded.Checkpoint || len(loaded.Events) != 0 || reader.checkpointLoads != 1 || reader.fullScans != 0 {
		t.Fatalf("checkpoint stream = %+v checkpoint loads %d full scans %d", loaded, reader.checkpointLoads, reader.fullScans)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("checkpoint stream payloads = %q, want %q", got, want)
	}
	if loaded.Verification.Events != len(want) || len(reader.log.Dedup) != len(want) || len(reader.log.Events) != 0 {
		t.Fatalf("checkpoint stream state = verification %+v dedup %d retained %d", loaded.Verification, len(reader.log.Dedup), len(reader.log.Events))
	}
}

func TestCheckpointStreamFailureDoesNotRetryOrPublishVerifiedCache(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	for index := range 4 {
		key := "checkpoint-stream-failure-" + strconv.Itoa(index)
		if _, err := Submit(f.ctx, f.store, f.request(t, private, key, []byte(key), nil), Options{SigningKey: f.signingKey}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := NewReader(f.store, CheckpointOptions{Enabled: true, SigningKey: f.signingKey}).Load(f.ctx, f.genesis); err != nil {
		t.Fatal(err)
	}

	reader := NewReader(f.store, CheckpointOptions{Enabled: true})
	refusal := errors.New("checkpoint consumer refused")
	calls := 0
	if _, err := reader.LoadWithProgressStream(f.ctx, f.genesis, nil, func(Event) error {
		calls++
		if calls == 3 {
			return refusal
		}
		return nil
	}); !errors.Is(err, refusal) {
		t.Fatalf("checkpoint stream failure = %v, want %v", err, refusal)
	}
	if calls != 3 || reader.target != "" || reader.head != "" || reader.fullScans != 0 || reader.checkpointFallbacks != 0 {
		t.Fatalf("failed checkpoint stream calls=%d target=%q head=%q scans=%d fallbacks=%d", calls, reader.target, reader.head, reader.fullScans, reader.checkpointFallbacks)
	}
}
