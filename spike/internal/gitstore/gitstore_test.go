package gitstore

import (
	"context"
	"path/filepath"
	"testing"
)

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
