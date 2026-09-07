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
			// The read budget is a fixed 4 MiB contract, so the boundary
			// fixtures are literal sizes rather than derived from the
			// constant: a raised constant must fail here, not move the test.
			atBudget := run(bytes.Repeat([]byte("x"), 4194304), "hash-object", "-w", "--stdin")
			huge := run(bytes.Repeat([]byte("x"), 4194305), "hash-object", "-w", "--stdin")
			tree := run([]byte(fmt.Sprintf("100644 blob %s\tsource.go\n100644 blob %s\tlarge.txt\n100644 blob %s\tatbudget.txt\n100644 blob %s\thuge.txt\n120000 blob %s\tlink\n", blob, large, atBudget, huge, blob)), "mktree")
			commit := run([]byte("tree "+tree+"\nauthor Test <test@example.invalid> 1 +0000\ncommitter Test <test@example.invalid> 1 +0000\n\nsource\n"), "hash-object", "-t", "commit", "-w", "--stdin")
			got, err := store.Preview(ctx, format, commit, "source.go")
			if err != nil || string(got.Content) != "exact source\n" {
				t.Fatalf("source: %+v %v", got, err)
			}
			// A file above the old whole-file ceiling is read and verified in
			// full within the read budget; one above the budget is refused
			// before any content is returned.
			if got, err := store.Preview(ctx, format, commit, "large.txt"); err != nil || len(got.Content) != PreviewContentLimit+1 {
				t.Fatalf("large within budget: %d %v", len(got.Content), err)
			}
			if PreviewReadBudget != 4194304 {
				t.Fatalf("read budget is %d, not the documented 4 MiB", PreviewReadBudget)
			}
			if got, err := store.Preview(ctx, format, commit, "atbudget.txt"); err != nil || len(got.Content) != 4194304 {
				t.Fatalf("exactly 4 MiB must read: %d %v", len(got.Content), err)
			}
			for _, tc := range []struct {
				path string
				want error
			}{{"absent.go", ErrPreviewMissing}, {"link", ErrPreviewType}, {"huge.txt", ErrPreviewLimit}} {
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
