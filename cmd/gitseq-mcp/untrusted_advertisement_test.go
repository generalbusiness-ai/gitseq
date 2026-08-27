package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/service"
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

// A room that has already found a resident is the case this guard exists for
// just as much as a fresh one, and it is the harder one: the record is
// untrusted input every time it is read, not only the first time. A room that
// answered from a remembered address would never look at the file again, so
// tampering would go unnoticed for the whole life of a session — and when the
// service it named then stopped, the act would land in the local fold with the
// tampering unmentioned, which is exactly the silent whole-log rebuild the
// refusal exists to prevent.
func TestUntrustedAdvertisementRefusesADurableActAfterAGoodOneWasCached(t *testing.T) {
	refuseAfterAGoodAdvertisementWasCached(t, false)
}

// The same transition with the service also gone, which is the shape the
// tampering actually arrives in: a record is rewritten and the resident it
// named is stopped. Losing the connection is the adapter's one reason to fold
// an act locally, so this is the case where a guard that only ran while the
// address was unknown would hand the act to the local fold and say nothing.
func TestUntrustedAdvertisementRefusesRatherThanFoldingLocallyWhenTheResidentAlsoStops(t *testing.T) {
	refuseAfterAGoodAdvertisementWasCached(t, true)
}

// refuseAfterAGoodAdvertisementWasCached runs the transition for each of the
// six failures: a room finds a resident and acts through it, then the record
// becomes untrustworthy. stopResident says whether the service the room
// remembers also goes away, which is what separates the classification the
// durable path makes up front from the one it makes before falling back.
func refuseAfterAGoodAdvertisementWasCached(t *testing.T, stopResident bool) {
	t.Helper()
	for _, testCase := range untrustedAdvertisements() {
		t.Run(testCase.name, func(t *testing.T) {
			workspace := initRepository(t, "repo")
			resident := startResident(t, workspace)
			server := newServer("human", workspace.Repo)

			// One good act first. It is what puts a trusted address in the
			// room, so what follows tests the transition rather than a room
			// that never had one.
			value, _, err := server.call(context.Background(), toolCall{Name: "state", Arguments: map[string]any{
				"kind": "assert", "text": "sent through the resident this room then remembers",
				"rests_on": []any{genesisOf(t, workspace)}, "idempotency_key": "cached-" + testCase.name,
			}})
			if err != nil {
				t.Fatalf("the resident did not accept the priming act: %v", err)
			}
			if result, ok := value.(map[string]any); !ok || result["degraded"] == true {
				t.Fatalf("the priming act did not go through the resident, so no address was remembered: %+v", value)
			}

			testCase.write(t, workspace)
			if stopResident {
				resident.Close()
			}

			before := depth(t, workspace)
			_, _, err = server.call(context.Background(), toolCall{Name: "state", Arguments: map[string]any{
				"kind": "assert", "text": "must not reach the log through a remembered address",
				"rests_on": []any{genesisOf(t, workspace)}, "idempotency_key": "after-cached-" + testCase.name,
			}})
			if err == nil {
				t.Fatal("durable act was accepted after the advertisement became untrustworthy")
			}
			if !strings.Contains(err.Error(), testCase.class) {
				t.Fatalf("the refusal does not name which failure this is:\n got: %v\nwant it to contain: %q", err, testCase.class)
			}
			for _, wayOut := range []string{"repair or remove that record", "--server -"} {
				if !strings.Contains(err.Error(), wayOut) {
					t.Fatalf("the refusal does not offer the way out %q: %v", wayOut, err)
				}
			}
			if after := depth(t, workspace); after != before {
				t.Fatalf("the act was folded locally anyway: depth %d became %d", before, after)
			}

			// The read path keeps the remembered address and pays nothing for
			// this guard: through the resident while it is there, and from the
			// verified local fold once it is not, exactly as before.
			read, _, err := server.call(context.Background(), toolCall{Name: "status"})
			if err != nil {
				t.Fatalf("an untrustworthy advertisement stopped a read: %v", err)
			}
			if status, ok := read.(actorStatus); !ok || len(status.Frontier) == 0 {
				t.Fatalf("the read did not answer: %+v", read)
			}
		})
	}
}

// A record can be rewritten while a call is already in flight, and losing the
// connection is what that looks like from inside the call. Transport loss is
// the adapter's one honest reason to fold an act locally, which makes it the
// one place a tamper could still arrive too late to be seen — so the record is
// read once more before anything is folded.
func TestAdvertisementRewrittenDuringACallRefusesTheLocalFallback(t *testing.T) {
	workspace := initRepository(t, "repo")
	workroomServer, err := service.New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	served := workroomServer.Handler()
	resident := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v0/submit") {
			served.ServeHTTP(w, r)
			return
		}
		// The act arrives, the record is rewritten underneath it, and the
		// connection dies without an answer. The caller cannot tell whether
		// this act landed here, and must not settle that by appending it
		// somewhere else.
		if err := os.WriteFile(filepath.Join(workspace.MetaDir, "resident.json"), []byte("{"), 0o600); err != nil {
			t.Error(err)
			return
		}
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Error("the test resident cannot drop a connection")
			return
		}
		connection, _, err := hijacker.Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		connection.Close()
	}))
	t.Cleanup(resident.Close)
	if _, err := workspace.PublishResident(resident.URL); err != nil {
		t.Fatal(err)
	}

	server := newServer("human", workspace.Repo)
	before := depth(t, workspace)
	_, _, err = server.call(context.Background(), toolCall{Name: "state", Arguments: map[string]any{
		"kind": "assert", "text": "must not be folded locally after the record was rewritten mid-call",
		"rests_on": []any{genesisOf(t, workspace)}, "idempotency_key": "rewritten-mid-call",
	}})
	if err == nil {
		t.Fatal("the act was folded locally after the record was rewritten while the call was in flight")
	}
	if !strings.Contains(err.Error(), "is not a resident record") {
		t.Fatalf("the refusal does not name the record that became untrustworthy: %v", err)
	}
	if after := depth(t, workspace); after != before {
		t.Fatalf("the act was folded locally anyway: depth %d became %d", before, after)
	}
}

// The refusal must come from the record, before the signing key is read. That
// ordering is the difference between a tampered repository costing a caller a
// message and it costing them their key in memory and a half-built act, and
// nothing about a passing refusal test says which of the two happened. So this
// makes both fail at once: the record is untrustworthy and the actor's custody
// is corrupt. Only the resolution running first can produce the record's
// reason; resolve second and the key's failure answers instead.
func TestUntrustedAdvertisementRefusesBeforeTheSigningKeyIsRead(t *testing.T) {
	workspace := initRepository(t, "repo")
	writeAdvertisement(t, workspace, []byte("{"))

	actor, err := workspace.ResolveActor("human")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(actor.KeyFile, []byte("not a signing key"), 0o600); err != nil {
		t.Fatal(err)
	}

	server := newServer("human", workspace.Repo)
	_, _, err = server.call(context.Background(), toolCall{Name: "state", Arguments: map[string]any{
		"kind": "assert", "text": "must be refused by the record, not by the key",
		"rests_on": []any{genesisOf(t, workspace)}, "idempotency_key": "key-order",
	}})
	if err == nil {
		t.Fatal("a durable act was accepted while both the advertisement and the actor's custody were unusable")
	}
	if !strings.Contains(err.Error(), "is not a resident record") {
		t.Fatalf("the signing key was read before the advertisement was judged; the refusal came from the key, not the record: %v", err)
	}
}

// startResident serves this workroom over loopback and publishes the record
// naming it, which is what a room reads once and then remembers. It hands back
// the service so a test can stop it and see what the room does next.
func startResident(t *testing.T, workspace *app.Workspace) *httptest.Server {
	t.Helper()
	workroomServer, err := service.New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	resident := httptest.NewServer(workroomServer.Handler())
	t.Cleanup(resident.Close)
	if _, err := workspace.PublishResident(resident.URL); err != nil {
		t.Fatal(err)
	}
	return resident
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
	path := filepath.Join(workspace.MetaDir, "resident.json")
	// A resident may have published a real record here already; this failure
	// has to be able to replace one, the way tampering after a good start
	// would.
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
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
