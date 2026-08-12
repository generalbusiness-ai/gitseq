package observe

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type recorder struct{ values []Measurement }

func (r *recorder) Record(_ context.Context, value Measurement) { r.values = append(r.values, value) }

func TestDisabledBeginDoesNothing(t *testing.T) {
	if done := Begin(context.Background(), nil, OperationFold, PathCold); done != nil {
		t.Fatal("disabled observation allocated a completion function")
	}
}

func TestBeginUsesBoundedOutcome(t *testing.T) {
	record := new(recorder)
	done := Begin(context.Background(), record, OperationVerify, PathCheckpoint)
	done(errors.New("secret path and object id"))
	if len(record.values) != 1 || record.values[0].Outcome != OutcomeError || record.values[0].Duration < 0 {
		t.Fatalf("measurement = %+v", record.values)
	}
}

func TestGitPathIsFinite(t *testing.T) {
	for _, test := range []struct {
		arguments []string
		want      Path
	}{
		{[]string{"rev-list", "refs/gitseq/private-id"}, PathScan},
		{[]string{"update-ref", "refs/gitseq/private-id"}, PathRef},
		{[]string{"hash-object", "-w"}, PathWrite},
		{[]string{"unknown", "/private/repo"}, PathOther},
	} {
		if got := GitPath(test.arguments); got != test.want {
			t.Fatalf("GitPath(%q) = %q, want %q", test.arguments, got, test.want)
		}
	}
}

// The route label is the one metric attribute fed from a value the handler did
// not choose, so it is the one that can grow without bound. routeTemplate's
// length ceiling and character allowlist are what keep it finite if a future
// router ever puts request-derived text in Pattern. Nothing held that guard
// before: deleting it entirely left this package and internal/telemetry green.
func TestRouteTemplateIsBounded(t *testing.T) {
	for _, test := range []struct {
		name    string
		pattern string
		want    string
	}{
		{"a registered template passes through", "GET /actors/{fingerprint}", "GET /actors/{fingerprint}"},
		{"an unmatched request is named, not blank", "", "unmatched"},
		{"a disallowed character collapses", "GET /actors/private?secret=1", "other"},
		{"a percent-encoded byte collapses", "GET /actors/%2e%2e", "other"},
		{"a non-ASCII byte collapses", "GET /actors/caf\u00e9", "other"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := routeTemplate(test.pattern); got != test.want {
				t.Fatalf("routeTemplate(%q) = %q, want %q", test.pattern, got, test.want)
			}
		})
	}

	// The ceiling is a boundary, so pin both sides of it. Testing only a wildly
	// over-length pattern would still pass if someone raised the limit.
	if got := routeTemplate(strings.Repeat("a", 96)); got != strings.Repeat("a", 96) {
		t.Fatalf("a pattern exactly at the ceiling collapsed: %q", got)
	}
	if got := routeTemplate(strings.Repeat("a", 97)); got != "other" {
		t.Fatalf("a pattern one over the ceiling survived: %q", got)
	}
}

func TestHTTPHandlerUsesRouteTemplateNotRawPath(t *testing.T) {
	record := new(recorder)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /actors/{fingerprint}", func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusNoContent) })
	request := httptest.NewRequest(http.MethodGet, "/actors/private-actor-id", nil)
	HTTPHandler(record, mux).ServeHTTP(httptest.NewRecorder(), request)
	if len(record.values) != 1 || record.values[0].Route != "GET /actors/{fingerprint}" || record.values[0].Status != 2 {
		t.Fatalf("HTTP measurement = %+v", record.values)
	}
	if record.values[0].Route == request.URL.Path {
		t.Fatal("raw request path entered telemetry")
	}
}
