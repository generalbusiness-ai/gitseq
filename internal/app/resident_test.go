package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func residentWorkspace(t *testing.T) *Workspace {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	workspace, _, err := Init(context.Background(), repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	return workspace
}

func TestResidentAddressIsPublishedBesideTheWorkroomAndWithdrawn(t *testing.T) {
	workspace := residentWorkspace(t)
	if url, ok := workspace.ResidentURL(); ok {
		t.Fatalf("a repository with no service published %q", url)
	}
	withdraw, err := workspace.PublishResident("http://127.0.0.1:7788/")
	if err != nil {
		t.Fatal(err)
	}
	url, ok := workspace.ResidentURL()
	if !ok || url != "http://127.0.0.1:7788" {
		t.Fatalf("published address was not found: %q ok=%v", url, ok)
	}
	withdraw()
	if url, ok := workspace.ResidentURL(); ok {
		t.Fatalf("withdrawn address is still advertised: %q", url)
	}
}

// A URL cannot say which workroom answers there, so the genesis travels with
// it. Trusting a record left by a workroom this repository no longer has would
// append the repository's acts to another log.
func TestResidentAddressForAnotherWorkroomIsRefused(t *testing.T) {
	workspace := residentWorkspace(t)
	if _, err := workspace.PublishResident("http://127.0.0.1:7788"); err != nil {
		t.Fatal(err)
	}
	content, err := json.Marshal(Resident{URL: "http://127.0.0.1:7788", Genesis: "git:sha1:0000000000000000000000000000000000000000", PID: os.Getpid()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace.MetaDir, residentFile), content, 0o600); err != nil {
		t.Fatal(err)
	}
	if url, ok := workspace.ResidentURL(); ok {
		t.Fatalf("a service holding another workroom was accepted: %q", url)
	}
}

// Withdrawal belongs to the process that published. A service that took the
// repository over is still serving it, and clearing its record would send
// clients into degraded mode for no reason.
func TestWithdrawalLeavesALaterServiceAdvertised(t *testing.T) {
	workspace := residentWorkspace(t)
	withdraw, err := workspace.PublishResident("http://127.0.0.1:7788")
	if err != nil {
		t.Fatal(err)
	}
	successor, err := json.Marshal(Resident{URL: "http://127.0.0.1:7799", Genesis: workspace.Config.Genesis, PID: os.Getpid() + 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace.MetaDir, residentFile), successor, 0o600); err != nil {
		t.Fatal(err)
	}
	withdraw()
	if url, ok := workspace.ResidentURL(); !ok || url != "http://127.0.0.1:7799" {
		t.Fatalf("the successor's address was withdrawn: %q ok=%v", url, ok)
	}
}

// The ownership claim is what stops two residents from serving one repository.
// Every test below drives one interleaving of the acquire/probe/take/release
// protocol exactly, by supplying the probe rather than a network: liveness is
// the only part of the protocol that is not a compare-and-swap, so replacing it
// is what makes the races reproducible instead of hopeful.

// probeSaying answers the same way about every claim.
func probeSaying(answer Liveness) Prober {
	return func(context.Context, ResidentClaim) Liveness { return answer }
}

// probeDeadOnly is what an honest probe does once one specific process has
// died: that claim's address refuses a dial, and anything else answers.
func probeDeadOnly(nonce string) Prober {
	return func(_ context.Context, claim ResidentClaim) Liveness {
		if claim.Nonce == nonce {
			return Dead
		}
		return Alive
	}
}

func claimRef(t *testing.T, workspace *Workspace) (string, bool) {
	t.Helper()
	value, present, err := workspace.Store.RefValue(context.Background(), ResidentRef(workspace.Config.Genesis))
	if err != nil {
		t.Fatal(err)
	}
	return value, present
}

func mustClaim(t *testing.T, workspace *Workspace, url string, probe Prober) *ResidentOwnership {
	t.Helper()
	ownership, err := workspace.ClaimResident(context.Background(), url, probe)
	if err != nil {
		t.Fatalf("claim %s: %v", url, err)
	}
	return ownership
}

// Two starters, one repository, one winner. The loser probes the winner, finds
// it answering, and refuses; it never becomes a second resident.
func TestOnlyOneOfManySimultaneousStartersOwnsTheResident(t *testing.T) {
	workspace := residentWorkspace(t)
	const starters = 12
	var owners atomic.Int64
	var refusals atomic.Int64
	var group sync.WaitGroup
	for i := 0; i < starters; i++ {
		group.Add(1)
		go func(port int) {
			defer group.Done()
			ownership, err := workspace.ClaimResident(context.Background(),
				fmt.Sprintf("http://127.0.0.1:%d", 7800+port), probeSaying(Alive))
			if err != nil {
				refusals.Add(1)
				return
			}
			if ownership.Claim().Nonce == "" {
				t.Errorf("ownership carries no nonce")
			}
			owners.Add(1)
		}(i)
	}
	group.Wait()
	if owners.Load() != 1 {
		t.Fatalf("%d starters owned the repository; exactly one may", owners.Load())
	}
	if refusals.Load() != starters-1 {
		t.Fatalf("%d starters refused; %d should have", refusals.Load(), starters-1)
	}
	if _, present := claimRef(t, workspace); !present {
		t.Fatal("the winner left no claim behind")
	}
}

// The refusal names the incumbent, because an operator reading it needs to know
// which service to stop rather than that something went wrong.
func TestASecondStarterRefusesAndNamesTheIncumbent(t *testing.T) {
	workspace := residentWorkspace(t)
	mustClaim(t, workspace, "http://127.0.0.1:7801", probeSaying(Alive))
	_, err := workspace.ClaimResident(context.Background(), "http://127.0.0.1:7802", probeSaying(Alive))
	var held *ResidentHeldError
	if !errors.As(err, &held) {
		t.Fatalf("a second starter beside a live resident got %v", err)
	}
	if held.URL != "http://127.0.0.1:7801" {
		t.Fatalf("refusal named %q, not the incumbent", held.URL)
	}
}

// A claim that cannot be read is a refusal, never a vacancy. The probe here
// says Dead about everything, which is the most permissive answer available:
// even that must not turn an unreadable claim into an opening, because the
// alternative is a corrupt file silently authorizing a second resident.
func TestAnUnreadableClaimIsRefusedRatherThanTreatedAsVacant(t *testing.T) {
	ctx := context.Background()
	for name, content := range map[string][]byte{
		"not json":         []byte("this is not a claim"),
		"truncated":        []byte(`{"genesis":"abc","url":"http://127.0.0.1:7801"`),
		"missing fields":   []byte(`{}`),
		"no nonce":         []byte(`{"genesis":"abc","url":"http://127.0.0.1:7801","pid":1}`),
		"oversized":        append([]byte(`{"note":"`), append(bytes.Repeat([]byte("x"), claimLimit+1), []byte(`"}`)...)...),
		"another workroom": []byte(`{"genesis":"0000000000000000000000000000000000000000","url":"http://127.0.0.1:7801","nonce":"aa","pid":1}`),
	} {
		t.Run(name, func(t *testing.T) {
			workspace := residentWorkspace(t)
			ref := ResidentRef(workspace.Config.Genesis)
			blob, err := workspace.Store.WriteBlob(ctx, content)
			if err != nil {
				t.Fatal(err)
			}
			if err := workspace.Store.UpdateRef(ctx, ref, blob, ""); err != nil {
				t.Fatal(err)
			}
			if _, err := workspace.ClaimResident(ctx, "http://127.0.0.1:7802", probeSaying(Dead)); err == nil {
				t.Fatal("an unreadable claim was taken as a vacancy")
			}
			if value, present := claimRef(t, workspace); !present || value != blob {
				t.Fatalf("the refused claim was disturbed: %q present=%v", value, present)
			}
		})
	}
}

// Stale-owner recovery. Only a definitive negative authorizes the transfer, and
// then the claim changes hands in one compare-and-swap.
func TestAStaleClaimIsTakenOverAfterADefinitiveNegative(t *testing.T) {
	workspace := residentWorkspace(t)
	dead := mustClaim(t, workspace, "http://127.0.0.1:7801", probeSaying(Alive))
	before, _ := claimRef(t, workspace)
	successor := mustClaim(t, workspace, "http://127.0.0.1:7802", probeDeadOnly(dead.Claim().Nonce))
	after, present := claimRef(t, workspace)
	if !present || after == before {
		t.Fatalf("the stale claim was not transferred: before=%s after=%s present=%v", before, after, present)
	}
	if successor.Claim().URL != "http://127.0.0.1:7802" {
		t.Fatalf("the successor claim names %q", successor.Claim().URL)
	}
}

// A hung incumbent that accepts connections and never answers, a timeout, and
// an unparseable reply all arrive here as Ambiguous. None of them may take the
// repository: not shown to be gone is not gone.
func TestAnAmbiguousProbeLeavesTheIncumbentClaimAlone(t *testing.T) {
	workspace := residentWorkspace(t)
	mustClaim(t, workspace, "http://127.0.0.1:7801", probeSaying(Alive))
	before, _ := claimRef(t, workspace)
	if _, err := workspace.ClaimResident(context.Background(), "http://127.0.0.1:7802", probeSaying(Ambiguous)); err == nil {
		t.Fatal("an ambiguous probe authorized a takeover")
	}
	if after, present := claimRef(t, workspace); !present || after != before {
		t.Fatalf("an ambiguous probe moved the claim: before=%s after=%s", before, after)
	}
}

// Two takers observe one dead claim. Exactly one wins the swap; the other finds
// the winner in place, probes it alive, and refuses.
func TestTwoTakersRacingOneStaleClaimYieldOneOwner(t *testing.T) {
	workspace := residentWorkspace(t)
	dead := mustClaim(t, workspace, "http://127.0.0.1:7801", probeSaying(Alive))
	probe := probeDeadOnly(dead.Claim().Nonce)
	var owners atomic.Int64
	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func(port int) {
			defer group.Done()
			if _, err := workspace.ClaimResident(context.Background(),
				fmt.Sprintf("http://127.0.0.1:%d", 7810+port), probe); err == nil {
				owners.Add(1)
			}
		}(i)
	}
	group.Wait()
	if owners.Load() != 1 {
		t.Fatalf("%d takers took one stale claim; exactly one may", owners.Load())
	}
}

// This is the interleaving that defeated the pathname protocol, and the reason
// ownership is a compare-and-swap on a value rather than a rename.
//
// Taker B reads the dead claim A and probes it. While B is between the read and
// the swap, A's claim is released and a new owner C takes the repository. B
// then attempts its swap. Under a rename keyed only by the pathname, B would
// move C's live claim away and serve alongside it. Here B's swap names the
// exact object it read, so the swap fails, B contests again, finds C answering,
// and refuses.
//
// The pause is driven from inside the probe rather than by timing, so the
// ordering is exact on every run.
func TestADelayedTakerCannotDisplaceTheNewOwner(t *testing.T) {
	ctx := context.Background()
	workspace := residentWorkspace(t)
	original := mustClaim(t, workspace, "http://127.0.0.1:7801", probeSaying(Alive))

	var successor *ResidentOwnership
	var displaced sync.Once
	delayed := func(_ context.Context, claim ResidentClaim) Liveness {
		if claim.Nonce != original.Claim().Nonce {
			// B has contested again and is looking at whoever holds the claim
			// now. That process is serving.
			return Alive
		}
		// B has read the original claim and is about to swap. Move the world
		// underneath it first: the original owner leaves and a new one takes
		// the repository.
		displaced.Do(func() {
			original.Release(ctx)
			successor = mustClaim(t, workspace, "http://127.0.0.1:7803", probeSaying(Alive))
		})
		return Dead
	}

	_, err := workspace.ClaimResident(ctx, "http://127.0.0.1:7802", delayed)
	if err == nil {
		t.Fatal("a delayed taker displaced the new owner and would have served beside it")
	}
	var held *ResidentHeldError
	if !errors.As(err, &held) || held.URL != "http://127.0.0.1:7803" {
		t.Fatalf("the delayed taker refused for the wrong reason: %v", err)
	}
	value, present := claimRef(t, workspace)
	if !present {
		t.Fatal("the new owner's claim was removed")
	}
	if successor == nil || value != successor.blob {
		t.Fatalf("the claim is %s, not the new owner's", value)
	}
}

// A departing process withdraws only its own claim. Whichever way the takeover
// and the shutdown interleave, one owner is left holding the repository.
func TestATakerRacingACleanShutdownYieldsOneOwnerInEitherOrder(t *testing.T) {
	ctx := context.Background()

	t.Run("takeover first", func(t *testing.T) {
		workspace := residentWorkspace(t)
		departing := mustClaim(t, workspace, "http://127.0.0.1:7801", probeSaying(Alive))
		successor := mustClaim(t, workspace, "http://127.0.0.1:7802", probeDeadOnly(departing.Claim().Nonce))
		departing.Release(ctx)
		value, present := claimRef(t, workspace)
		if !present || value != successor.blob {
			t.Fatalf("a departing process removed its successor's claim: %q present=%v", value, present)
		}
	})

	t.Run("release first", func(t *testing.T) {
		workspace := residentWorkspace(t)
		departing := mustClaim(t, workspace, "http://127.0.0.1:7801", probeSaying(Alive))
		departing.Release(ctx)
		if _, present := claimRef(t, workspace); present {
			t.Fatal("release left the claim behind")
		}
		successor := mustClaim(t, workspace, "http://127.0.0.1:7802", probeSaying(Ambiguous))
		value, present := claimRef(t, workspace)
		if !present || value != successor.blob {
			t.Fatalf("the newcomer did not take a free claim: %q present=%v", value, present)
		}
	})
}

// Every acquisition mints a new nonce, so no object id this protocol compares
// against can ever come back. That is what keeps a value compare-and-swap free
// of A-B-A.
func TestEveryAcquisitionCarriesAFreshNonce(t *testing.T) {
	ctx := context.Background()
	workspace := residentWorkspace(t)
	seen := map[string]bool{}
	for i := 0; i < 5; i++ {
		ownership := mustClaim(t, workspace, "http://127.0.0.1:7801", probeSaying(Ambiguous))
		nonce := ownership.Claim().Nonce
		if len(nonce) != 32 {
			t.Fatalf("nonce %q is not sixteen bytes of hex", nonce)
		}
		if seen[nonce] {
			t.Fatalf("nonce %q was reused between acquisitions", nonce)
		}
		seen[nonce] = true
		ownership.Release(ctx)
	}
}

// One repository is one claim however it is named, and two repositories never
// contend. The claim is a shared ref in the common directory, so a path alias,
// a symlink, and a linked worktree all reach the same one.
func TestAliasesSymlinksAndLinkedWorktreesContendForOneClaim(t *testing.T) {
	ctx := context.Background()
	workspace := residentWorkspace(t)
	owner := mustClaim(t, workspace, "http://127.0.0.1:7801", probeSaying(Alive))

	symlink := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(workspace.Repo, symlink); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	linked := filepath.Join(t.TempDir(), "linked checkout")
	if output, err := exec.Command("git", "-C", workspace.Repo, "worktree", "add", "-qb", "linked", linked).CombinedOutput(); err != nil {
		t.Fatalf("add linked worktree: %v: %s", err, output)
	}

	for name, path := range map[string]string{"symlink": symlink, "linked worktree": linked} {
		t.Run(name, func(t *testing.T) {
			other, err := Open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := other.ClaimResident(ctx, "http://127.0.0.1:7802", probeSaying(Alive)); err == nil {
				t.Fatalf("a resident started through the %s beside the one holding the repository", name)
			}
		})
	}

	t.Run("a different repository is unaffected", func(t *testing.T) {
		elsewhere := residentWorkspace(t)
		if _, err := elsewhere.ClaimResident(ctx, "http://127.0.0.1:7803", probeSaying(Alive)); err != nil {
			t.Fatalf("a distinct repository could not start its own resident: %v", err)
		}
	})

	if value, present := claimRef(t, workspace); !present || value != owner.blob {
		t.Fatalf("the original claim moved: %q present=%v", value, present)
	}
}
