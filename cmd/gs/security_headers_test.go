package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/service"
)

// The resident serves a browser UI, so every response it can produce is a
// response a browser renders — including the ones a wrapper produces by
// refusing to continue. This exercises the real composition from
// residentHandler rather than a chain assembled here, because the defect this
// pins is not a missing header but a header applied at the wrong depth: a
// policy installed inside TrustedHostHandler is absent from exactly the
// non-loopback mutation refusal, and a chain built for the test would hide
// that by construction. Both short-circuits are covered: the cleared-deadline
// failure in residentHTTPHandler and the host refusal in TrustedHostHandler.
func TestResidentResponsesCarryBrowserSecurityHeaders(t *testing.T) {
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "workroom")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	workspace, _, err := app.Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	server, err := service.New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	handler := residentHandler(nil, server.Handler())

	want := map[string]string{
		"Content-Security-Policy": "frame-ancestors 'none'",
		"X-Frame-Options":         "DENY",
		"X-Content-Type-Options":  "nosniff",
		"Referrer-Policy":         "no-referrer",
	}

	// Each case pins the status, the content type and a fragment of the body it
	// expects, not only the headers. Without that a case quietly stops reaching
	// the response path it is named after and still passes, which happened
	// three times in this file. /v0/status returned a 500 from the
	// cleared-deadline branch rather than an API read. The malformed-act case
	// never reached a route at all, because httptest.NewRequest defaults Host
	// to example.com, so TrustedHostHandler refused it as non-loopback with the
	// same 400 the last case expects for that reason. And the malformed-act row
	// then carried an empty body fragment, which strings.Contains satisfies for
	// any response whatsoever: a pin that pins nothing, in the very row added
	// to stop the second mistake.
	asset := firstEmbeddedAsset(t, handler)
	tests := []struct {
		name        string
		status      int
		contentType string
		body        string
		request     func() *http.Request
	}{
		{"embedded ui", http.StatusOK, "text/html; charset=utf-8", "<!doctype html>", func() *http.Request {
			return httptest.NewRequest(http.MethodGet, "/", nil)
		}},
		{"embedded asset", http.StatusOK, "text/css; charset=utf-8", "", func() *http.Request {
			return httptest.NewRequest(http.MethodGet, asset, nil)
		}},
		{"api read", http.StatusOK, "application/json", "genesis", func() *http.Request {
			return httptest.NewRequest(http.MethodGet, "/v0/identity", nil)
		}},
		{"cleared-deadline failure", http.StatusInternalServerError, "text/plain; charset=utf-8", "deadline could not be cleared", func() *http.Request {
			return httptest.NewRequest(http.MethodGet, "/v0/status", nil)
		}},
		{"unknown route", http.StatusNotFound, "text/plain; charset=utf-8", "404 page not found", func() *http.Request {
			return httptest.NewRequest(http.MethodGet, "/v0/no-such-route", nil)
		}},
		{"malformed act", http.StatusBadRequest, "application/json", "invalid character 'n' looking for beginning of object key string", func() *http.Request {
			request := httptest.NewRequest(http.MethodPost, "/v0/act", strings.NewReader("{not json"))
			request.Header.Set("Content-Type", "application/json")
			request.Host = "127.0.0.1:7777"
			return request
		}},
		{"non-loopback mutation refusal", http.StatusBadRequest, "application/json", "mutation host must resolve only to loopback", func() *http.Request {
			request := httptest.NewRequest(http.MethodPost, "/v0/act", strings.NewReader("{}"))
			request.Header.Set("Content-Type", "application/json")
			request.Host = "203.0.113.1:7777"
			return request
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, test.request())
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d: this case no longer reaches the path it names (body %q)", response.Code, test.status, response.Body.String())
			}
			if got := response.Header().Get("Content-Type"); got != test.contentType {
				t.Fatalf("Content-Type = %q, want %q: this case no longer reaches the path it names", got, test.contentType)
			}
			// An empty fragment would make this vacuous, so the rows that
			// cannot name stable content say so by having none and are held by
			// status and content type alone.
			if test.body != "" && !strings.Contains(response.Body.String(), test.body) {
				t.Fatalf("body = %q, want a mention of %q", response.Body.String(), test.body)
			}
			for name, value := range want {
				if got := response.Header().Get(name); got != value {
					t.Errorf("%s = %q, want %q", name, got, value)
				}
			}
		})
	}
}

// firstEmbeddedAsset reads an asset URL out of the served index rather than
// naming one, because Vite content-hashes the filenames and any rebuild would
// otherwise leave a test pointing at a file that no longer exists.
func firstEmbeddedAsset(t *testing.T, handler http.Handler) string {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	match := regexp.MustCompile(`/assets/[A-Za-z0-9._-]+\.css`).FindString(response.Body.String())
	if match == "" {
		t.Fatalf("no stylesheet asset referenced by the served index: %q", response.Body.String())
	}
	return match
}

// The policy must not have been bought by loosening the boundary it sits
// outside of. A non-loopback mutation still has to be refused, with its own
// status, content type and reason intact.
func TestBrowserSecurityHeadersLeaveLoopbackMutationProtectionIntact(t *testing.T) {
	refused := false
	inner := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { refused = true })
	handler := residentHandler(nil, service.TrustedHostHandler(inner))

	request := httptest.NewRequest(http.MethodPost, "/v0/act", strings.NewReader("{}"))
	request.Host = "203.0.113.1:7777"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if refused {
		t.Fatal("a non-loopback mutation reached the inner handler")
	}
	if response.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if !strings.Contains(response.Body.String(), "mutation host must resolve only to loopback") {
		t.Errorf("body = %q, want the loopback refusal", response.Body.String())
	}
	if got := response.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("the refusal lost the policy: X-Frame-Options = %q", got)
	}
}

// A loopback mutation must still reach the routes, so the wrapper is not
// refusing everything and passing the tests above by accident.
func TestBrowserSecurityHeadersDoNotBlockLoopbackMutations(t *testing.T) {
	reached := false
	inner := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		reached = true
		writer.WriteHeader(http.StatusNoContent)
	})
	handler := residentHandler(nil, service.TrustedHostHandler(inner))

	request := httptest.NewRequest(http.MethodPost, "/v0/act", strings.NewReader("{}"))
	request.Host = "127.0.0.1:7777"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if !reached {
		t.Fatal("a loopback mutation was refused")
	}
	if response.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
	if got := response.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("Referrer-Policy = %q, want no-referrer", got)
	}
}

// The profiler listens separately and never passes through residentHandler, so
// a policy wired only into that composition would miss it. pprof.Index is an
// HTML page, which is exactly the surface the policy exists for, and the
// documentation says every resident response carries the headers.
func TestProfilerResponsesCarryBrowserSecurityHeaders(t *testing.T) {
	handler := profilerHandler()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if !strings.Contains(response.Body.String(), "<html") {
		t.Fatalf("the profiler index is no longer an HTML page: %q", response.Body.String())
	}
	for name, value := range map[string]string{
		"Content-Security-Policy": "frame-ancestors 'none'",
		"X-Frame-Options":         "DENY",
		"X-Content-Type-Options":  "nosniff",
		"Referrer-Policy":         "no-referrer",
	} {
		if got := response.Header().Get(name); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
}
