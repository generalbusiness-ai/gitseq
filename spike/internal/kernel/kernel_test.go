package kernel

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"gitseq/spike/internal/gitstore"
	"gitseq/spike/internal/intent"
)

type fixtureState struct {
	ctx        context.Context
	store      gitstore.Store
	scratch    gitstore.Store
	signingKey string
	publicKey  string
	genesis    string
	format     string
}

func newFixture(t testing.TB, format string) fixtureState {
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
		Version: 0, ObjectFormat: format, PayloadCeiling: 1 << 20,
		SequencerPublicKey: publicKey,
	}, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	return fixtureState{ctx: ctx, store: store, scratch: scratch, signingKey: keyPath, publicKey: publicKey, genesis: genesis, format: format}
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

func TestCreateSubmitReplayVerifyObjectFormats(t *testing.T) {
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

func TestSubmitterReusesExactHeadAndRebuildsAfterExternalAdvance(t *testing.T) {
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
	if submitter.cache.fullScans != 2 {
		t.Fatalf("external advance left full scans at %d, want 2", submitter.cache.fullScans)
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
	if submitter.cache.fullScans != 2 {
		t.Fatalf("CAS loss full scans = %d, want 2", submitter.cache.fullScans)
	}
	verified, err := Verify(f.ctx, f.store, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	if verified.Events != 2 || verified.Head != out.result.Head {
		t.Fatalf("verification = %+v result = %+v", verified, out.result)
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

func TestConcurrentCASProducesOneLinearChain(t *testing.T) {
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

func TestOversizedEnvelopeIsRefusedWithoutPoisoningTheLog(t *testing.T) {
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

func TestVerifierRejectsRebindingAndTrailerMutation(t *testing.T) {
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

	// The original log is valid; replace its trailer in another maliciously
	// sequenced commit and ensure intent-to-envelope consistency catches it.
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
	if _, err := Verify(fA.ctx, fA.store, fA.genesis); err == nil {
		t.Fatal("verifier accepted altered causal trailers")
	}
}

func TestLoadReturnsVerifiedPayloadAndAttachments(t *testing.T) {
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

func TestContinuationBindsVerifiedSealedFrontier(t *testing.T) {
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
	log, err := scanHead(f.ctx, f.store, approved.Genesis, approved.Head, true)
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
