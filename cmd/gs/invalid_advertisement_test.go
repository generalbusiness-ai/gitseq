package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
)

// A resident advertisement that is present and cannot be trusted must refuse
// the command, not fold it locally in silence. The record is an ordinary file
// inside the repository that any local process can write, and a durable act
// quietly folded here is a whole-log rebuild the author never asked for and
// cannot see afterwards.
//
// Every class below is one way the record can be untrustworthy, and each one
// asserts the reason its own guard prints. That is deliberate: several of the
// guards are reachable from a fixture another guard would also catch — an
// unparseable record decodes to an empty address, an empty address is not a
// usable URL — so a test that only asserted "it refused" would keep passing
// with its own guard deleted.

// advertisementPath is where the repository keeps the record. The name is
// spelled here rather than reached for through the package, and
// TestTheAdvertisementPathIsTheOneTheRepositoryWrites keeps it honest.
func advertisementPath(workspace *app.Workspace) string {
	return filepath.Join(workspace.MetaDir, "resident.json")
}

// advertised reports the address this repository publishes, when it publishes
// a usable one. It is the published-or-not question the serving tests ask;
// everything untrustworthy answers no, exactly as it did when this was a
// boolean on the workspace.
func advertised(workspace *app.Workspace) (string, bool) {
	advertisement := workspace.ResidentAdvertisement()
	if advertisement.State != app.AdvertisementPublished {
		return "", false
	}
	return advertisement.URL, true
}

func writeAdvertisement(t *testing.T, workspace *app.Workspace, content []byte) string {
	t.Helper()
	path := advertisementPath(workspace)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The path the tests below write is the path a real publication writes.
func TestTheAdvertisementPathIsTheOneTheRepositoryWrites(t *testing.T) {
	t.Parallel()
	workspace, _ := statusSummaryFixture(t)
	if _, err := workspace.PublishResident("http://127.0.0.1:7788"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(advertisementPath(workspace)); err != nil {
		t.Fatalf("publishing did not write %s: %v", advertisementPath(workspace), err)
	}
}

// validAdvertisement is a record this repository would accept in full: right
// genesis, a loopback address, nothing else wrong. Each class below spoils
// exactly one thing about it.
func validAdvertisement(t *testing.T, workspace *app.Workspace, url string) []byte {
	t.Helper()
	content, err := json.Marshal(app.Resident{URL: url, Genesis: workspace.View().Genesis, PID: os.Getpid()})
	if err != nil {
		t.Fatal(err)
	}
	return content
}

// invalidClass is one way the advertisement can be untrustworthy, the reason
// its guard prints, and where that guard lives.
type invalidClass struct {
	// spoil writes the record. It is given a live loopback listener the
	// command must never dial, so a class that stops refusing shows up as a
	// dial rather than only as a changed message.
	spoil func(t *testing.T, workspace *app.Workspace, listener string)
	// reason is the substring only this class's guard produces.
	reason string
	// guard names the code the class is pinned to, for the reader.
	guard string
}

func invalidClasses() map[string]invalidClass {
	return map[string]invalidClass{
		"unreadable": {
			guard: "app.readResident: open error",
			spoil: func(t *testing.T, workspace *app.Workspace, listener string) {
				path := writeAdvertisement(t, workspace, validAdvertisement(t, workspace, listener))
				if err := os.Chmod(path, 0o000); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
				// Mode 0000 does not stop a caller that bypasses permissions,
				// so prove the file really is unreadable here. Without this the
				// class would pass as root by never reaching the open error it
				// claims to pin, and a silent pass is worse than a skip.
				if _, err := os.ReadFile(path); err == nil {
					t.Skip("this user can read a 0000 file, so the unreadable branch cannot be reached; run as an unprivileged user")
				}
			},
			reason: "cannot be read",
		},
		"oversized": {
			guard: "app.readResident: advertisementLimit",
			spoil: func(t *testing.T, workspace *app.Workspace, listener string) {
				// Valid in every other respect, so only the bound can refuse it.
				padded := []byte(`{"url":` + strconv.Quote(listener) +
					`,"genesis":` + strconv.Quote(workspace.View().Genesis) + `,"pad":"`)
				padded = append(padded, bytes.Repeat([]byte("x"), 8<<10)...)
				writeAdvertisement(t, workspace, append(padded, []byte(`"}`)...))
			},
			reason: "is larger than the 8192 bytes",
		},
		"not a record": {
			guard: "app.readResident: json.Unmarshal",
			spoil: func(t *testing.T, workspace *app.Workspace, _ string) {
				writeAdvertisement(t, workspace, []byte("this is not a resident record"))
			},
			reason: "is not a resident record",
		},
		"truncated": {
			guard: "app.readResident: json.Unmarshal",
			spoil: func(t *testing.T, workspace *app.Workspace, listener string) {
				writeAdvertisement(t, workspace, []byte(`{"url":`+strconv.Quote(listener)+`,"genesis":`))
			},
			reason: "is not a resident record",
		},
		"empty address": {
			guard: "app.ResidentAdvertisement: record.URL == \"\"",
			spoil: func(t *testing.T, workspace *app.Workspace, _ string) {
				writeAdvertisement(t, workspace, validAdvertisement(t, workspace, ""))
			},
			reason: "advertises no address",
		},
		"missing genesis": {
			guard: "app.ResidentAdvertisement: record.Genesis != config.Genesis",
			spoil: func(t *testing.T, workspace *app.Workspace, listener string) {
				writeAdvertisement(t, workspace, []byte(`{"url":`+strconv.Quote(listener)+`}`))
			},
			reason: "names workroom",
		},
		"foreign genesis": {
			guard: "app.ResidentAdvertisement: record.Genesis != config.Genesis",
			spoil: func(t *testing.T, workspace *app.Workspace, listener string) {
				writeAdvertisement(t, workspace, []byte(`{"url":`+strconv.Quote(listener)+
					`,"genesis":"git:sha1:0000000000000000000000000000000000000000"}`))
			},
			reason: "names workroom",
		},
		"malformed address": {
			guard: "residentclient.ValidateURL: URL shape",
			spoil: func(t *testing.T, workspace *app.Workspace, listener string) {
				writeAdvertisement(t, workspace, validAdvertisement(t, workspace, listener+"/v0/submit"))
			},
			reason: "without credentials",
		},
		"non-loopback address": {
			guard: "residentclient.ValidateURL: loopback",
			spoil: func(t *testing.T, workspace *app.Workspace, _ string) {
				writeAdvertisement(t, workspace, validAdvertisement(t, workspace, "http://192.0.2.1:7777"))
			},
			reason: "must name a loopback address",
		},
	}
}

// The nine untrustworthy classes, each refused before any dial and each named
// by its own guard's reason. The tenth class is absence, which must still act
// locally, and TestNoAdvertisementActsLocally holds that.
func TestEveryUntrustworthyAdvertisementRefusesTheWrite(t *testing.T) {
	ctx := context.Background()
	for name, class := range invalidClasses() {
		t.Run(name, func(t *testing.T) {
			workspace, _ := statusSummaryFixture(t)
			var hits atomic.Int64
			listener := countingServer(t, &hits, http.NotFoundHandler())
			class.spoil(t, workspace, listener.URL)
			before, err := workspace.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}

			commandErr, stderr := writeAssert(t, workspace.Repo, "must not land")
			if commandErr == nil {
				t.Fatalf("an untrustworthy advertisement (%s) was folded locally in silence; guard %s", name, class.guard)
			}
			message := commandErr.Error()
			if !strings.Contains(message, class.reason) {
				t.Fatalf("the refusal does not carry the %s guard's reason %q: %v", class.guard, class.reason, commandErr)
			}
			for _, want := range []string{"this repository advertises", "--server -"} {
				if !strings.Contains(message, want) {
					t.Fatalf("the refusal does not name %q: %v", want, commandErr)
				}
			}
			if hits.Load() != 0 {
				t.Fatalf("an untrustworthy advertisement was dialed %d times", hits.Load())
			}
			if strings.Contains(stderr, "verified local fallback") {
				t.Fatalf("the refusal must not masquerade as a fallback notice: %q", stderr)
			}
			mustNotAppend(t, workspace, before)
		})
	}
}

// The refusal happens before the command reads a signing key. The actor's key
// file is removed here, so a command that resolved where to act after taking
// custody of the key would refuse for the wrong reason and fail this by name.
// Refusing after loading a key is not refusing early enough: it means the key
// was read for an act that could never have been sequenced.
func TestAnUntrustworthyAdvertisementRefusesBeforeAnyKeyIsRead(t *testing.T) {
	for name, class := range invalidClasses() {
		t.Run(name, func(t *testing.T) {
			workspace, _ := statusSummaryFixture(t)
			var hits atomic.Int64
			listener := countingServer(t, &hits, http.NotFoundHandler())
			class.spoil(t, workspace, listener.URL)

			keys, err := filepath.Glob(filepath.Join(workspace.MetaDir, "actors", "*"))
			if err != nil {
				t.Fatal(err)
			}
			if len(keys) == 0 {
				t.Fatal("the fixture provisioned no actor key to remove")
			}
			for _, key := range keys {
				if err := os.Remove(key); err != nil {
					t.Fatal(err)
				}
			}

			commandErr, _ := writeAssert(t, workspace.Repo, "must not land")
			if commandErr == nil {
				t.Fatal("the command succeeded with neither a trustworthy advertisement nor a key")
			}
			if !strings.Contains(commandErr.Error(), class.reason) {
				t.Fatalf("the advertisement was resolved after the key was read: %v", commandErr)
			}
		})
	}
}

// Every command that takes --server, on the same untrustworthy record. This is
// the table that proves the resolver is wired into each of them rather than
// only being correct in itself: with the record spoiled each refuses with the
// resolver's own words, with `--server -` each gets past it, and with nothing
// advertised each gets past it too.
//
// The six writes additionally prove they appended nothing.
func TestEveryServerAwareCommandRefusesAnUntrustworthyAdvertisement(t *testing.T) {
	ctx := context.Background()
	const marker = "this repository advertises"
	commands := []struct {
		name      string
		writes    bool
		arguments func(t *testing.T, workspace *app.Workspace) []string
		run       func(ctx context.Context, arguments []string) error
	}{
		{"state", true, func(t *testing.T, workspace *app.Workspace) []string {
			repo := workspace.Repo
			return []string{"--repo", repo, "--as", "operator", "--kind", "assert", "--text", "must not land"}
		}, stateCommand},
		{"review", true, func(t *testing.T, workspace *app.Workspace) []string {
			repo := workspace.Repo
			return []string{"--repo", repo, "--as", "operator", "--checkout", repo,
				"--artifact", "artifact-event", "--promise", "promise-event",
				"--verdict", "approved", "--text", "must not land"}
		}, reviewCommand},
		{"merge", true, func(t *testing.T, workspace *app.Workspace) []string {
			repo := workspace.Repo
			return []string{"--repo", repo, "--as", "operator", "--checkout", repo,
				"--candidate", strings.Repeat("0", 40), "--approval", "approval-event", "--text", "must not land"}
		}, mergeCommand},
		{"ratify", true, func(t *testing.T, workspace *app.Workspace) []string {
			repo := workspace.Repo
			return []string{"--repo", repo, "--as", "operator", "target-event"}
		}, ratifyCommand},
		{"supersede", true, func(t *testing.T, workspace *app.Workspace) []string {
			repo := workspace.Repo
			return []string{"--repo", repo, "--as", "operator", "--text", "must not land", "target-event"}
		}, supersedeCommand},
		{"batch", true, func(t *testing.T, workspace *app.Workspace) []string {
			repo := workspace.Repo
			return []string{"--repo", repo, "--as", "operator", batchFile(t)}
		}, batchCommand},
		{"status", false, func(t *testing.T, workspace *app.Workspace) []string {
			repo := workspace.Repo
			return []string{"--repo", repo, "--json"}
		}, statusCommand},
		{"work", false, func(t *testing.T, workspace *app.Workspace) []string {
			repo := workspace.Repo
			return []string{"--repo", repo, "--as", "operator"}
		}, workCommand},
		{"artifacts", false, func(t *testing.T, workspace *app.Workspace) []string {
			return []string{"--repo", workspace.Repo, "--path", "AGENTS.md"}
		}, artifactsCommand},
		{"inspect", false, func(t *testing.T, workspace *app.Workspace) []string {
			return []string{"--repo", workspace.Repo, anyEvent(t, workspace)}
		}, inspectCommand},
		{"reviews", false, func(t *testing.T, workspace *app.Workspace) []string {
			repo := workspace.Repo
			return []string{"--repo", repo, "--checkout", repo}
		}, reviewsCommand},
	}
	for _, command := range commands {
		t.Run(command.name, func(t *testing.T) {
			workspace, _ := statusSummaryFixture(t)
			var hits atomic.Int64
			listener := countingServer(t, &hits, http.NotFoundHandler())
			arguments := command.arguments(t, workspace)

			// Untrustworthy: refused, in the resolver's own words, with nothing
			// dialed and — for a write — nothing appended.
			writeAdvertisement(t, workspace, []byte(`{"url":`+strconv.Quote(listener.URL)+
				`,"genesis":"git:sha1:0000000000000000000000000000000000000000"}`))
			before, err := workspace.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			commandErr, _, _ := runPiped(t, func() error { return command.run(ctx, arguments) })
			if commandErr == nil || !strings.Contains(commandErr.Error(), marker) {
				t.Fatalf("gs %s did not refuse an untrustworthy advertisement: %v", command.name, commandErr)
			}
			if !strings.Contains(commandErr.Error(), "names workroom") {
				t.Fatalf("gs %s refused for some other reason: %v", command.name, commandErr)
			}
			if hits.Load() != 0 {
				t.Fatalf("gs %s dialed the untrustworthy advertisement %d times", command.name, hits.Load())
			}
			if command.writes {
				mustNotAppend(t, workspace, before)
			}

			// `--server -` is the named way out and must get past the resolver.
			sentinelErr, _, _ := runPiped(t, func() error {
				return command.run(ctx, append(append([]string{}, arguments...), "--server", "-"))
			})
			if sentinelErr != nil && strings.Contains(sentinelErr.Error(), marker) {
				t.Fatalf("gs %s --server - was still stopped by the advertisement: %v", command.name, sentinelErr)
			}

			// Absence must behave exactly as it did before residents existed.
			if err := os.Remove(advertisementPath(workspace)); err != nil {
				t.Fatal(err)
			}
			absentErr, _, _ := runPiped(t, func() error { return command.run(ctx, arguments) })
			if absentErr != nil && strings.Contains(absentErr.Error(), marker) {
				t.Fatalf("gs %s refused with no advertisement at all: %v", command.name, absentErr)
			}
		})
	}
}

// anyEvent names a real event in this repository, so `gs inspect` is stopped by
// the advertisement rather than by an event it could never have found.
func anyEvent(t *testing.T, workspace *app.Workspace) string {
	t.Helper()
	snapshot, err := workspace.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range snapshot.Projection.Statements {
		if statement.Event != "" {
			return statement.Event
		}
	}
	t.Fatal("the fixture holds no event to inspect")
	return ""
}

// batchFile writes one act for the batch command to read, so the batch reaches
// the resolver with real work in hand rather than refusing on its input.
func batchFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "batch.json")
	content := []byte(`[{"verb":"state","kind":"assert","text":"must not land"}]`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A record left behind by a service that is gone is the tenth class, and the
// only one whose guard is at the submission boundary rather than in the read:
// the record is entirely valid and the address simply refuses the dial.
func TestAnAdvertisedAddressThatRefusesTheDialAppendsNothing(t *testing.T) {
	ctx := context.Background()
	workspace, _ := statusSummaryFixture(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := "http://" + listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	writeAdvertisement(t, workspace, validAdvertisement(t, workspace, address))
	before, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	commandErr, _ := writeAssert(t, workspace.Repo, "must not land")
	if commandErr == nil {
		t.Fatal("a dead advertised address folded the act locally in silence")
	}
	if !strings.Contains(commandErr.Error(), "nothing was appended") {
		t.Fatalf("the refusal does not say nothing landed: %v", commandErr)
	}
	mustNotAppend(t, workspace, before)
}
