package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

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
			// None of these cases is about the timeout, so the worker is given
			// one it cannot reach: a five-second contract left the exit status
			// this test reads at the mercy of how long a loaded machine takes
			// to start a shell.
			binary := workerScript(t, "printf '%s\\n' 'worker diagnostic' >&2\n"+
				"printf '%s\\n' '"+test.stdout+"'\nexit "+strconv.Itoa(test.exitCode)+"\n")
			selected := runCase{Scenario: "cold_status", ActorCount: 1, Fanout: 1}
			contract := perflane.Contract{TimeoutSeconds: map[string]int{"cold_status": workerAmpleTimeout}}
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

// workerSleepSeconds is how long a timeout fixture sleeps: far longer than the
// one-second worker timeout, so a worker that outlives its deadline cannot be
// mistaken for one that was stopped, and bounded, so a test that does hang
// ends rather than waiting for the package deadline.
const workerSleepSeconds = 30

// workerReturnBudget is well under that sleep. It says nothing about which
// route stopped the worker; what returning inside it proves is that every
// write end of the captured streams was closed, because Run cannot return
// until the goroutines copying stdout and stderr into their buffers have seen
// end of file on both. A sleeping child left holding those pipes would hold
// Run open to the end of the sleep.
const workerReturnBudget = 10 * time.Second

// workerAmpleTimeout is a contract timeout no fixture can reach: it is for the
// tests that are about something other than the timeout, so that a slow start
// on a loaded machine cannot turn their subject into a killed process. Only a
// test whose subject is the timeout gives the worker one second.
const workerAmpleTimeout = 60

// notTimedOut names why an error is not the failure of a worker its timeout
// stopped, and returns the empty string when it is one. A cancelled worker
// fails in exactly two ways, and which one it is depends on machine load
// rather than on anything worth asserting: a process that started is killed,
// and one that never started is refused with the deadline itself. Neither
// depends on the child having emitted a diagnostic first, which is why nothing
// here reads stream content: under load the worker can be killed before its
// first write, or before it runs at all. That diagnostics are retained on a
// process failure is proved deterministically by the process-failure case
// above, where the worker speaks and then exits on its own.
func notTimedOut(err error, elapsed time.Duration) string {
	if !killedBySignal(err) && !errors.Is(err, context.DeadlineExceeded) {
		return "the worker was not stopped by its timeout"
	}
	if elapsed >= workerReturnBudget {
		return fmt.Sprintf("the worker's captured streams stayed open for %s, past the %s budget", elapsed, workerReturnBudget)
	}
	return ""
}

// killedBySignal reports whether an error is the exit status of a process the
// runtime killed rather than one that chose its own status. An ordinary
// nonzero exit is an *exec.ExitError too, and proves nothing about the
// timeout, so what is read is the signal in the process state, never the type
// of the error. exec.CommandContext cancels by killing.
func killedBySignal(err error) bool {
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return false
	}
	status, held := exit.ProcessState.Sys().(syscall.WaitStatus)
	return held && status.Signaled() && status.Signal() == syscall.SIGKILL
}

func timedOut(t *testing.T, err error, elapsed time.Duration) {
	t.Helper()
	if reason := notTimedOut(err, elapsed); reason != "" {
		t.Fatalf("%s: %v", reason, err)
	}
}

func TestWorkerProcessRetainsTimeout(t *testing.T) {
	sleep := "exec sleep " + strconv.Itoa(workerSleepSeconds) + "\n"
	for _, test := range []struct {
		name   string
		body   string
		silent bool
	}{
		// exec replaces the shell, so cancellation kills the worker itself and
		// does not leave a sleeping child holding the output pipes open.
		{name: "diagnostic before the sleep", body: "printf '%s\\n' 'worker waiting' >&2\n" + sleep},
		// The same enforcement where the child says nothing at all, which is
		// what a worker killed before its first write looks like from here.
		{name: "silent worker", body: sleep, silent: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			binary := workerScript(t, test.body)
			selected := runCase{Scenario: "cold_status", ActorCount: 1, Fanout: 1}
			contract := perflane.Contract{TimeoutSeconds: map[string]int{"cold_status": 1}}
			started := time.Now()
			_, err := runWorker(context.Background(), binary, "unused fixture", selected, contract, "", false)
			timedOut(t, err, time.Since(started))
			if test.silent && !strings.HasSuffix(err.Error(), "stdout: ; stderr: ") {
				t.Fatalf("the silent fixture said something, so it is not the no-diagnostic control: %v", err)
			}
		})
	}
}

// The before-start half of the same enforcement. A context already past its
// deadline when runWorker is called stops the worker in exec's own check,
// before the process exists: there is no exit status, and no chance of a
// diagnostic. The fixture would succeed at once if it ran, so a worker that
// did start is unmistakable here.
func TestWorkerProcessReportsAnExpiredDeadlineBeforeStarting(t *testing.T) {
	t.Parallel()
	binary := workerScript(t, "printf '%s\\n' '{\"actor_count\":1,\"dependency_fanout\":1}'\n")
	expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	selected := runCase{Scenario: "cold_status", ActorCount: 1, Fanout: 1}
	contract := perflane.Contract{TimeoutSeconds: map[string]int{"cold_status": workerAmpleTimeout}}
	started := time.Now()
	result, err := runWorker(expired, binary, "unused fixture", selected, contract, "", false)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("an expired deadline did not stop the worker: %v", err)
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		t.Fatalf("the worker ran and exited despite the expired deadline: %v", err)
	}
	if result.ActorCount != 0 || result.Fanout != 0 {
		t.Fatalf("a worker that never started returned a result: %+v", result)
	}
	if elapsed := time.Since(started); elapsed >= workerReturnBudget {
		t.Fatalf("the refusal took %s", elapsed)
	}
}

// The oracle above must not take an ordinary failure for a cancellation. A
// worker that exits nonzero on its own returns an *exec.ExitError and returns
// it at once, so accepting the type alone, or the promptness alone, would let
// a removed timeout pass as an enforced one. Both fixtures are rejected here,
// whether or not the worker spoke first, because a diagnostic is no part of
// the question.
//
// The subject here is how an exit status is classified, not the timeout, so
// the worker is given a timeout it cannot reach. With the one second the
// timeout tests use, a loaded machine that is slow to start a shell kills the
// fixture before it can exit 7, and the control loses the very status it is
// about.
func TestTimeoutOracleRejectsAnOrdinaryExit(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "silent", body: "exit 7\n"},
		{name: "with a diagnostic", body: "printf '%s\\n' 'worker waiting' >&2\nexit 7\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			binary := workerScript(t, test.body)
			selected := runCase{Scenario: "cold_status", ActorCount: 1, Fanout: 1}
			contract := perflane.Contract{TimeoutSeconds: map[string]int{"cold_status": workerAmpleTimeout}}
			started := time.Now()
			_, err := runWorker(context.Background(), binary, "unused fixture", selected, contract, "", false)
			elapsed := time.Since(started)
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 7 {
				t.Fatalf("the fixture did not exit 7 of its own accord, so it is not the control it claims to be: %v", err)
			}
			if elapsed >= workerReturnBudget {
				t.Fatalf("the fixture took %s to exit, so promptness alone would not have accepted it", elapsed)
			}
			if reason := notTimedOut(err, elapsed); reason == "" {
				t.Fatalf("the timeout oracle accepted an ordinary exit 7 as a cancellation: %v", err)
			}
		})
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
