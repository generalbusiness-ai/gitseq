package residentclient

import (
	"context"
	"encoding/json"
	"errors"
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
