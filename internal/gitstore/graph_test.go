package gitstore

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGraphParsesRootMergeRefsAndTrailers(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	run("init", "-q", "-b", "main", ".")
	if err := os.WriteFile(filepath.Join(root, "a"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "root")
	run("checkout", "-qb", "side")
	run("commit", "-qm", "side work\n\nRests-On: git:sha1:g#git:sha1:e", "--allow-empty")
	run("checkout", "-q", "main")
	run("merge", "-q", "--no-ff", "side", "-m", "merge side")

	gitDir := filepath.Join(root, ".git")
	commits, err := Store{Repo: gitDir}.Graph(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 3 {
		t.Fatalf("expected 3 commits, got %d", len(commits))
	}
	merge, side, first := commits[0], commits[1], commits[2]
	if len(merge.Parents) != 2 || merge.Subject != "merge side" {
		t.Fatalf("merge row = %#v", merge)
	}
	if len(side.RestsOn) != 1 || side.RestsOn[0] != "git:sha1:g#git:sha1:e" {
		t.Fatalf("trailer row = %#v", side)
	}
	if len(first.Parents) != 0 {
		t.Fatalf("root commit has parents: %#v", first)
	}
	found := false
	for _, ref := range merge.Refs {
		if ref == "main" {
			found = true
		}
	}
	if !found {
		t.Fatalf("merge refs = %#v", merge.Refs)
	}
}

// gitRunner builds a throwaway repository and returns its git dir plus a
// runner, so each hostile-metadata case gets a clean history.
func gitRunner(t *testing.T) (string, func(args ...string)) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	run("init", "-q", "-b", "main", ".")
	if err := os.WriteFile(filepath.Join(root, "a"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	return filepath.Join(root, ".git"), run
}

// The defect this framing exists to close. The separators the old format used
// between fields and records were ordinary bytes that git stores verbatim, and
// four of these fields are committer-controlled. A body carrying them forged a
// whole row: on the reproduction that motivated this change, a two-commit
// repository rendered three commits, the extra one bearing a hash, subject,
// author and far-future timestamp of the committer's choosing.
//
// Each case puts hostile bytes in a different committer-controlled field. The
// requirement is not that the bytes survive prettily; it is that they stay
// inside the field they were written in and produce no extra row.
func TestHostileCommitMetadataCannotForgeARailwayRow(t *testing.T) {
	forged := "\x1eFORGEDHASH\x1fPARENT\x1fDECOR\x1fFORGED SUBJECT\x1fEvil Author\x1f9999999999\x1f\x1fforged body"
	for _, test := range []struct {
		name    string
		subject string
		body    string
		author  string
	}{
		{name: "record separator in body", subject: "benign", body: "body" + forged},
		{name: "unit separators in body", subject: "benign", body: "a\x1fb\x1fc\x1fd\x1fe\x1ff\x1fg\x1fh"},
		{name: "separators in subject", subject: "benign" + forged},
		{name: "separators in author name", subject: "benign", author: "Real\x1fFake\x1eOther"},
		{name: "terminal controls and newlines", subject: "benign\x07", body: "line\n\nmore\x1b[2Jcleared\x1e"},
		{name: "separators in a Rests-On trailer", subject: "benign", body: "text\n\nRests-On: git:sha1:g#git:sha1:e\x1fFORGED\x1eROW"},
	} {
		t.Run(test.name, func(t *testing.T) {
			gitDir, run := gitRunner(t)
			run("commit", "-qm", "ordinary commit")
			message := test.subject
			if test.body != "" {
				message += "\n\n" + test.body
			}
			args := []string{"commit", "-q", "--allow-empty", "-m", message}
			if test.author != "" {
				args = append(args, "--author", test.author+" <e@e>")
			}
			run(args...)

			commits, err := Store{Repo: gitDir}.Graph(context.Background(), 60)
			if err != nil {
				t.Fatalf("hostile metadata made Graph fail open-endedly: %v", err)
			}
			if len(commits) != 2 {
				t.Fatalf("git holds 2 commits, railway rendered %d: hostile metadata forged a row", len(commits))
			}
			for _, commit := range commits {
				if len(commit.Hash) != 40 && len(commit.Hash) != 64 {
					t.Errorf("row carries a hash no git object could have: %q", commit.Hash)
				}
				if commit.Author != "t" && commit.Author != "Real\x1fFake\x1eOther" {
					t.Errorf("row carries an unexpected author %q", commit.Author)
				}
				if commit.Time <= 0 {
					t.Errorf("row %q carries a non-positive time %d", commit.Hash, commit.Time)
				}
			}
		})
	}
}

// Honest history must be unaffected: hostile input failing closed is worth
// nothing if ordinary input stopped round-tripping. This pins the exact bytes
// of a body and subject through the new framing.
func TestOrdinaryMetadataStillRoundTrips(t *testing.T) {
	gitDir, run := gitRunner(t)
	run("commit", "-qm", "subject line\n\nbody first paragraph\n\nbody second paragraph\n\nRests-On: git:sha1:g#git:sha1:e")

	commits, err := Store{Repo: gitDir}.Graph(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 1 {
		t.Fatalf("expected 1 commit, got %d", len(commits))
	}
	commit := commits[0]
	if commit.Subject != "subject line" {
		t.Errorf("subject = %q", commit.Subject)
	}
	if want := "body first paragraph\n\nbody second paragraph\n\nRests-On: git:sha1:g#git:sha1:e"; commit.Body != want {
		t.Errorf("body = %q, want %q", commit.Body, want)
	}
	if len(commit.RestsOn) != 1 || commit.RestsOn[0] != "git:sha1:g#git:sha1:e" {
		t.Errorf("trailers = %#v", commit.RestsOn)
	}
}

// The two silent failures the old parser had, now refusals. A short record hit
// len(fields) < 8 and was skipped, so a malformed or truncated commit vanished
// from the railway rather than announcing itself; an unparsable timestamp left
// Time at zero, which sorted that commit to the front - exactly where a hostile
// timestamp would aim it. A view that quietly omits or misplaces history is
// worse than one that refuses to render.
func TestAMalformedStreamIsRefusedRatherThanSkipped(t *testing.T) {
	for _, test := range []struct {
		name   string
		fields []string
		want   string
	}{
		{
			name:   "record short of its field count",
			fields: []string{"a", "b", "c"},
			want:   "not a multiple of",
		},
		{
			name:   "one whole record plus a fragment",
			fields: []string{"h", "p", "d", "s", "a", "1", "", "b", "extra"},
			want:   "not a multiple of",
		},
		{
			name:   "unparsable timestamp",
			fields: []string{"h", "p", "d", "s", "a", "not-a-number", "", "b"},
			want:   "unparsable timestamp",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseGraphStream([]byte(strings.Join(test.fields, "\x00")))
			if err == nil {
				t.Fatalf("malformed stream parsed without complaint; the railway would silently omit or misplace history")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Errorf("error %q does not mention %q", err, test.want)
			}
		})
	}
}

// The empty tail token -z leaves after the final record is dropped, but an
// empty field in the middle is meaningful: a commit with no decorations and no
// trailers has both legitimately, and treating those as absent fields would
// desynchronise the stride the framing depends on.
func TestEmptyFieldsAreKeptAndOnlyTheTrailingTerminatorIsDropped(t *testing.T) {
	stream := strings.Join([]string{"h", "", "", "subject", "author", "42", "", ""}, "\x00") + "\x00"
	commits, err := parseGraphStream([]byte(stream))
	if err != nil {
		t.Fatalf("a record with empty decorations and trailers was refused: %v", err)
	}
	if len(commits) != 1 {
		t.Fatalf("expected 1 commit, got %d", len(commits))
	}
	if commits[0].Subject != "subject" || commits[0].Time != 42 {
		t.Errorf("fields shifted: %#v", commits[0])
	}
	if len(commits[0].Parents) != 0 || len(commits[0].Refs) != 0 || len(commits[0].RestsOn) != 0 {
		t.Errorf("empty fields produced entries: %#v", commits[0])
	}
}

// The hole the NUL framing did not close, and the reason the framing cannot be
// the whole argument. `git commit` refuses a NUL in a log message, so no
// honestly-created commit carries the separator - but `git hash-object
// --literally` writes one anyway, and such an object is reachable from a ref
// and arrives intact through a push. `git fsck --strict` calls it nulInCommit,
// yet `git log -z` still emits a complete, valid-looking record with the body
// silently truncated at the NUL. Before this refusal, Graph returned that row
// and dropped the rest of the metadata without a word.
//
// The object is built with plumbing on purpose. A test that only ever creates
// commits the way an honest committer does can never reach this, which is
// exactly how the gap survived the first round of review.
func TestARawCommitObjectCarryingNULIsRefusedNotTruncated(t *testing.T) {
	ctx := context.Background()
	gitDir, run := gitRunner(t)
	run("commit", "-qm", "base")

	root := filepath.Dir(gitDir)
	capture := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		output, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(output))
	}
	tree := capture("rev-parse", "HEAD^{tree}")
	parent := capture("rev-parse", "HEAD")

	raw := "tree " + tree + "\nparent " + parent +
		"\nauthor Evil <e@e> 1700000000 +0000\ncommitter Evil <e@e> 1700000000 +0000\n\n" +
		"visible subject\n\nSECRET\x00TRUNCATED-AFTER-THIS\n"
	rawPath := filepath.Join(t.TempDir(), "raw")
	if err := os.WriteFile(rawPath, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	oid := capture("hash-object", "--literally", "-t", "commit", "-w", rawPath)
	// Reached through a ref, which is how such an object arrives from a push
	// rather than from anything this process chose to render.
	run("update-ref", "refs/heads/evil", oid)

	_, err := Store{Repo: gitDir}.Graph(ctx, 60)
	if err == nil {
		t.Fatal("Graph rendered a commit git fsck calls nulInCommit, silently truncating its metadata")
	}
	if !strings.Contains(err.Error(), "NUL byte") {
		t.Errorf("refusal does not name the cause: %v", err)
	}
}

// The decoration half of the condition, settled by test rather than asserted.
// Every hostile case above plants bytes in a commit message; none plants a
// hostile ref name, so the claim that decorations are covered rested on
// nothing. Git's own ref-name validation is what covers them, and pinning it
// here means a future git that relaxed the rule would fail this rather than
// quietly widen the parser's exposure.
func TestGitRefusesSeparatorAndControlBytesInRefNames(t *testing.T) {
	ctx := context.Background()
	gitDir, run := gitRunner(t)
	run("commit", "-qm", "base")
	root := filepath.Dir(gitDir)

	// Only bytes that actually reach git. A NUL cannot be put in argv at all -
	// Go's exec refuses it before git starts - so including it here would
	// credit git with a refusal that belongs to the operating system. An
	// invariant broader on paper than in fact is how the porcelain over-claim
	// happened; the NUL case is asserted separately below against the boundary
	// that really enforces it.
	for _, name := range []string{
		"refs/heads/a\x1fb", // unit separator
		"refs/heads/a\x1eb", // record separator
		"refs/heads/a\x07b", // terminal bell
	} {
		cmd := exec.CommandContext(ctx, "git", "-C", root, "update-ref", name, "HEAD")
		if err := cmd.Run(); err == nil {
			t.Errorf("git accepted ref name %q; decorations can carry framing bytes after all", name)
		}
	}
	// And the decorations that do reach Graph still render.
	commits, err := Store{Repo: gitDir}.Graph(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 1 {
		t.Fatalf("expected 1 commit, got %d", len(commits))
	}
}

// The NUL half of the ref-name question, attributed to the boundary that
// actually enforces it. An argument containing NUL never reaches git: the
// operating system's exec interface cannot carry one, and Go reports that
// before spawning. Splitting this from the test above keeps the git invariant
// exactly as wide as git makes it.
func TestANULCannotReachGitThroughArgv(t *testing.T) {
	gitDir, run := gitRunner(t)
	run("commit", "-qm", "base")
	err := exec.CommandContext(context.Background(), "git", "-C", filepath.Dir(gitDir),
		"update-ref", "refs/heads/a\x00b", "HEAD").Run()
	if err == nil {
		t.Fatal("a NUL survived argv; the operating-system boundary is not what this test assumed")
	}
	if !strings.Contains(err.Error(), "NUL") && !strings.Contains(err.Error(), "invalid argument") {
		t.Logf("refused, though not for the expected reason: %v", err)
	}
}

// An absent object is refused rather than answered. This exercises
// AuditBatch.readObject directly, not Graph, and an absent id rather than a
// hash mismatch - the name says so because an earlier one claimed both and did
// neither, which is the failure mode of a test that exists to prove coverage.
//
// Hash mismatch itself is covered by AuditBatch recomputing the object hash;
// an earlier hand-rolled reader here accepted the id 1111... with the payload
// "hello", which actually hashes to 34f5fae8.
func TestReadObjectRefusesAnObjectThatIsAbsent(t *testing.T) {
	ctx := context.Background()
	gitDir, run := gitRunner(t)
	run("commit", "-qm", "base")

	format, err := Store{Repo: gitDir}.ObjectFormat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := Store{Repo: gitDir}.OpenAuditBatch(ctx, format)
	if err != nil {
		t.Fatal(err)
	}
	defer batch.Close()

	// A well-formed object id that names nothing in this repository.
	absent := strings.Repeat("1", len("0000000000000000000000000000000000000000"))
	if format == "sha256" {
		absent = strings.Repeat("1", 64)
	}
	if _, err := batch.readObject(absent, "commit", maxGraphCommitBytes); err == nil {
		t.Fatal("reading an object that is not present returned no error")
	}
}

// Honest history still renders: every refusal above is worth nothing if the
// ordinary path stopped working when the reader changed.
func TestGraphStillRendersAfterObjectVerification(t *testing.T) {
	gitDir, run := gitRunner(t)
	run("commit", "-qm", "one")
	run("commit", "-qm", "two", "--allow-empty")

	commits, err := Store{Repo: gitDir}.Graph(context.Background(), 10)
	if err != nil {
		t.Fatalf("ordinary history was refused: %v", err)
	}
	if len(commits) != 2 {
		t.Fatalf("expected 2 commits, got %d", len(commits))
	}
}

// The exact-syntax cases codex named. strings.Fields, which this parser used
// before, accepts a tab or a run of spaces wherever the protocol specifies one
// space - so a stream that is not the protocol would have been read as though
// it were. These are reachable only because the header check is separable from
// the read loop.
func TestCatFileHeaderMustMatchTheProtocolExactly(t *testing.T) {
	const oid = "1111111111111111111111111111111111111111"
	for _, test := range []struct {
		name   string
		header string
		ok     bool
	}{
		{name: "exact protocol", header: oid + " commit 5\n", ok: true},
		{name: "tab between fields", header: oid + "\tcommit\t5\n"},
		{name: "two spaces between fields", header: oid + "  commit  5\n"},
		{name: "trailing space", header: oid + " commit 5 \n"},
		{name: "different object id", header: "2222222222222222222222222222222222222222 commit 5\n"},
		{name: "wrong type", header: oid + " tag 5\n"},
		{name: "size is not a number", header: oid + " commit five\n"},
		{name: "no fields at all", header: "\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			size, err := parseCatFileHeader(test.header, oid, "commit", 1<<20)
			if test.ok {
				if err != nil {
					t.Fatalf("the exact protocol was refused: %v", err)
				}
				if size != 5 {
					t.Errorf("size = %d, want 5", size)
				}
				return
			}
			if err == nil {
				t.Fatalf("accepted a header that is not the protocol: %q", test.header)
			}
		})
	}
}

// The bound exists so a hostile object cannot choose this process's
// allocation. Without it the declared size is whatever the stream says.
func TestAnOversizedObjectIsRefusedBeforeAllocation(t *testing.T) {
	const oid = "1111111111111111111111111111111111111111"
	if _, err := parseCatFileHeader(oid+" commit 999999999999\n", oid, "commit", maxGraphCommitBytes); err == nil {
		t.Fatal("a size past the bound was accepted, so the reader would allocate whatever the stream asked for")
	}
}

// The condition I narrowed without saying so. A ratified requirement asked for
// surplus RESPONSES to be refused; I delivered refusal of surplus FIELDS
// inside one header and reported it as covering the case. They are different:
// extra words on a header line, versus a whole extra response after the last
// object asked about.
//
// It was genuinely unclosed. Close shut stdin and waited without reading
// stdout, and the caller deferred Close and discarded its error, so trailing
// bytes were never looked at by anything. Close now proves stdout reached EOF
// and Graph propagates that error.
func TestSurplusResponsesAfterTheLastObjectAreRefused(t *testing.T) {
	ctx := context.Background()
	gitDir, run := gitRunner(t)
	run("commit", "-qm", "base")

	format, err := Store{Repo: gitDir}.ObjectFormat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := Store{Repo: gitDir}.OpenAuditBatch(ctx, format)
	if err != nil {
		t.Fatal(err)
	}

	// Queue more responses than will be consumed, so output is still waiting on
	// stdout when Close runs - the shape of a desynchronised reader. Two manual
	// requests plus the one readObject issues for itself means three responses
	// queued and one consumed, not one extra.
	head := strings.TrimSpace(capture(t, ctx, filepath.Dir(gitDir), "rev-parse", "HEAD"))
	if _, err := io.WriteString(batch.stdin, head+"\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(batch.stdin, head+"\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := batch.readObject(head, "commit", maxGraphCommitBytes); err != nil {
		t.Fatalf("reading the first object failed: %v", err)
	}

	if err := batch.Close(); err == nil {
		t.Fatal("Close accepted a stream holding a response nobody asked to read")
	} else if !strings.Contains(err.Error(), "after the last requested object") {
		t.Errorf("refusal does not name the cause: %v", err)
	}
}

func capture(t *testing.T, ctx context.Context, root string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(output)
}

// The propagation itself, which the direct-Close test above cannot reach.
// Graph asks for exactly what it reads, so surplus never arises in its own
// flow; discarding the Close error there broke no test until this existed.
func TestVerifyCommitObjectsPropagatesTheCloseRefusal(t *testing.T) {
	ctx := context.Background()
	gitDir, run := gitRunner(t)
	run("commit", "-qm", "base")

	format, err := Store{Repo: gitDir}.ObjectFormat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := Store{Repo: gitDir}.OpenAuditBatch(ctx, format)
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(capture(t, ctx, filepath.Dir(gitDir), "rev-parse", "HEAD"))
	// One more queued than the caller will read.
	if _, err := io.WriteString(batch.stdin, head+"\n"); err != nil {
		t.Fatal(err)
	}

	err = verifyCommitObjects(batch, []GraphCommit{{Hash: head}})
	if err == nil {
		t.Fatal("the surplus refusal did not reach the caller: Close's error is being discarded")
	}
	if !strings.Contains(err.Error(), "after the last requested object") {
		t.Errorf("error is not the surplus refusal: %v", err)
	}
}

// The deadlock the previous bound would have caused, which no small-response
// test could expose. An earlier Close drained 4096 bytes and then called Wait:
// with an unrequested response larger than the stdout pipe, git stayed blocked
// writing the next byte while Wait waited for it to exit. Neither side could
// move, and these callers' contexts need not carry deadlines, so it hung
// indefinitely.
//
// A blob well past pipe capacity is queued and never read. The bounded context
// is the assertion: if Close ever hangs again this fails rather than stalling
// the suite.
func TestCloseDoesNotDeadlockOnALargeUnreadResponse(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	gitDir, run := gitRunner(t)
	run("commit", "-qm", "base")
	root := filepath.Dir(gitDir)

	// A blob far larger than any pipe buffer, so cat-file cannot finish writing
	// it into a stream nobody is draining.
	big := filepath.Join(t.TempDir(), "big")
	if err := os.WriteFile(big, make([]byte, 4<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	blob := strings.TrimSpace(capture(t, ctx, root, "hash-object", "-w", big))

	format, err := Store{Repo: gitDir}.ObjectFormat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := Store{Repo: gitDir}.OpenAuditBatch(ctx, format)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(batch.stdin, blob+"\n"); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- batch.Close() }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Close accepted a stream holding a response nobody read")
		}
		if !strings.Contains(err.Error(), "after the last requested object") {
			t.Errorf("refusal does not name the cause: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("Close did not return: it is waiting for a process blocked writing into an undrained pipe")
	}
}
