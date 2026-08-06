package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"gitseq/spike/internal/gitstore"
	"gitseq/spike/internal/intent"
	"gitseq/spike/internal/kernel"
)

func TestActualProcessExitRecoversFromGitAlone(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	binary := filepath.Join(root, "gitseq-spike")
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v: %s", err, output)
	}
	repo := filepath.Join(root, "domain.git")
	key := filepath.Join(root, "sequencer")
	createOutput, err := exec.CommandContext(ctx, binary, "create", "--repo", repo, "--key", key).Output()
	if err != nil {
		t.Fatal(err)
	}
	var created map[string]string
	if err := json.Unmarshal(createOutput, &created); err != nil {
		t.Fatal(err)
	}
	genesis := created["genesis"]

	scratch, err := gitstore.InitBare(ctx, filepath.Join(root, "scratch.git"), "sha1")
	if err != nil {
		t.Fatal(err)
	}
	_, actorKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	requestPath := func(name, payload string) string {
		t.Helper()
		tree, err := scratch.WritePayloadTree(ctx, []byte(payload), nil)
		if err != nil {
			t.Fatal(err)
		}
		signed, err := intent.Sign(intent.Intent{
			Version: intent.Version, Target: "git:sha1:" + genesis, Schema: "process-exit.v0", PayloadTree: "git:sha1:" + tree,
			IdempotencyNS: "black-box", IdempotencyKey: name,
		}, actorKey)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(kernel.Request{Signed: signed, Payload: []byte(payload)})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, name+".json")
		if err := os.WriteFile(path, encoded, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	runSubmit := func(request, failpoint string) ([]byte, error) {
		args := []string{"submit", "--repo", repo, "--key", key, "--request", request}
		if failpoint != "" {
			args = append(args, "--failpoint", failpoint)
		}
		return exec.CommandContext(ctx, binary, args...).Output()
	}
	requireExit97 := func(err error) {
		t.Helper()
		var exitError *exec.ExitError
		if !errors.As(err, &exitError) || exitError.ExitCode() != 97 {
			t.Fatalf("wanted simulated process death (97), got %v", err)
		}
	}

	beforeCAS := requestPath("before-cas", "before")
	_, err = runSubmit(beforeCAS, "after_commit_written")
	requireExit97(err)
	output, err := runSubmit(beforeCAS, "")
	if err != nil {
		t.Fatal(err)
	}
	var first kernel.Result
	if err := json.Unmarshal(output, &first); err != nil {
		t.Fatal(err)
	}
	if first.Replay {
		t.Fatal("object-only pre-CAS attempt became visible")
	}

	afterCAS := requestPath("after-cas", "after")
	_, err = runSubmit(afterCAS, "after_ref_cas")
	requireExit97(err)
	output, err = runSubmit(afterCAS, "")
	if err != nil {
		t.Fatal(err)
	}
	var second kernel.Result
	if err := json.Unmarshal(output, &second); err != nil {
		t.Fatal(err)
	}
	if !second.Replay {
		t.Fatal("post-CAS process death was not recovered as a replay")
	}

	verifyOutput, err := exec.CommandContext(ctx, binary, "verify", "--repo", repo, "--genesis", genesis).Output()
	if err != nil {
		t.Fatal(err)
	}
	var verification kernel.Verification
	if err := json.Unmarshal(verifyOutput, &verification); err != nil {
		t.Fatal(err)
	}
	if verification.Events != 2 {
		t.Fatalf("verification after process deaths = %#v", verification)
	}
}
