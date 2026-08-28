package host_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The public surface exists to be imported from outside this module, and the
// only honest proof of that is a module that actually does it. An in-tree test
// cannot show it: every package here may reach into internal/, so an in-tree
// test passes whether or not the exported surface is sufficient.
//
// These tests build a real second module against this checkout and run an
// application in it. They cost a compile, and they are the reason the exported
// surface can be trusted to be enough rather than believed to be.

// outsideApplication is a complete application in another module: it binds a
// repository to itself, appends signed acts, replays the log through its own
// fold, and never mentions gitseq's internals.
const outsideApplication = `package outside

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/host"
)

// This application's whole vocabulary and its whole meaning.
type move struct {
	Square string ` + "`json:\"square\"`" + `
}

type state struct {
	squares []string
	refused int
}

// fold is deterministic, total, and the only thing that decides what an act
// means. Unknown schemas stay opaque; an act out of turn is recorded and
// ineffective.
func fold(records []host.Record) state {
	var result state
	var seats []string
	for _, record := range records {
		switch record.Schema {
		case "outside/seat@0":
			if len(seats) < 2 {
				seats = append(seats, record.Actor)
			}
		case "outside/move@0":
			var decoded move
			if err := json.Unmarshal(record.Payload, &decoded); err != nil {
				result.refused++
				continue
			}
			if len(seats) != 2 || record.Actor != seats[len(result.squares)%2] {
				result.refused++
				continue
			}
			result.squares = append(result.squares, decoded.Square)
		}
	}
	return result
}

func application() host.Application {
	return host.Application{Name: "outside", FoldVersion: "outside-fold@0", SourceURL: "https://example.invalid/outside.git"}
}

func key(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return private
}

func TestOutsideApplicationRunsOnTheKernel(t *testing.T) {
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}

	white, black := key(t), key(t)
	workspace, err := host.Init(ctx, repo, application(), white, host.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, seat := range []ed25519.PrivateKey{white, black} {
		if _, err := workspace.Append(ctx, seat, host.Act{Schema: "outside/seat@0", Payload: []byte("{}")}); err != nil {
			t.Fatal(err)
		}
	}
	play := func(signer ed25519.PrivateKey, square string) host.Record {
		t.Helper()
		payload, err := json.Marshal(move{Square: square})
		if err != nil {
			t.Fatal(err)
		}
		record, err := workspace.Append(ctx, signer, host.Act{Schema: "outside/move@0", Payload: payload})
		if err != nil {
			t.Fatal(err)
		}
		return record
	}
	play(white, "e4")
	play(black, "e5")
	// Out of turn, and signed outside host. The host receives the canonical
	// draft, public key, and signature, never black's private key. The kernel
	// takes the valid signature and the fold is what refuses the move.
	payload, err := json.Marshal(move{Square: "d5"})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := workspace.Prepare(host.Act{Schema: "outside/move@0", Payload: payload, IdempotencyKey: "external-d5"})
	if err != nil {
		t.Fatal(err)
	}
	signingBytes, err := host.ActorSigningBytes(prepared)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.AppendSigned(ctx, host.SignedAct{
		Prepared: prepared, ActorKey: black.Public().(ed25519.PublicKey),
		ActorSignature: ed25519.Sign(black, signingBytes),
	}); err != nil {
		t.Fatal(err)
	}

	log, err := workspace.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	result := fold(log.Records)
	if len(result.squares) != 2 || result.squares[0] != "e4" || result.squares[1] != "e5" {
		t.Fatalf("state %v, want the two moves in turn", result.squares)
	}
	if result.refused != 1 {
		t.Fatalf("refused %d acts, want the out-of-turn move recorded and ineffective", result.refused)
	}

	// An ordinary clone with the already-fetched sequence is a different
	// operation from Init. OpenAttached needs only public attachment fields,
	// and explicit sequencer custody makes that existing sequence writable.
	clone := filepath.Join(t.TempDir(), "clone")
	if output, err := exec.Command("git", "init", "-q", clone).CombinedOutput(); err != nil {
		t.Fatalf("git init clone: %v: %s", err, output)
	}
	ref := "refs/seq/" + log.Genesis
	if output, err := exec.Command("git", "-C", clone, "fetch", "--no-tags", repo, ref+":"+ref).CombinedOutput(); err != nil {
		t.Fatalf("fetch sequence: %v: %s", err, output)
	}
	gitDir, err := exec.Command("git", "-C", repo, "rev-parse", "--absolute-git-dir").Output()
	if err != nil {
		t.Fatal(err)
	}
	attached, err := host.OpenAttached(ctx, clone, application(), host.Attachment{
		Genesis: log.Genesis, SequencerKey: filepath.Join(strings.TrimSpace(string(gitDir)), "gitseq", "sequencer"),
	})
	if err != nil {
		t.Fatal(err)
	}
	remote := key(t)
	prepared, err = attached.Prepare(host.Act{Schema: "outside/audit@0", Payload: []byte("attached"), IdempotencyKey: "attached"})
	if err != nil {
		t.Fatal(err)
	}
	signingBytes, err = host.ActorSigningBytes(prepared)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := attached.AppendSigned(ctx, host.SignedAct{
		Prepared: prepared, ActorKey: remote.Public().(ed25519.PublicKey),
		ActorSignature: ed25519.Sign(remote, signingBytes),
	}); err != nil {
		t.Fatal(err)
	}
	attachedLog, err := attached.Records(ctx)
	if err != nil || attachedLog.Genesis != log.Genesis || attachedLog.Depth != log.Depth+1 {
		t.Fatalf("attached log = %+v, %v, want existing genesis advanced once", attachedLog, err)
	}

	// A second reader replays the same log to the same state.
	reopened, err := host.Open(ctx, repo, application())
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := reopened.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	again := fold(replayed.Records)
	if len(again.squares) != len(result.squares) || again.refused != result.refused {
		t.Fatal("replaying the same log in a fresh reader gave a different state")
	}

	// A different application is refused the repository, and says so as a
	// missing interpreter rather than a broken log.
	other := host.Application{Name: "elsewhere", FoldVersion: "elsewhere-fold@0"}
	if _, err := host.Open(ctx, repo, other); !errors.Is(err, host.ErrUninterpretable) {
		t.Fatalf("open by another application = %v, want host.ErrUninterpretable", err)
	}
}

func TestOutsideApplicationUsesTheIdentifierContract(t *testing.T) {
	private := key(t)
	fingerprint, err := host.ActorFingerprint(private.Public().(ed25519.PublicKey))
	if err != nil || !host.ValidActorFingerprint(fingerprint) {
		t.Fatalf("external actor fingerprint = %q, %v", fingerprint, err)
	}
	const genesis = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const event = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	id := host.EventID("sha1", genesis, event)
	if id != "git:sha1:"+genesis+"#git:sha1:"+event || !host.ValidEventID(id) {
		t.Fatalf("external event identifier = %q", id)
	}
}
`

// reachInside is the negative half. If the internal packages were reachable
// from outside, the exported surface would not have to be sufficient, and this
// whole boundary would be a convention rather than a rule the compiler keeps.
const reachInside = `package reachinside

import _ "github.com/generalbusiness-ai/gitseq/internal/kernel"
`

func repoRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate the checkout under test")
	}
	return filepath.Dir(filepath.Dir(filename))
}

// outsideModule writes a module that resolves gitseq to this exact checkout,
// so the test measures the working tree rather than a published version.
func outsideModule(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain unavailable")
	}
	root := repoRoot(t)
	module := t.TempDir()
	goMod := "module example.invalid/outside\n\ngo 1.26.0\n\n" +
		"require github.com/generalbusiness-ai/gitseq v0.0.0\n\n" +
		"replace github.com/generalbusiness-ai/gitseq => " + root + "\n"
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(module, "go.mod"), goMod)
	write(filepath.Join(module, "outside", "outside_test.go"), outsideApplication)
	write(filepath.Join(module, "reachinside", "reachinside.go"), reachInside)
	return module
}

func runInModule(t *testing.T, module string, arguments ...string) (string, error) {
	t.Helper()
	command := exec.Command("go", arguments...)
	command.Dir = module
	// The module has no go.sum of its own, so the toolchain resolves the two
	// dependencies this checkout already pins and records them itself.
	command.Env = append(os.Environ(), "GOFLAGS=-mod=mod")
	output, err := command.CombinedOutput()
	return string(output), err
}

// TestApplicationInAnotherModuleRunsOnTheExportedSurface is item 3's own
// acceptance: an application outside this module gets a repository bound to
// itself, appends signed acts, and folds them, using nothing but the exported
// surface.
func TestApplicationInAnotherModuleRunsOnTheExportedSurface(t *testing.T) {
	module := outsideModule(t)
	output, err := runInModule(t, module, "test", "./outside", "-count=1")
	if err != nil {
		t.Fatalf("an application in another module could not run on the exported surface: %v\n%s", err, output)
	}
}

// TestAnotherModuleCannotReachInternalPackages keeps the surface honest from
// the other side: what is not exported here is not available there, so the
// exported surface is the whole contract.
func TestAnotherModuleCannotReachInternalPackages(t *testing.T) {
	module := outsideModule(t)
	output, err := runInModule(t, module, "build", "./reachinside")
	if err == nil {
		t.Fatal("another module compiled against an internal package")
	}
	if !strings.Contains(output, "internal package") {
		t.Fatalf("build failed for some other reason than the internal boundary:\n%s", output)
	}
}

// TestPublicSurfaceDependsOnNoApplicationProfile keeps the layering claim in
// docs/reference/architecture.md a fact rather than an intention. The public
// surface and the host vocabulary under it sit below every application, so
// importing one would put the host layer on top of the layer it selects — and
// an application outside this module would link, and inherit, meanings it
// never asked for. The compiler will not catch that on its own: internal/app
// is importable from here.
func TestPublicSurfaceDependsOnNoApplicationProfile(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain unavailable")
	}
	forbidden := map[string]string{
		"github.com/generalbusiness-ai/gitseq/internal/workroom": "the Workroom application profile",
		"github.com/generalbusiness-ai/gitseq/internal/app":      "the Workroom-coupled host adapter",
	}
	for _, pkg := range []string{"./host", "./host/live", "./internal/apphost"} {
		command := exec.Command("go", "list", "-deps", pkg)
		command.Dir = repoRoot(t)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("go list -deps %s: %v\n%s", pkg, err, output)
		}
		for _, line := range strings.Split(string(output), "\n") {
			if reason, bad := forbidden[strings.TrimSpace(line)]; bad {
				t.Errorf("%s depends on %s (%s)", pkg, strings.TrimSpace(line), reason)
			}
		}
	}
}
