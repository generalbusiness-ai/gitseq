package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
)

// The advertisement is an ordinary file inside the repository that any local
// process can write. These are the six ways it fails to be trustworthy, built
// here the way a corruption or a tamper would leave them rather than described.
// Five are the record's own failures and reach the caller through
// app.ResidentAdvertisement; the sixth reads and names this workroom and is
// caught one step later by the loopback validation, so both code paths are
// covered rather than one standing in for the other.
type untrustedAdvertisement struct {
	name string
	// write leaves the repository holding one untrustworthy record.
	write func(t *testing.T, workspace *app.Workspace)
	// class is the part of the refusal that says which failure this is. A
	// refusal that does not carry it tells the caller a resident is unusable
	// without saying what to repair, which is the whole value of refusing.
	class string
}

func untrustedAdvertisements() []untrustedAdvertisement {
	return []untrustedAdvertisement{
		{
			name:  "unreadable",
			write: makeAdvertisementUnreadable,
			class: "cannot be read",
		},
		{
			name: "larger than a record may be",
			write: func(t *testing.T, workspace *app.Workspace) {
				writeAdvertisement(t, workspace, bytes.Repeat([]byte("a"), (8<<10)+1))
			},
			class: "is larger than the 8192 bytes a resident record may be",
		},
		{
			name:  "not a record",
			write: func(t *testing.T, workspace *app.Workspace) { writeAdvertisement(t, workspace, []byte("{")) },
			class: "is not a resident record",
		},
		{
			name: "carrying no address",
			write: func(t *testing.T, workspace *app.Workspace) {
				writeAdvertisement(t, workspace, advertisementRecord(t, "", workspace.View().Genesis))
			},
			class: "advertises no address",
		},
		{
			name: "naming another workroom",
			write: func(t *testing.T, workspace *app.Workspace) {
				writeAdvertisement(t, workspace, advertisementRecord(t, "http://127.0.0.1:9", "git:sha1:"+strings.Repeat("0", 40)+"#git:sha1:"+strings.Repeat("1", 40)))
			},
			class: "names workroom",
		},
		{
			name: "not a bare http loopback origin",
			write: func(t *testing.T, workspace *app.Workspace) {
				writeAdvertisement(t, workspace, advertisementRecord(t, "http://example.com", workspace.View().Genesis))
			},
			// This one comes back through the other sentence, which quotes the
			// address before saying what is wrong with it.
			class: `advertises "http://example.com", which is not usable: resident service must name a loopback address`,
		},
	}
}

// A read may still answer from the verified local fold when the advertisement
// cannot be trusted. Reading locally costs a caller nothing it is not told
// about, so refusing every read would strand a session over a file it can
// repair without ever seeing the room.
func TestUntrustedAdvertisementStillLetsAReadAnswerLocally(t *testing.T) {
	for _, testCase := range untrustedAdvertisements() {
		t.Run(testCase.name, func(t *testing.T) {
			workspace := initRepository(t, "repo")
			testCase.write(t, workspace)

			server := newServer("human", workspace.Repo)
			value, _, err := server.call(context.Background(), toolCall{Name: "status"})
			if err != nil {
				t.Fatalf("an untrustworthy advertisement stopped a read: %v", err)
			}
			status, ok := value.(actorStatus)
			if !ok {
				t.Fatalf("status answered %T, not the local fold's own reading", value)
			}
			if len(status.Frontier) == 0 || status.Frontier[0].Depth == 0 {
				t.Fatalf("the local read answered nothing: %+v", status)
			}
		})
	}
}

// A durable act must not be folded locally because the record naming where to
// send it could not be trusted. Folding it anyway is a whole-log rebuild the
// author never asked for and cannot see, and it hides the tampering that
// caused it. The refusal names which of the six failures it is and the way
// out, and nothing is appended.
func TestUntrustedAdvertisementRefusesADurableAct(t *testing.T) {
	for _, testCase := range untrustedAdvertisements() {
		t.Run(testCase.name, func(t *testing.T) {
			workspace := initRepository(t, "repo")
			testCase.write(t, workspace)
			before := depth(t, workspace)

			server := newServer("human", workspace.Repo)
			_, _, err := server.call(context.Background(), toolCall{Name: "state", Arguments: map[string]any{
				"kind": "assert", "text": "must not reach the log through an untrusted advertisement",
				"rests_on": []any{genesisOf(t, workspace)}, "idempotency_key": "untrusted-" + testCase.name,
			}})
			if err == nil {
				t.Fatal("a durable act was accepted while the advertisement could not be trusted")
			}
			if !strings.Contains(err.Error(), testCase.class) {
				t.Fatalf("the refusal does not name which failure this is:\n got: %v\nwant it to contain: %q", err, testCase.class)
			}
			for _, wayOut := range []string{"nothing was appended", "--server -"} {
				if !strings.Contains(err.Error(), wayOut) {
					t.Fatalf("the refusal does not offer the way out %q: %v", wayOut, err)
				}
			}
			if after := depth(t, workspace); after != before {
				t.Fatalf("the act was folded locally anyway: depth %d became %d", before, after)
			}
		})
	}
}

// The refusal is about a record that is there and cannot be trusted, not about
// having no resident. A repository advertising nothing still acts locally
// exactly as it did before residents existed, and a session that met an
// untrustworthy record keeps its attachment: repairing the file is enough to
// carry on, with no reconnection.
func TestAbsentAdvertisementStillActsLocallyAfterARefusal(t *testing.T) {
	workspace := initRepository(t, "repo")
	writeAdvertisement(t, workspace, []byte("{"))
	server := newServer("human", workspace.Repo)

	if _, _, err := server.call(context.Background(), toolCall{Name: "state", Arguments: map[string]any{
		"kind": "assert", "text": "refused", "rests_on": []any{genesisOf(t, workspace)},
		"idempotency_key": "refused-once",
	}}); err == nil {
		t.Fatal("the untrustworthy record did not refuse")
	}

	// The room survives the refusal, so removing the record is the whole
	// repair: the next call resolves the advertisement again.
	if err := os.Remove(filepath.Join(workspace.MetaDir, "resident.json")); err != nil {
		t.Fatal(err)
	}
	before := depth(t, workspace)
	if _, _, err := server.call(context.Background(), toolCall{Name: "state", Arguments: map[string]any{
		"kind": "assert", "text": "accepted once the record is gone", "rests_on": []any{genesisOf(t, workspace)},
		"idempotency_key": "accepted-after",
	}}); err != nil {
		t.Fatalf("the session did not survive the refusal: %v", err)
	}
	if after := depth(t, workspace); after != before+1 {
		t.Fatalf("the act did not land locally: depth %d became %d", before, after)
	}
}

func writeAdvertisement(t *testing.T, workspace *app.Workspace, content []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(workspace.MetaDir, "resident.json"), content, 0o600); err != nil {
		t.Fatal(err)
	}
}

// A directory where the record belongs opens and then refuses to be read,
// which is the failure a caller sees for anything it cannot get bytes out of.
func makeAdvertisementUnreadable(t *testing.T, workspace *app.Workspace) {
	t.Helper()
	if err := os.Mkdir(filepath.Join(workspace.MetaDir, "resident.json"), 0o700); err != nil {
		t.Fatal(err)
	}
}

func advertisementRecord(t *testing.T, url, genesis string) []byte {
	t.Helper()
	content, err := json.Marshal(map[string]any{"url": url, "genesis": genesis, "pid": 1})
	if err != nil {
		t.Fatal(err)
	}
	return content
}
