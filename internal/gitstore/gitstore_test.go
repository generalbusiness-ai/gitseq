package gitstore

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRevListMetadataReturnsOrderedCommitIdentity(t *testing.T) {
	ctx := context.Background()
	store, err := InitBare(ctx, filepath.Join(t.TempDir(), "repo.git"), "sha1")
	if err != nil {
		t.Fatal(err)
	}
	tree, err := store.WriteSingleFileTree(ctx, "event", []byte("payload"))
	if err != nil {
		t.Fatal(err)
	}
	env := []string{
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_AUTHOR_DATE=Thu, 07 Apr 2005 22:13:13 +0000",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.invalid", "GIT_COMMITTER_DATE=Thu, 07 Apr 2005 22:13:13 +0000",
	}
	first, err := store.run(ctx, []byte("first\n\nbody\x1e\n"), env, "commit-tree", tree)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.run(ctx, []byte("second\n"), env, "commit-tree", tree, "-p", string(first))
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := store.RevListMetadata(ctx, string(second))
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata) != 2 {
		t.Fatalf("metadata count = %d, want 2", len(metadata))
	}
	if metadata[0].OID != string(first) || metadata[0].Tree != tree || len(metadata[0].Parents) != 0 || metadata[0].Timestamp != 1112911993 || metadata[0].Message != "first\n\nbody\x1e\n" {
		t.Fatalf("first metadata = %+v", metadata[0])
	}
	if metadata[1].OID != string(second) || metadata[1].Tree != tree || len(metadata[1].Parents) != 1 || metadata[1].Parents[0] != string(first) || metadata[1].Timestamp != 1112911993 || metadata[1].Message != "second\n" {
		t.Fatalf("second metadata = %+v", metadata[1])
	}
}

func TestRevListMetadataAfterReturnsExclusiveOrderedSuffix(t *testing.T) {
	ctx := context.Background()
	store, err := InitBare(ctx, filepath.Join(t.TempDir(), "repo.git"), "sha1")
	if err != nil {
		t.Fatal(err)
	}
	tree, err := store.WriteSingleFileTree(ctx, "event", []byte("payload"))
	if err != nil {
		t.Fatal(err)
	}
	env := []string{
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_AUTHOR_DATE=Thu, 07 Apr 2005 22:13:13 +0000",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.invalid", "GIT_COMMITTER_DATE=Thu, 07 Apr 2005 22:13:13 +0000",
	}
	base, err := store.run(ctx, []byte("base\n"), env, "commit-tree", tree)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.run(ctx, []byte("first suffix\n\nmultiline body\x1e\n"), env, "commit-tree", tree, "-p", string(base))
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.run(ctx, []byte("second suffix\n"), env, "commit-tree", tree, "-p", string(first))
	if err != nil {
		t.Fatal(err)
	}

	var metadata []CommitMetadata
	err = store.WalkRevListMetadataAfter(ctx, string(base), string(second), func(commit CommitMetadata) error {
		metadata = append(metadata, commit)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata) != 2 {
		t.Fatalf("metadata count = %d, want 2", len(metadata))
	}
	if metadata[0].OID != string(first) || metadata[0].Tree != tree || len(metadata[0].Parents) != 1 || metadata[0].Parents[0] != string(base) || metadata[0].Timestamp != 1112911993 || metadata[0].Message != "first suffix\n\nmultiline body\x1e\n" {
		t.Fatalf("first suffix metadata = %+v", metadata[0])
	}
	if metadata[1].OID != string(second) || metadata[1].Tree != tree || len(metadata[1].Parents) != 1 || metadata[1].Parents[0] != string(first) || metadata[1].Timestamp != 1112911993 || metadata[1].Message != "second suffix\n" {
		t.Fatalf("second suffix metadata = %+v", metadata[1])
	}
}

func TestRevListMetadataAfterExactHeadIsEmpty(t *testing.T) {
	ctx := context.Background()
	store, err := InitBare(ctx, filepath.Join(t.TempDir(), "repo.git"), "sha1")
	if err != nil {
		t.Fatal(err)
	}
	tree, err := store.WriteSingleFileTree(ctx, "event", []byte("payload"))
	if err != nil {
		t.Fatal(err)
	}
	head, err := store.run(ctx, []byte("only\n"), []string{
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_AUTHOR_DATE=Thu, 07 Apr 2005 22:13:13 +0000",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.invalid", "GIT_COMMITTER_DATE=Thu, 07 Apr 2005 22:13:13 +0000",
	}, "commit-tree", tree)
	if err != nil {
		t.Fatal(err)
	}

	var metadata []CommitMetadata
	err = store.WalkRevListMetadataAfter(ctx, string(head), string(head), func(commit CommitMetadata) error {
		metadata = append(metadata, commit)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if metadata != nil {
		t.Fatalf("metadata = %+v, want nil", metadata)
	}
}

func TestReadCommitMetadataRejectsMalformedFramingAndFields(t *testing.T) {
	tests := []struct {
		name string
		data string
		want string
	}{
		{name: "short record", data: "oid\x00tree\x00", want: "framing"},
		{name: "empty oid", data: "\x00tree\x00\x001\x00message\x00", want: "identity"},
		{name: "empty tree", data: "oid\x00\x00\x001\x00message\x00", want: "identity"},
		{name: "timestamp", data: "oid\x00tree\x00\x00nope\x00message\x00", want: "timestamp"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := readCommitMetadata(bufio.NewReader(strings.NewReader(test.data)))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("readCommitMetadata() error = %v, want %q", err, test.want)
			}
		})
	}
	if _, done, err := readCommitMetadata(bufio.NewReader(strings.NewReader(""))); err != nil || !done {
		t.Fatalf("empty stream = done %v, error %v", done, err)
	}
}

func TestWalkRevListMetadataStopsAndReapsOnVisitorErrorOrCancellation(t *testing.T) {
	ctx := context.Background()
	store, err := InitBare(ctx, filepath.Join(t.TempDir(), "repo.git"), "sha1")
	if err != nil {
		t.Fatal(err)
	}
	tree, err := store.WriteSingleFileTree(ctx, "event", []byte("payload"))
	if err != nil {
		t.Fatal(err)
	}
	env := []string{
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_AUTHOR_DATE=Thu, 07 Apr 2005 22:13:13 +0000",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.invalid", "GIT_COMMITTER_DATE=Thu, 07 Apr 2005 22:13:13 +0000",
	}
	base, err := store.run(ctx, []byte("base\n"), env, "commit-tree", tree)
	if err != nil {
		t.Fatal(err)
	}
	head := string(base)
	for index := 0; index < 32; index++ {
		commit, err := store.run(ctx, []byte(fmt.Sprintf("commit %d\n%s\n", index, strings.Repeat("x", 8192))), env, "commit-tree", tree, "-p", head)
		if err != nil {
			t.Fatal(err)
		}
		head = string(commit)
	}

	t.Run("visitor error", func(t *testing.T) {
		visitErr := errors.New("stop visiting")
		calls := 0
		walkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		err := store.WalkRevListMetadataAfter(walkCtx, string(base), head, func(CommitMetadata) error {
			calls++
			return visitErr
		})
		if !errors.Is(err, visitErr) || calls != 1 {
			t.Fatalf("walk error = %v, calls = %d", err, calls)
		}
	})

	t.Run("context cancellation", func(t *testing.T) {
		walkCtx, cancel := context.WithCancel(ctx)
		calls := 0
		err := store.WalkRevListMetadataAfter(walkCtx, string(base), head, func(CommitMetadata) error {
			calls++
			if calls == 1 {
				cancel()
			}
			return nil
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("walk error = %v, want context cancellation", err)
		}
	})
}

func TestHashPayloadTreeMatchesGitObjectIdentity(t *testing.T) {
	ctx := context.Background()
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			store, err := InitBare(ctx, filepath.Join(t.TempDir(), "repo.git"), format)
			if err != nil {
				t.Fatal(err)
			}
			for _, attachments := range []map[string][]byte{
				nil,
				{"z.txt": []byte("last"), "a.json": []byte(`{"first":true}`)},
			} {
				written, err := store.WritePayloadTree(ctx, []byte("event payload"), attachments)
				if err != nil {
					t.Fatal(err)
				}
				calculated, err := HashPayloadTree(format, []byte("event payload"), attachments)
				if err != nil {
					t.Fatal(err)
				}
				if calculated != written {
					t.Fatalf("calculated tree %s != written tree %s", calculated, written)
				}
			}
		})
	}
}

func TestReadFileLimitRejectsOversizeBlob(t *testing.T) {
	ctx := context.Background()
	store, err := InitBare(ctx, filepath.Join(t.TempDir(), "repo.git"), "sha1")
	if err != nil {
		t.Fatal(err)
	}
	tree, err := store.WriteSingleFileTree(ctx, "checkpoint", []byte("12345"))
	if err != nil {
		t.Fatal(err)
	}
	commit, err := store.run(ctx, []byte("test\n"), []string{
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_AUTHOR_DATE=Thu, 07 Apr 2005 22:13:13 +0000",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.invalid", "GIT_COMMITTER_DATE=Thu, 07 Apr 2005 22:13:13 +0000",
	}, "commit-tree", tree)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadFileLimit(ctx, string(commit), "checkpoint", 4); err == nil {
		t.Fatal("oversize blob was accepted")
	}
	if data, err := store.ReadFileLimit(ctx, string(commit), "checkpoint", 5); err != nil || string(data) != "12345" {
		t.Fatalf("bounded read = %q, %v", data, err)
	}
}

// The resident ownership claim is a compare-and-swap on a ref, so these three
// operations are the whole of its exclusivity. Each is checked here against
// real Git rather than assumed from the manual page.
func TestRefValueSeparatesAbsenceFromFailure(t *testing.T) {
	ctx := context.Background()
	store, err := InitBare(ctx, filepath.Join(t.TempDir(), "repo.git"), "sha1")
	if err != nil {
		t.Fatal(err)
	}
	if value, present, err := store.RefValue(ctx, "refs/gitseq/resident/absent"); err != nil || present || value != "" {
		t.Fatalf("an absent ref reported %q present=%v err=%v", value, present, err)
	}
	blob, err := store.WriteBlob(ctx, []byte(`{"claim":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateRef(ctx, "refs/gitseq/resident/held", blob, ""); err != nil {
		t.Fatal(err)
	}
	if value, present, err := store.RefValue(ctx, "refs/gitseq/resident/held"); err != nil || !present || value != blob {
		t.Fatalf("a held ref reported %q present=%v err=%v", value, present, err)
	}
	if _, _, err := store.RefValue(ctx, "refs/bad name"); err == nil {
		t.Fatal("an invalid ref name was accepted")
	}
}

// Creating with an empty expected value is what makes two fresh starters
// resolve to one, and swapping against the observed value is what stops a
// delayed taker from displacing whoever holds the claim now.
func TestRefUpdatesAreConditionalOnTheObservedValue(t *testing.T) {
	ctx := context.Background()
	store, err := InitBare(ctx, filepath.Join(t.TempDir(), "repo.git"), "sha1")
	if err != nil {
		t.Fatal(err)
	}
	ref := "refs/gitseq/resident/one"
	first, err := store.WriteBlob(ctx, []byte("first"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.WriteBlob(ctx, []byte("second"))
	if err != nil {
		t.Fatal(err)
	}
	third, err := store.WriteBlob(ctx, []byte("third"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateRef(ctx, ref, first, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateRef(ctx, ref, second, ""); err == nil {
		t.Fatal("a create succeeded against an existing ref")
	}
	if err := store.UpdateRef(ctx, ref, second, third); err == nil {
		t.Fatal("a swap succeeded against a value the ref does not hold")
	}
	if err := store.UpdateRef(ctx, ref, second, first); err != nil {
		t.Fatalf("a swap against the observed value failed: %v", err)
	}
	if err := store.DeleteRef(ctx, ref, first); err == nil {
		t.Fatal("a delete succeeded against a stale expected value")
	}
	if err := store.DeleteRef(ctx, ref, ""); err == nil {
		t.Fatal("a delete with no expected value was allowed")
	}
	if err := store.DeleteRef(ctx, ref, second); err != nil {
		t.Fatalf("a delete against the held value failed: %v", err)
	}
	if _, present, err := store.RefValue(ctx, ref); err != nil || present {
		t.Fatalf("the ref survived its delete: present=%v err=%v", present, err)
	}
	if err := store.UpdateRef(ctx, ref, second, first); err == nil {
		t.Fatal("a swap succeeded against a deleted ref")
	}
}

func TestBlobLimitRefusesOversizedAndNonBlobObjects(t *testing.T) {
	ctx := context.Background()
	store, err := InitBare(ctx, filepath.Join(t.TempDir(), "repo.git"), "sha1")
	if err != nil {
		t.Fatal(err)
	}
	blob, err := store.WriteBlob(ctx, []byte("0123456789"))
	if err != nil {
		t.Fatal(err)
	}
	content, err := store.BlobLimit(ctx, blob, 32)
	if err != nil || string(content) != "0123456789" {
		t.Fatalf("read within the limit gave %q, %v", content, err)
	}
	if _, err := store.BlobLimit(ctx, blob, 4); err == nil {
		t.Fatal("a blob over the limit was read")
	}
	tree, err := store.WriteSingleFileTree(ctx, "event", []byte("payload"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.BlobLimit(ctx, tree, 1024); err == nil {
		t.Fatal("a tree was read as a blob")
	}
	if _, err := store.BlobLimit(ctx, "0000000000000000000000000000000000000000", 1024); err == nil {
		t.Fatal("a missing object was read as a blob")
	}
}
