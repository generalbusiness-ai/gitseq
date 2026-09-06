package gitstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreviewImmutableObjectsAndBounds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			store, err := InitBare(ctx, filepath.Join(t.TempDir(), "repo.git"), format)
			if err != nil {
				t.Fatal(err)
			}
			run := func(input []byte, args ...string) string {
				t.Helper()
				out, err := store.run(ctx, input, nil, args...)
				if err != nil {
					t.Fatal(err)
				}
				return strings.TrimSpace(string(out))
			}
			blob := run([]byte("exact source\n"), "hash-object", "-w", "--stdin")
			large := run(bytes.Repeat([]byte("x"), PreviewContentLimit+1), "hash-object", "-w", "--stdin")
			tree := run([]byte(fmt.Sprintf("100644 blob %s\tsource.go\n100644 blob %s\tlarge.txt\n120000 blob %s\tlink\n", blob, large, blob)), "mktree")
			commit := run([]byte("tree "+tree+"\nauthor Test <test@example.invalid> 1 +0000\ncommitter Test <test@example.invalid> 1 +0000\n\nsource\n"), "hash-object", "-t", "commit", "-w", "--stdin")
			got, err := store.Preview(ctx, format, commit, "source.go")
			if err != nil || string(got.Content) != "exact source\n" {
				t.Fatalf("source: %+v %v", got, err)
			}
			for _, tc := range []struct {
				path string
				want error
			}{{"absent.go", ErrPreviewMissing}, {"link", ErrPreviewType}, {"large.txt", ErrPreviewLimit}} {
				_, err := store.Preview(ctx, format, commit, tc.path)
				if !errors.Is(err, tc.want) {
					t.Errorf("%s: %v", tc.path, err)
				}
			}
			for _, path := range []string{"../source.go", "a/../source.go", "/source.go", "a//b", ".", "a\\b", "HEAD:source.go", "a\x00b", strings.Repeat("a/", 33) + "x"} {
				if _, err := store.Preview(ctx, format, commit, path); err == nil {
					t.Errorf("accepted path %q", path)
				}
			}
			for _, head := range []string{"HEAD", commit[:12], commit + ":source.go", "--help", strings.ToUpper(commit)} {
				if _, err := store.Preview(ctx, format, head, "source.go"); err == nil {
					t.Errorf("accepted revision %q", head)
				}
			}
			replacement := run([]byte("wrong replacement"), "hash-object", "-w", "--stdin")
			run(nil, "update-ref", "refs/replace/"+blob, replacement)
			got, err = store.Preview(ctx, format, commit, "source.go")
			if err != nil || string(got.Content) != "exact source\n" {
				t.Fatalf("replacement changed preview: %+v %v", got, err)
			}
			var names strings.Builder
			for i := 0; i < PreviewEntryLimit+3; i++ {
				fmt.Fprintf(&names, "100644 blob %s\tfile%03d.txt\n", blob, i)
			}
			child := run([]byte(names.String()), "mktree")
			root := run([]byte(fmt.Sprintf("040000 tree %s\tdirectory\n160000 commit %s\tsubmodule\n", child, commit)), "mktree")
			directoryCommit := run([]byte("tree "+root+"\nauthor Test <test@example.invalid> 1 +0000\ncommitter Test <test@example.invalid> 1 +0000\n\nlisting\n"), "hash-object", "-t", "commit", "-w", "--stdin")
			got, err = store.Preview(ctx, format, directoryCommit, "directory")
			if err != nil || !got.Directory || len(got.Entries) != PreviewEntryLimit || got.Omitted != 3 {
				t.Fatalf("listing: %+v %v", got, err)
			}
			if _, err := store.Preview(ctx, format, directoryCommit, "submodule"); !errors.Is(err, ErrPreviewType) {
				t.Fatalf("submodule: %v", err)
			}
		})
	}
}
