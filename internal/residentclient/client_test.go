package residentclient

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
)

func TestResolveActor(t *testing.T) {
	t.Setenv(ActorEnvironment, " from-environment ")
	if got, err := ResolveActor("--as", "explicit"); err != nil || got != "explicit" {
		t.Fatalf("explicit actor = %q, %v", got, err)
	}
	if got, err := ResolveActor("--as", ""); err != nil || got != "from-environment" {
		t.Fatalf("environment actor = %q, %v", got, err)
	}
	t.Setenv(ActorEnvironment, " ")
	if _, err := ResolveActor("--actor", ""); err == nil || !strings.Contains(err.Error(), "--actor") || !strings.Contains(err.Error(), ActorEnvironment) {
		t.Fatalf("missing actor error = %v", err)
	}
}

func TestValidateURL(t *testing.T) {
	for _, raw := range []string{"http://localhost:7777", "http://127.0.0.1:7777/", "http://[::1]:7777"} {
		if _, err := ValidateURL(raw); err != nil {
			t.Errorf("ValidateURL(%q): %v", raw, err)
		}
	}
	for _, raw := range []string{"https://127.0.0.1:7777", "http://example.com", "http://user@127.0.0.1:7777", "http://127.0.0.1:7777/path", "http://127.0.0.1:7777?query"} {
		if _, err := ValidateURL(raw); err == nil {
			t.Errorf("ValidateURL(%q) succeeded", raw)
		}
	}
}

func TestClientRefusesRedirectAndBoundsResponse(t *testing.T) {
	var followed atomic.Bool
	destination := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		followed.Store(true)
		_, _ = writer.Write([]byte(`{"ok":true}`))
	}))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, destination.URL, http.StatusFound)
	}))
	defer source.Close()
	client := NewWithHTTP(source.Client(), time.Second)
	var target struct {
		OK bool `json:"ok"`
	}
	if err := client.GetJSON(context.Background(), source.URL, "/v0/status", 64, &target); !IsTransportError(err) {
		t.Fatalf("redirect error = %T %v, want transport error", err, err)
	}
	if followed.Load() {
		t.Fatal("resident redirect was followed")
	}

	oversize := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"value":"too large"}`))
	}))
	defer oversize.Close()
	if err := NewWithHTTP(oversize.Client(), time.Second).GetJSON(context.Background(), oversize.URL, "/v0/status", 8, &target); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversize response error = %v", err)
	}
}

func TestClientRequiresOneStrictJSONValue(t *testing.T) {
	for name, response := range map[string]string{
		"unknown field":  `{"ok":true,"extra":1}`,
		"trailing value": `{"ok":true} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				_, _ = writer.Write([]byte(response))
			}))
			defer server.Close()
			var target struct {
				OK bool `json:"ok"`
			}
			if err := NewWithHTTP(server.Client(), time.Second).GetJSON(context.Background(), server.URL, "/v0/status", 128, &target); err == nil {
				t.Fatal("malformed response was accepted")
			}
		})
	}
}

func TestSubmitUsesGuardedResidentRoundTrip(t *testing.T) {
	want := app.Submission{}
	want.Result.Commit = "commit"
	want.Record.ID = "event"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/v0/submit" || request.Header.Get("Content-Type") != "application/json" {
			t.Errorf("request = %s %s content-type %q", request.Method, request.URL.Path, request.Header.Get("Content-Type"))
		}
		var submitted kernel.Request
		if err := json.NewDecoder(request.Body).Decode(&submitted); err != nil {
			t.Error(err)
		}
		_ = json.NewEncoder(writer).Encode(want)
	}))
	defer server.Close()
	got, err := NewWithHTTP(server.Client(), time.Second).Submit(context.Background(), nil, server.URL, kernel.Request{})
	if err != nil || got.Result.Commit != "commit" || got.Record.ID != "event" {
		t.Fatalf("Submit() = %#v, %v", got, err)
	}
}

func TestHTTPRefusalIsNotTransportLoss(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(`{"error":"refused"}`))
	}))
	defer server.Close()
	_, err := NewWithHTTP(server.Client(), time.Second).Submit(context.Background(), nil, server.URL, kernel.Request{})
	var refusal *HTTPError
	if !errors.As(err, &refusal) || IsTransportError(err) || err.Error() != "refused" {
		t.Fatalf("refusal = %T %v", err, err)
	}
}

// Only a refused dial says an ownership claim is stale. Every other answer
// leaves the claim alone, because taking a repository away from a resident that
// is actually serving is the split room the claim exists to prevent, while
// refusing to start only costs one operator a message.
func TestProbeResidentTreatsOnlyARefusedDialAsDefinitive(t *testing.T) {
	ctx := context.Background()
	client := New(2 * time.Second)

	alive := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v0/identity" {
			t.Errorf("probe asked for %q", request.URL.Path)
		}
		if request.Method != http.MethodGet {
			t.Errorf("probe used %s", request.Method)
		}
		_ = json.NewEncoder(writer).Encode(map[string]string{"genesis": "abc123"})
	}))
	defer alive.Close()
	if got := client.ProbeResident(ctx, app.ResidentClaim{URL: alive.URL, Genesis: "abc123"}); got != app.Alive {
		t.Fatalf("a resident answering for this workroom probed %v", got)
	}
	if got := client.ProbeResident(ctx, app.ResidentClaim{URL: alive.URL, Genesis: "another"}); got != app.Ambiguous {
		t.Fatalf("a port answering for another workroom probed %v; it must not authorize a takeover", got)
	}

	silent := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("this is not the identity answer"))
	}))
	defer silent.Close()
	if got := client.ProbeResident(ctx, app.ResidentClaim{URL: silent.URL, Genesis: "abc123"}); got != app.Ambiguous {
		t.Fatalf("an unparseable answer probed %v", got)
	}

	// A listener that accepts and then never answers is the wedged incumbent.
	// It is still an incumbent.
	hung, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer hung.Close()
	go func() {
		for {
			connection, err := hung.Accept()
			if err != nil {
				return
			}
			defer connection.Close()
		}
	}()
	brief, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	if got := client.ProbeResident(brief, app.ResidentClaim{URL: "http://" + hung.Addr().String(), Genesis: "abc123"}); got != app.Ambiguous {
		t.Fatalf("a hung listener probed %v", got)
	}

	// Nothing listening at all. This is the only case that frees a claim.
	dead, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := dead.Addr().String()
	if err := dead.Close(); err != nil {
		t.Fatal(err)
	}
	if got := client.ProbeResident(ctx, app.ResidentClaim{URL: "http://" + address, Genesis: "abc123"}); got != app.Dead {
		t.Fatalf("a refused dial probed %v; stale-owner recovery depends on it", got)
	}
}

// A claim is an ordinary file in the repository, so its URL is untrusted input.
// Probing whatever it named would turn starting a service into a request
// forgery against any address a local writer chose.
func TestProbeResidentRefusesToDialAnythingButLoopback(t *testing.T) {
	client := New(time.Second)
	for _, url := range []string{
		"http://198.51.100.7:7777",
		"http://example.invalid",
		"https://127.0.0.1:7777",
		"http://user:secret@127.0.0.1:7777",
		"http://127.0.0.1:7777/v0/act",
		"",
	} {
		if got := client.ProbeResident(context.Background(), app.ResidentClaim{URL: url, Genesis: "abc123"}); got != app.Ambiguous {
			t.Errorf("claim URL %q probed %v; it must not be dialled or believed", url, got)
		}
	}
}
