package gitstore

import (
	"context"
	"path/filepath"
	"testing"
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
