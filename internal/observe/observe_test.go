package observe

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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
