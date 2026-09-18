package residentclient

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// The deadline is decided in one place, so this is where the precedence between
// what an author typed, what started the instance, and what everything used
// before the deadline could be set at all is held down.
func TestResolveSubmitDeadlinePrefersExplicitThenEnvironment(t *testing.T) {
	t.Setenv(SubmitDeadlineEnvironment, "")
	if deadline, err := ResolveSubmitDeadline(""); err != nil || deadline != DefaultSubmitDeadline {
		t.Fatalf("unset deadline = %s, %v, want %s", deadline, err, DefaultSubmitDeadline)
	}
	if deadline, err := ResolveSubmitDeadline("90s"); err != nil || deadline != 90*time.Second {
		t.Fatalf("explicit deadline = %s, %v", deadline, err)
	}
	t.Setenv(SubmitDeadlineEnvironment, " 2m ")
	if deadline, err := ResolveSubmitDeadline(""); err != nil || deadline != 2*time.Minute {
		t.Fatalf("environment deadline = %s, %v", deadline, err)
	}
	if deadline, err := ResolveSubmitDeadline("30s"); err != nil || deadline != 30*time.Second {
		t.Fatalf("explicit value did not win over the environment: %s, %v", deadline, err)
	}
}

// A value nobody can read is refused rather than replaced by the default.
// Somebody who writes one meant something by it, and silently keeping the old
// deadline is the failure they were trying to avoid.
func TestResolveSubmitDeadlineRefusesWhatItCannotUse(t *testing.T) {
	t.Setenv(SubmitDeadlineEnvironment, "")
	for _, value := range []string{"soon", "30", "0", "-5s", "0s"} {
		deadline, err := ResolveSubmitDeadline(value)
		if err == nil {
			t.Fatalf("accepted %q as a deadline of %s", value, deadline)
		}
		if !strings.Contains(err.Error(), "--deadline") || !strings.Contains(err.Error(), "30s") {
			t.Fatalf("refusal of %q does not name the flag and an accepted form: %v", value, err)
		}
	}
	// The environment is refused in the same terms, naming itself rather than
	// the flag, because that is where the reader has to go to fix it.
	t.Setenv(SubmitDeadlineEnvironment, "later")
	err := func() error { _, err := ResolveSubmitDeadline(""); return err }()
	if err == nil || !strings.Contains(err.Error(), SubmitDeadlineEnvironment) {
		t.Fatalf("environment refusal = %v", err)
	}
}

type fakeTimeout struct{}

func (fakeTimeout) Error() string   { return "i/o timeout" }
func (fakeTimeout) Timeout() bool   { return true }
func (fakeTimeout) Temporary() bool { return false }

// Whether the deadline expired decides what a caller is told to do next, so the
// two failures must not be confused: a refusal is definite, an expiry is not.
func TestTimedOutSeparatesAnExpiredDeadlineFromARefusal(t *testing.T) {
	var timeout net.Error = fakeTimeout{}
	for _, err := range []error{context.DeadlineExceeded, os.ErrDeadlineExceeded, timeout,
		&TransportError{Err: context.DeadlineExceeded}, &ReadError{Err: timeout}} {
		if !TimedOut(err) {
			t.Fatalf("expired deadline %v was read as a refusal", err)
		}
	}
	for _, err := range []error{errors.New("connection refused"), context.Canceled,
		&HTTPError{StatusCode: 400, Message: "refused"}} {
		if TimedOut(err) {
			t.Fatalf("refusal %v was read as an expired deadline", err)
		}
	}
}
