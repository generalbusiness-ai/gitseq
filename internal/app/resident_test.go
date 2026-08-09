package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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
