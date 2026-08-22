package identity_test

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/host"
	"github.com/generalbusiness-ai/gitseq/host/identity"
)

// These tests use only what an application outside this module can use: they
// create a real repository through the public host surface, record real
// identity acts in it, and resolve the verified log that comes back. Together
// with resolve_test.go — which builds a log by hand to state the time rules
// exactly — they cover both what is written and what is judged.

const (
	testName = "gitseq-identity-test"
	testFold = "gitseq-identity-test-fold@0"
)

func testWorkspace(t *testing.T, ctx context.Context) (*host.Workspace, ed25519.PrivateKey) {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	_, initializer := testKey(t)
	workspace, err := host.Init(ctx, repo, host.Application{Name: testName, FoldVersion: testFold}, initializer, host.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return workspace, initializer
}

func resolveWorkspace(t *testing.T, ctx context.Context, workspace *host.Workspace) *identity.Resolution {
	t.Helper()
	log, err := workspace.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return identity.Resolve(log)
}

// The onboarding path this thread exists for, walked end to end in one test.
// A newcomer arrives holding nothing, plays as an unanchored key, logs in with
// GitHub, and is the same key afterwards — now with a persistent identity, and
// with no ssh key provisioned by hand anywhere in the story.
func TestNewcomerPlaysUnanchoredThenAnchorsThroughGitHubLogin(t *testing.T) {
	ctx := context.Background()
	workspace, initializer := testWorkspace(t, ctx)

	// Setup: the deployment records the public half of its witnessing key, so
	// what it witnesses stays checkable after the deployment is gone.
	witnessPublic, witnessPrivate := testKey(t)
	if _, err := identity.DeclareWitness(ctx, workspace, initializer, witnessPublic, []string{identity.GitHubScheme}); err != nil {
		t.Fatal(err)
	}

	// The newcomer's browser mints a key and plays. Zero prompts, no account.
	_, session := testKey(t)
	move, err := workspace.Append(ctx, session, host.Act{Schema: "chess/move@0", Payload: []byte("e4")})
	if err != nil {
		t.Fatal(err)
	}
	if resolved := resolveWorkspace(t, ctx, workspace).LookupAt(move.ID); resolved.Anchored {
		t.Fatalf("a browser-minted key started out anchored: %+v", resolved)
	}

	// The upgrade: the deployment checks the provider's token out of band, and
	// then says what it found under its own key.
	server := githubServer(t, "gho_valid", 4242, "alice")
	found, err := identity.GitHub{API: server.URL}.Check(ctx, "gho_valid")
	if err != nil {
		t.Fatal(err)
	}
	anchor, err := identity.Endorse(ctx, workspace, witnessPrivate, identity.Anchor{
		Subject: move.Actor, Identity: &found, Scope: "play",
	})
	if err != nil {
		t.Fatal(err)
	}

	afterAnchor, err := workspace.Append(ctx, session, host.Act{Schema: "chess/move@0", Payload: []byte("e5")})
	if err != nil {
		t.Fatal(err)
	}
	resolved := resolveWorkspace(t, ctx, workspace).LookupAt(afterAnchor.ID)
	if !resolved.Anchored {
		t.Fatal("the session key did not anchor after a witnessed GitHub login")
	}
	want := identity.Identity{Scheme: identity.GitHubScheme, Subject: "4242", Handle: "alice"}
	if resolved.Identity != want {
		t.Errorf("identity = %+v, want %+v", resolved.Identity, want)
	}
	if resolved.Vouching != identity.Witnessed || resolved.Verification != identity.InLog {
		t.Errorf("axes = %v/%v, want witnessed/in-log", resolved.Vouching, resolved.Verification)
	}
	if resolved.Record != anchor.ID {
		t.Errorf("answering record = %q, want %q", resolved.Record, anchor.ID)
	}
	if got, want := resolved.Display(move.Actor), "alice [github:4242] (witnessed; in-log)"; got != want {
		t.Errorf("display = %q, want %q", got, want)
	}
}

// The recovery and agent-credential path, through a real repository: an
// anchored person endorses another key, which inherits the identity and can be
// withdrawn again.
func TestAnchoredPersonEndorsesAnotherKeyAndCanWithdrawIt(t *testing.T) {
	ctx := context.Background()
	workspace, initializer := testWorkspace(t, ctx)
	witnessPublic, witnessPrivate := testKey(t)
	if _, err := identity.DeclareWitness(ctx, workspace, initializer, witnessPublic, []string{identity.GitHubScheme}); err != nil {
		t.Fatal(err)
	}

	_, personKey := testKey(t)
	person, err := workspace.Append(ctx, personKey, host.Act{Schema: "chess/join@0", Payload: []byte("{}")})
	if err != nil {
		t.Fatal(err)
	}
	alice := identity.Identity{Scheme: identity.GitHubScheme, Subject: "4242", Handle: "alice"}
	if _, err := identity.Endorse(ctx, workspace, witnessPrivate, identity.Anchor{
		Subject: person.Actor, Identity: &alice,
	}); err != nil {
		t.Fatal(err)
	}

	_, agentKey := testKey(t)
	agent, err := workspace.Append(ctx, agentKey, host.Act{Schema: "chess/join@0", Payload: []byte("{}")})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := identity.Endorse(ctx, workspace, personKey, identity.Anchor{
		Subject: agent.Actor, Scope: "move",
	})
	if err != nil {
		t.Fatal(err)
	}

	agentAct, err := workspace.Append(ctx, agentKey, host.Act{Schema: "chess/move@0", Payload: []byte("e4")})
	if err != nil {
		t.Fatal(err)
	}
	resolved := resolveWorkspace(t, ctx, workspace).LookupAt(agentAct.ID)
	if !resolved.Anchored || resolved.Identity != alice {
		t.Fatalf("agent credential = %+v, want alice's identity", resolved)
	}
	if resolved.Vouching != identity.Witnessed {
		t.Errorf("vouching = %v, want witnessed, the strength of the anchor that minted it", resolved.Vouching)
	}

	_, err = identity.Revoke(ctx, workspace, personKey, credential.ID)
	if err != nil {
		t.Fatal(err)
	}
	afterWithdrawal, err := workspace.Append(ctx, agentKey, host.Act{Schema: "chess/move@0", Payload: []byte("e5")})
	if err != nil {
		t.Fatal(err)
	}
	if resolveWorkspace(t, ctx, workspace).LookupAt(afterWithdrawal.ID).Anchored {
		t.Fatal("the agent credential survived its withdrawal")
	}
}

// Endorse writes the repository's own genesis, whatever the caller put there,
// so an endorsement built for one log cannot be appended to another.
func TestEndorseWritesThisRepositorysGenesis(t *testing.T) {
	ctx := context.Background()
	workspace, initializer := testWorkspace(t, ctx)
	witnessPublic, witnessPrivate := testKey(t)
	if _, err := identity.DeclareWitness(ctx, workspace, initializer, witnessPublic, []string{identity.GitHubScheme}); err != nil {
		t.Fatal(err)
	}
	_, session := testKey(t)
	joined, err := workspace.Append(ctx, session, host.Act{Schema: "chess/join@0", Payload: []byte("{}")})
	if err != nil {
		t.Fatal(err)
	}
	alice := identity.Identity{Scheme: identity.GitHubScheme, Subject: "4242"}
	record, err := identity.Endorse(ctx, workspace, witnessPrivate, identity.Anchor{
		Genesis: "somebody-elses-log", Subject: joined.Actor, Identity: &alice,
	})
	if err != nil {
		t.Fatal(err)
	}

	log, err := workspace.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var written identity.Anchor
	if err := json.Unmarshal(record.Payload, &written); err != nil {
		t.Fatal(err)
	}
	if written.Genesis != log.Genesis {
		t.Errorf("recorded genesis = %q, want this repository's %q", written.Genesis, log.Genesis)
	}
	afterAnchor, err := workspace.Append(ctx, session, host.Act{Schema: "chess/move@0", Payload: []byte("e4")})
	if err != nil {
		t.Fatal(err)
	}
	log, err = workspace.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !identity.Resolve(log).LookupAt(afterAnchor.ID).Anchored {
		t.Error("an endorsement written for this repository did not resolve in it")
	}
}

// A witness declaration signed by anything but the initializing key is
// recorded like any other record and resolves to nothing. The refusal is the
// resolution's, not the append path's.
func TestWitnessDeclaredByTheWrongKeyIsRecordedAndIgnored(t *testing.T) {
	ctx := context.Background()
	workspace, _ := testWorkspace(t, ctx)
	_, impostor := testKey(t)
	witnessPublic, _ := testKey(t)

	record, err := identity.DeclareWitness(ctx, workspace, impostor, witnessPublic, []string{identity.GitHubScheme})
	if err != nil {
		t.Fatalf("the append itself should not refuse: %v", err)
	}
	if record.ID == "" {
		t.Fatal("no record was written")
	}
	if _, ok := resolveWorkspace(t, ctx, workspace).Witness(); ok {
		t.Fatal("a witness declared by the wrong key took force")
	}
}

func TestDeclareWitnessRefusesSomethingThatIsNotAKey(t *testing.T) {
	ctx := context.Background()
	workspace, initializer := testWorkspace(t, ctx)
	if _, err := identity.DeclareWitness(ctx, workspace, initializer, []byte("short"), []string{identity.GitHubScheme}); err == nil {
		t.Fatal("a witness key of the wrong size was accepted")
	}
	if _, err := identity.DeclareWitness(ctx, workspace, initializer, make([]byte, ed25519.PublicKeySize), nil); err == nil {
		t.Fatal("a witness that could witness for nothing was accepted")
	}
}

// GitHub's numeric account id is what an identity means, because that is the
// identifier GitHub does not reissue.
func TestGitHubCheckAnchorsToTheAccountIdNotTheLogin(t *testing.T) {
	ctx := context.Background()
	server := githubServer(t, "gho_valid", 99, "renamed-later")

	found, err := identity.GitHub{API: server.URL}.Check(ctx, "gho_valid")
	if err != nil {
		t.Fatal(err)
	}
	want := identity.Identity{Scheme: identity.GitHubScheme, Subject: "99", Handle: "renamed-later"}
	if found != want {
		t.Fatalf("identity = %+v, want %+v", found, want)
	}
}

func TestGitHubCheckRefusesWhatCannotBeAnIdentity(t *testing.T) {
	ctx := context.Background()
	cases := map[string]http.HandlerFunc{
		"no account id": func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"login":"alice"}`))
		},
		"unreadable answer": func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`not json`))
		},
		"control character in login": func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte("{\"id\":1,\"login\":\"a\\u0000b\"}"))
		},
	}
	for name, handler := range cases {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(handler)
			defer server.Close()
			if _, err := (identity.GitHub{API: server.URL}).Check(ctx, "gho_valid"); err == nil {
				t.Fatalf("%s was accepted as an identity", name)
			}
		})
	}
}

// A provider's refusal is reported as a status and nothing more. Its body is
// outside this program's control and can echo what was sent to it, so putting
// it in an error is a way of writing a credential somewhere it was never meant
// to go.
func TestGitHubCheckDoesNotCarryTheProvidersBodyOrTheTokenIntoAnError(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("bad credentials for " + r.Header.Get("Authorization")))
	}))
	defer server.Close()

	_, err := identity.GitHub{API: server.URL}.Check(ctx, "gho_secret")
	if err == nil {
		t.Fatal("a refused token was accepted")
	}
	if strings.Contains(err.Error(), "gho_secret") || strings.Contains(err.Error(), "bad credentials") {
		t.Fatalf("the error carried the token or the provider's body: %v", err)
	}
}

// A token is used in an Authorization header, so a token that could end that
// header and begin a line nobody wrote is refused before the request is built.
func TestGitHubCheckRefusesATokenThatCannotBeABearerCredential(t *testing.T) {
	ctx := context.Background()
	reached := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	defer server.Close()

	for _, token := range []string{"", "gho\r\nX-Injected: 1", "gho token", strings.Repeat("g", 513)} {
		if _, err := (identity.GitHub{API: server.URL}).Check(ctx, token); err == nil {
			t.Errorf("token %q was accepted", token)
		}
	}
	if reached {
		t.Error("a refused token still reached the provider")
	}
}

// A provider's answer is somebody else's, and a reader with no ceiling is a
// reader whose memory somebody else chooses. The answer here is perfectly well
// formed and simply enormous, so only the ceiling can refuse it: without one it
// would parse, and the identity inside it would be accepted.
func TestGitHubCheckBoundsWhatItReadsFromTheProvider(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":1,"login":"alice","padding":"`))
		chunk := strings.Repeat("x", 64*1024)
		for i := 0; i < 32; i++ {
			if _, err := w.Write([]byte(chunk)); err != nil {
				return
			}
		}
		w.Write([]byte(`"}`))
	}))
	defer server.Close()

	if _, err := (identity.GitHub{API: server.URL}).Check(ctx, "gho_valid"); err == nil {
		t.Fatal("an answer past the ceiling was accepted")
	}
}

// A provider writes its own status line, and HTTP/1.1 carries the reason
// phrase verbatim. A provider that quotes back what it was sent — by malice,
// or by a careless "unauthorized: <credential>" — hands this package a string
// with the caller's token in it, and reporting that string is how a credential
// reaches a log nobody meant to write it to. The rule is stronger than hiding
// the token: nothing from the status line is reported at all, so a provider
// cannot place text of its choosing in an error however it encodes it.
func TestGitHubCheckReportsNothingFromTheProvidersStatusLine(t *testing.T) {
	ctx := context.Background()
	const token = "gho_reason_phrase_secret"
	server := rawStatusServer(t, "401 Unauthorized-"+token)

	_, err := identity.GitHub{API: server}.Check(ctx, token)
	if err == nil {
		t.Fatal("a refused token was accepted")
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("the provider's reason phrase carried the token into an error: %v", err)
	}
	if !strings.Contains(err.Error(), "401 Unauthorized") {
		t.Errorf("error = %v, want the canonical status this program built", err)
	}
}

// The HTTP client is the deployment's to set, so the transport is not one this
// package chose and its error text is not text this package wrote. A transport
// that reports the request it was handed — a debugging round-tripper, a proxy
// wrapper, anything hostile — would otherwise publish the Authorization header
// through the error returned here.
func TestGitHubCheckReportsNothingFromATransportError(t *testing.T) {
	ctx := context.Background()
	const token = "gho_transport_secret"
	client := &http.Client{Transport: leakyTransport{token: token}}

	_, err := identity.GitHub{API: "https://provider.invalid", Client: client}.Check(ctx, token)
	if err == nil {
		t.Fatal("a failed transport was accepted")
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("the transport error carried the token into an error: %v", err)
	}
	if strings.Contains(err.Error(), "dial refused") {
		t.Errorf("the transport chose text in this error: %v", err)
	}
}

// A caller that cancels still learns that it was the cancellation, because
// that answer is the caller's own and not the transport's.
func TestGitHubCheckStillReportsTheCallersOwnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := &http.Client{Transport: leakyTransport{token: "gho_secret"}}

	_, err := identity.GitHub{API: "https://provider.invalid", Client: client}.Check(ctx, "gho_secret")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the caller's own cancellation", err)
	}
}

// leakyTransport stands in for any round-tripper that reports what it was
// asked to send.
type leakyTransport struct{ token string }

func (l leakyTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("dial refused while sending Authorization: Bearer %s", l.token)
}

// rawStatusServer answers with a status line written byte for byte, which
// httptest cannot do: its server composes the reason phrase from Go's own
// table, so it can never reproduce a provider that chose its own.
func rawStatusServer(t *testing.T, statusLine string) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				// Read only the request head. Reading to EOF would wait for a
				// client that is itself waiting for this answer.
				buffered := bufio.NewReader(conn)
				for {
					line, err := buffered.ReadString('\n')
					if err != nil || line == "\r\n" || line == "\n" {
						break
					}
				}
				io.WriteString(conn, "HTTP/1.1 "+statusLine+"\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
			}()
		}
	}()
	return "http://" + listener.Addr().String()
}

func githubServer(t *testing.T, wantToken string, id int64, login string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user" {
			t.Errorf("asked for %q, want /user", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+wantToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"id": id, "login": login})
	}))
	t.Cleanup(server.Close)
	return server
}
