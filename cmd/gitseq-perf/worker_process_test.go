package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/perflane"
)

func TestWorkerProcessKeepsDiagnosticsOutOfResult(t *testing.T) {
	for _, test := range []struct {
		name     string
		stdout   string
		exitCode int
		wantErr  string
	}{
		{name: "success", stdout: `{"actor_count":1,"dependency_fanout":1}`},
		{name: "process failure", stdout: `{"actor_count":1,"dependency_fanout":1}`, exitCode: 7, wantErr: "worker: exit status 7"},
		{name: "invalid JSON", stdout: `not JSON`, wantErr: "decode worker result:"},
		{name: "unknown field", stdout: `{"actor_count":1,"dependency_fanout":1,"unexpected":true}`, wantErr: `unknown field "unexpected"`},
		{name: "wrong result axes", stdout: `{"actor_count":2,"dependency_fanout":1}`, wantErr: "actor_count = 2, want 1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			// All strings here are fixed test data. The executable emits a
			// diagnostic on every path, including a successful JSON result.
			binary := workerScript(t, "printf '%s\\n' 'worker diagnostic' >&2\n"+
				"printf '%s\\n' '"+test.stdout+"'\nexit "+strconv.Itoa(test.exitCode)+"\n")
			selected := runCase{Scenario: "cold_status", ActorCount: 1, Fanout: 1}
			contract := perflane.Contract{TimeoutSeconds: map[string]int{"cold_status": 5}}
			result, err := runWorker(context.Background(), binary, "unused fixture", selected, contract, "", false)
			if test.wantErr == "" {
				if err != nil || result.ActorCount != 1 || result.Fanout != 1 {
					t.Fatalf("worker actor_count=%d, dependency_fanout=%d, error=%v", result.ActorCount, result.Fanout, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("error = %v, want %q", err, test.wantErr)
			}
			if test.exitCode != 0 {
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != test.exitCode {
					t.Fatalf("worker exit status was lost: %v", err)
				}
			}
			if test.name != "wrong result axes" && !strings.Contains(err.Error(), "worker diagnostic") {
				t.Fatalf("failure lost stderr: %v", err)
			}
		})
	}
}

func TestWorkerProcessRetainsTimeout(t *testing.T) {
	// exec replaces the shell, so cancellation kills the worker itself and
	// does not leave a sleeping child holding the output pipes open.
	binary := workerScript(t, "printf '%s\\n' 'worker waiting' >&2\nexec sleep 30\n")
	selected := runCase{Scenario: "cold_status", ActorCount: 1, Fanout: 1}
	contract := perflane.Contract{TimeoutSeconds: map[string]int{"cold_status": 1}}
	_, err := runWorker(context.Background(), binary, "unused fixture", selected, contract, "", false)
	var exit *exec.ExitError
	if !errors.As(err, &exit) || !strings.Contains(err.Error(), "worker waiting") {
		t.Fatalf("timed-out worker lost process failure or diagnostics: %v", err)
	}
}

func workerScript(t *testing.T, body string) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "worker with spaces")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
	return binary
}
