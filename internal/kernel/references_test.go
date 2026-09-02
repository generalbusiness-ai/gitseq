package kernel

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/gitstore"
	"github.com/generalbusiness-ai/gitseq/internal/intent"
)

// A rests_on entry is an opaque string to the kernel, with one exception it can
// check without knowing any application meaning: the canonical event identifier
// names a workroom and a commit, and when the workroom half is this log, the
// string asserts that the commit is a position in this sequence. That assertion
// is either true or false in Git alone.
//
// Admission owes the sequence the true case. A dangling in-log reference that
// sequences cleanly is inherited by every fold and every reader for ever, and no
// later act can repair it, because the log is append-only. These tests are
// written from that requirement, not from what the kernel did before them.
//
// What is deliberately not checked: a reference to another workroom's genesis,
// or any string that is not a canonical identifier at all. Those make no claim
// about this log, so this kernel has nothing to resolve them against and carries
// them unchanged.

func eventID(format, genesis, commit string) string {
	return "git:" + format + ":" + genesis + "#git:" + format + ":" + commit
}

// fabricated is a well-formed object id of the right width that names nothing.
// This is the shape of the defect in the wild: an identifier an author invented
// or mistyped rather than copied from an emitted event.
const fabricated = "0123456789abcdef0123456789abcdef01234567"

func TestSubmitRefusesFabricatedInLogReference(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)

	options := Options{SigningKey: f.signingKey}
	before := mustHead(t, f.store, Ref(f.genesis))

	dangling := eventID(f.format, f.genesis, fabricated)
	_, err := Submit(f.ctx, f.store, f.request(t, private, "dangling", []byte("dangling"), []string{dangling}), options)
	if !errors.Is(err, ErrUnresolvedReference) {
		t.Fatalf("submit with a fabricated in-log reference = %v, want ErrUnresolvedReference", err)
	}
	// The reason must name the reference. A refusal that says only "invalid
	// submission" leaves the author guessing which of several bases was wrong.
	if !strings.Contains(err.Error(), fabricated) {
		t.Fatalf("refusal %q does not name the reference it refused", err)
	}
	if head := mustHead(t, f.store, Ref(f.genesis)); head != before {
		t.Fatalf("refused submission advanced the log: head %s, want %s", head, before)
	}
	events, _, err := Load(f.ctx, f.store, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("refused submission left %d events in the log, want 0", len(events))
	}
}

// The resident path is the one that carries real traffic, and it answers from a
// cached verified frontier rather than a fresh scan. It must refuse the same
// record, and must still be usable afterwards.
func TestSubmitterRefusesFabricatedInLogReference(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	submitter := NewSubmitter(f.store, Options{SigningKey: f.signingKey})

	first, err := submitter.Submit(f.ctx, f.request(t, private, "first", []byte("first"), nil))
	if err != nil {
		t.Fatal(err)
	}
	dangling := eventID(f.format, f.genesis, fabricated)
	if _, err := submitter.Submit(f.ctx, f.request(t, private, "dangling", []byte("dangling"), []string{dangling})); !errors.Is(err, ErrUnresolvedReference) {
		t.Fatalf("resident submit with a fabricated in-log reference = %v, want ErrUnresolvedReference", err)
	}
	resolved := eventID(f.format, f.genesis, first.Commit)
	if _, err := submitter.Submit(f.ctx, f.request(t, private, "second", []byte("second"), []string{resolved})); err != nil {
		t.Fatalf("resident submit after a refusal = %v, want success", err)
	}
}

// Resolvable references admit. The genesis commit is a position in the sequence
// like any other and is the usual first basis in a new workroom, so a rule that
// refused it would refuse every roster record ever written.
func TestSubmitAdmitsResolvedInLogReferences(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	options := Options{SigningKey: f.signingKey}

	seedID := eventID(f.format, f.genesis, f.genesis)
	first, err := Submit(f.ctx, f.store, f.request(t, private, "first", []byte("first"), []string{seedID}), options)
	if err != nil {
		t.Fatalf("submit resting on the genesis event = %v, want success", err)
	}
	firstID := eventID(f.format, f.genesis, first.Commit)

	// Several distinct bases, and the same basis named twice. Duplicates are the
	// author's business, not an admission fault: the kernel resolves the set.
	second, err := Submit(f.ctx, f.store, f.request(t, private, "second", []byte("second"), []string{firstID, seedID, firstID}), options)
	if err != nil {
		t.Fatalf("submit resting on resolvable bases = %v, want success", err)
	}
	if second.Commit == "" {
		t.Fatal("submit returned no commit")
	}
}

// A record cannot rest on itself: its own identifier does not exist until the
// sequencer has admitted it. The refusal is the same one — there is no position
// in the sequence with that name.
func TestSubmitRefusesSelfReference(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	options := Options{SigningKey: f.signingKey}

	first, err := Submit(f.ctx, f.store, f.request(t, private, "first", []byte("first"), nil), options)
	if err != nil {
		t.Fatal(err)
	}
	// Predicting one's own commit id is not possible, so this stands in for it:
	// the head that a self-reference would have to be a descendant of cannot
	// name the record being submitted, and every candidate that is not yet in
	// the log is refused alike. Naming the head's successor position is the
	// closest observable form.
	future, err := f.store.SignedCommit(f.ctx, mustEmptyTree(t, f.store), first.Commit, "gitseq-not-sequenced", f.signingKey, gitstore.CommitIdentity{
		AuthorName: "actor", AuthorEmail: "actor@actor.gitseq.invalid",
		CommitterName: "gitseq sequencer", CommitterEmail: "sequencer@gitseq.invalid",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Submit(f.ctx, f.store, f.request(t, private, "future", []byte("future"), []string{eventID(f.format, f.genesis, future)}), options); !errors.Is(err, ErrUnresolvedReference) {
		t.Fatalf("submit resting on an unsequenced descendant = %v, want ErrUnresolvedReference", err)
	}
}

// Existing as a Git object is not the same as being in the log. An ordinary
// repository commit — a branch tip, a merge head, the commit an artifact cites
// in body.commit — is a real object that names no position in the sequence, and
// citing one as an event identifier is exactly the confusion the reference
// documentation warns about.
func TestSubmitRefusesCommitOutsideTheLog(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)

	outside, err := f.store.SignedCommit(f.ctx, mustEmptyTree(t, f.store), "", "an ordinary repository commit", f.signingKey, gitstore.CommitIdentity{
		AuthorName: "someone", AuthorEmail: "someone@example.invalid",
		CommitterName: "someone", CommitterEmail: "someone@example.invalid",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = Submit(f.ctx, f.store, f.request(t, private, "outside", []byte("outside"), []string{eventID(f.format, f.genesis, outside)}), Options{SigningKey: f.signingKey})
	if !errors.Is(err, ErrUnresolvedReference) {
		t.Fatalf("submit resting on a commit outside the log = %v, want ErrUnresolvedReference", err)
	}
}

// References that make no claim about this log are carried as before. The
// kernel has no way to resolve another workroom's identifier, and no business
// interpreting a string that is not an identifier at all.
func TestSubmitCarriesReferencesItCannotResolve(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)

	rests := []string{
		// another workroom, whose sequence this repository may not even hold
		eventID(f.format, fabricated, fabricated),
		// a different object format, so not this repository's identifier space
		"git:sha256:" + strings.Repeat("ab", 32) + "#git:sha256:" + strings.Repeat("cd", 32),
		// opaque application strings
		"https://example.invalid/pull/7",
		"not-an-identifier",
	}
	result, err := Submit(f.ctx, f.store, f.request(t, private, "opaque", []byte("opaque"), rests), Options{SigningKey: f.signingKey})
	if err != nil {
		t.Fatalf("submit with unresolvable-by-design references = %v, want success", err)
	}
	events, _, err := Load(f.ctx, f.store, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	if last.Commit != result.Commit {
		t.Fatalf("loaded head %s, want %s", last.Commit, result.Commit)
	}
	if !intent.EqualRefs(last.Intent.RestsOn, rests) {
		t.Fatalf("carried references = %v, want %v", last.Intent.RestsOn, rests)
	}
}

// Admission is a gate on what may be appended, not a new condition on what has
// already been appended. Records with dangling in-log references were sequenced
// before this check existed; they are history, and a verifier that refused them
// now would make the whole existing log unreadable rather than repair anything.
func TestVerifyAcceptsHistoricalDanglingReference(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)

	valid, err := Submit(f.ctx, f.store, f.request(t, private, "valid", []byte("valid"), nil), Options{SigningKey: f.signingKey})
	if err != nil {
		t.Fatal(err)
	}

	// Append a properly signed event carrying a dangling in-log reference, the
	// way the sequencer would have before admission checked it.
	dangling := []string{eventID(f.format, f.genesis, fabricated)}
	request := f.request(t, private, "historical", []byte("historical"), dangling)
	tree, err := f.store.WritePayloadTree(f.ctx, request.Payload, request.Attachments)
	if err != nil {
		t.Fatal(err)
	}
	historical, err := f.store.SignedCommit(f.ctx, tree, valid.Head, intent.Envelope(request.Signed, dangling), f.signingKey, gitstore.CommitIdentity{
		AuthorName: "actor", AuthorEmail: "actor@actor.gitseq.invalid",
		CommitterName: "gitseq sequencer", CommitterEmail: "sequencer@gitseq.invalid",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpdateRef(f.ctx, Ref(f.genesis), historical, valid.Head); err != nil {
		t.Fatal(err)
	}

	verification, err := Verify(f.ctx, f.store, f.genesis)
	if err != nil {
		t.Fatalf("verify over a historical dangling reference = %v, want success", err)
	}
	if verification.Head != historical {
		t.Fatalf("verified head = %s, want %s", verification.Head, historical)
	}
	events, _, err := Load(f.ctx, f.store, f.genesis)
	if err != nil {
		t.Fatalf("load over a historical dangling reference = %v, want success", err)
	}
	if !intent.EqualRefs(events[len(events)-1].Intent.RestsOn, dangling) {
		t.Fatalf("historical references = %v, want %v", events[len(events)-1].Intent.RestsOn, dangling)
	}

	// An exact retry of the historical record replays it rather than refusing
	// it: admission holds only a new submission to resolvability, and the log
	// already carries this one.
	retry, err := Submit(f.ctx, f.store, request, Options{SigningKey: f.signingKey})
	if err != nil {
		t.Fatalf("retry of a historical dangling record = %v, want replay", err)
	}
	if !retry.Replay || retry.Commit != historical {
		t.Fatalf("retry = %+v, want replay of %s", retry, historical)
	}

	// And the log stays usable: the next record may rest on the historical one,
	// which is now a real position, whatever its own bases said.
	if _, err := Submit(f.ctx, f.store, f.request(t, private, "after", []byte("after"), []string{eventID(f.format, f.genesis, historical)}), Options{SigningKey: f.signingKey}); err != nil {
		t.Fatalf("submit after a historical dangling reference = %v, want success", err)
	}
}

func mustEmptyTree(t testing.TB, store gitstore.Store) string {
	t.Helper()
	tree, err := store.EmptyTree(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return tree
}
