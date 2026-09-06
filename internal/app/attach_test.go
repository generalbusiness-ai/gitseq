package app

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/apphost"
	"github.com/generalbusiness-ai/gitseq/internal/gitstore"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

func attachTestGit(t *testing.T, repo string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repo}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func TestAttachCheckpointWriteFailurePreservesMemoryAndCanRetry(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires ordinary POSIX directory write permissions")
	}
	for _, newerAppend := range []bool{false, true} {
		name := "retry imported head"
		if newerAppend {
			name = "restart with a newer append"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			source, seed, err := Init(ctx, testRepo(t), "operator", 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			genesis := source.View().Genesis
			ref := kernel.Ref(genesis)
			trusted := attachTestGit(t, source.Repo, "rev-parse", ref)
			var heads []string
			for _, text := range []string{"imported", "newer append"} {
				actRecord(t, ctx, source, "operator", Act{Verb: VerbState, Kind: workroom.KindAssert, Text: text, RestsOn: []string{seed.ID}, IdempotencyKey: text})
				heads = append(heads, attachTestGit(t, source.Repo, "rev-parse", ref))
			}
			repo := testRepo(t)
			attachTestGit(t, repo, "fetch", source.Repo, ref+":refs/remotes/source/sequence")
			if _, err := AttachSequence(ctx, repo, genesis, "sha1", trusted, ""); err != nil {
				t.Fatal(err)
			}
			_, commonDir, err := apphost.ResolveGitDirs(ctx, repo)
			if err != nil {
				t.Fatal(err)
			}
			store := gitstore.Store{Repo: commonDir}
			meta := apphost.MetaDir(commonDir)
			configPath := filepath.Join(meta, apphost.ConfigFile)
			before, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			previousGate := attachCheckpointGate
			t.Cleanup(func() {
				attachCheckpointGate = previousGate
				_ = os.Chmod(meta, 0o700)
			})
			gateRan := false
			attachCheckpointGate = func() {
				gateRan = true
				if newerAppend {
					if err := store.UpdateRef(ctx, ref, heads[1], heads[0]); err != nil {
						t.Fatalf("inject append after import CAS: %v", err)
					}
				}
				if err := os.Chmod(meta, 0o500); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := AttachSequence(ctx, repo, genesis, "sha1", heads[0], trusted); err == nil || !strings.Contains(err.Error(), "local rollback witness could not advance") {
				t.Fatalf("checkpoint failure was not reported: %v", err)
			}
			if !gateRan {
				t.Fatal("checkpoint write failure control did not run")
			}
			attachCheckpointGate = previousGate
			if err := os.Chmod(meta, 0o700); err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(configPath)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("failed checkpoint write erased prior memory: %v", err)
			}
			want := heads[0]
			if newerAppend {
				want = heads[1]
			}
			if got := attachTestGit(t, repo, "rev-parse", ref); got != want {
				t.Fatalf("failure recovery rewound the sequence: got %s want %s", got, want)
			}
			if newerAppend {
				if _, err := AttachSequence(ctx, repo, genesis, "sha1", heads[0], want); err == nil {
					t.Fatal("retry with an older remote head overwrote a newer append")
				}
				// Reopening and explicitly auditing the actual local head is
				// sufficient; no saved witness or canonical ref is reset.
				reopened, err := Open(ctx, repo)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := reopened.Verify(ctx); err != nil {
					t.Fatalf("restart audit after restoring write access: %v", err)
				}
			} else if _, err := AttachSequence(ctx, repo, genesis, "sha1", want, want); err != nil {
				t.Fatalf("retry after restoring write access: %v", err)
			}
			config, err := apphost.LoadConfig(meta)
			if err != nil || config.VerifiedFrontier == nil || config.VerifiedFrontier.Head != want {
				t.Fatalf("recovery did not persist the actual head: %+v, %v", config.VerifiedFrontier, err)
			}
		})
	}
}

func TestAttachSequencePreservesTrustedStateOnRefusal(t *testing.T) {
	// No parallel subtests: the gate deliberately controls the import window.
	for _, mode := range []string{"rewind", "sibling", "unsigned history", "concurrent append", "checkpoint ahead", "writable custody"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			source, seed, err := Init(ctx, testRepo(t), "operator", 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			genesis := source.View().Genesis
			ref := kernel.Ref(genesis)
			base := attachTestGit(t, source.Repo, "rev-parse", ref)
			appendEvent := func(text string) string {
				t.Helper()
				actRecord(t, ctx, source, "operator", Act{Verb: VerbState, Kind: workroom.KindAssert, Text: text, RestsOn: []string{seed.ID}, IdempotencyKey: text})
				return attachTestGit(t, source.Repo, "rev-parse", ref)
			}
			trusted := appendEvent("trusted local position")
			candidate := appendEvent("remote next position")
			concurrent := appendEvent("concurrent local append")
			wantRef := trusted
			switch mode {
			case "rewind":
				candidate = base
			case "sibling":
				_, private, err := source.Actor("operator")
				if err != nil {
					t.Fatal(err)
				}
				request, err := source.BuildActRequest(ctx, private, "operator", Act{Verb: VerbState, Kind: workroom.KindAssert, Text: "sibling", RestsOn: []string{seed.ID}, IdempotencyKey: "sibling"})
				if err != nil {
					t.Fatal(err)
				}
				if err := source.Store.UpdateRef(ctx, ref, base, concurrent); err != nil {
					t.Fatal(err)
				}
				result, err := kernel.Submit(ctx, source.Store, request, kernel.Options{SigningKey: source.View().SequencerKey})
				if err != nil {
					t.Fatal(err)
				}
				candidate = result.Head
				if _, err := kernel.VerifyAt(ctx, source.Store, genesis, candidate); err != nil {
					t.Fatalf("sibling control must be internally valid: %v", err)
				}
			case "unsigned history":
				tree := attachTestGit(t, source.Repo, "rev-parse", candidate+"^{tree}")
				candidate = attachTestGit(t, source.Repo, "-c", "commit.gpgsign=false", "-c", "user.name=test", "-c", "user.email=test@example.invalid", "commit-tree", tree, "-p", trusted, "-m", "unsigned candidate")
			case "concurrent append":
				wantRef = concurrent
			}
			// Transport every control through separate refs; none is admitted
			// just to give the verifier access to its objects.
			attachTestGit(t, source.Repo, "update-ref", "refs/test/candidate", candidate)
			attachTestGit(t, source.Repo, "update-ref", "refs/test/trusted", trusted)
			attachTestGit(t, source.Repo, "update-ref", "refs/test/concurrent", concurrent)
			repo := testRepo(t)
			attachTestGit(t, repo, "fetch", source.Repo, "refs/test/*:refs/remotes/source/*")
			if _, err := AttachSequence(ctx, repo, genesis, "sha1", trusted, ""); err != nil {
				t.Fatalf("initial attachment: %v", err)
			}
			_, commonDir, err := apphost.ResolveGitDirs(ctx, repo)
			if err != nil {
				t.Fatal(err)
			}
			store := gitstore.Store{Repo: commonDir}
			meta := apphost.MetaDir(commonDir)
			config, err := apphost.LoadConfig(meta)
			if err != nil {
				t.Fatal(err)
			}
			if !config.ReadOnly || config.SequencerKey != "" || len(config.Actors) != 0 {
				t.Fatalf("attachment created signing custody: %+v", config)
			}
			if mode == "checkpoint ahead" {
				verified, err := kernel.VerifyAt(ctx, store, genesis, concurrent)
				if err != nil {
					t.Fatal(err)
				}
				config.VerifiedFrontier = &apphost.VerifiedFrontier{Head: concurrent, Depth: verified.Depth}
			} else if mode == "writable custody" {
				config.ReadOnly = false
				config.SequencerKey = "existing-test-custody"
			}
			if err := apphost.SaveConfig(meta, config); err != nil {
				t.Fatal(err)
			}
			configPath := filepath.Join(meta, apphost.ConfigFile)
			before, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			gateRan := false
			oldGate := attachImportGate
			t.Cleanup(func() { attachImportGate = oldGate })
			if mode == "concurrent append" {
				attachImportGate = func() {
					gateRan = true
					if err := store.UpdateRef(ctx, ref, concurrent, trusted); err != nil {
						t.Fatalf("failed to inject concurrent append: %v", err)
					}
				}
			}
			if _, err := AttachSequence(ctx, repo, genesis, "sha1", candidate, trusted); err == nil {
				t.Fatal("invalid import succeeded")
			}
			if mode == "concurrent append" && !gateRan {
				t.Fatal("concurrent append control never reached the CAS window")
			}
			if got := attachTestGit(t, repo, "rev-parse", ref); got != wantRef {
				t.Fatalf("refusal changed authoritative ref: got %s want %s", got, wantRef)
			}
			after, err := os.ReadFile(configPath)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("refusal changed saved custody/checkpoint: %v", err)
			}
		})
	}
}
