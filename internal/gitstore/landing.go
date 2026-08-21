package gitstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Landing statuses. "absent" and "unknown" are deliberately different words:
// a check that could not run is not evidence that the commit is missing.
const (
	LandingLanded  = "landed"
	LandingAbsent  = "absent"
	LandingUnknown = "unknown"
)

// Landing answers, for one commit, whether a branch already carries it. The
// fold knows whether a commitment closed; only git knows whether the code
// landed, and a thread can be in either state without the other.
//
// Status keeps "no" and "cannot tell" apart on purpose. `git merge-base
// --is-ancestor` exits 1 for a commit that is genuinely not an ancestor and
// 128 for a commit this clone has never heard of, and collapsing the two
// turns a command that never ran into a confident negative — the exact fault
// that left a false sentence standing in a design note for seven days.
type Landing struct {
	Commit string `json:"commit"`
	Status string `json:"status"`
	// The merge that brought the commit onto the branch, when there was one.
	// A fast-forwarded commit sits on the branch with no merge above it.
	Merge string `json:"merge,omitempty"`
	// Commit time of the merge, or of the commit itself when it arrived
	// without one. Zero when unknown.
	Time int64 `json:"time,omitempty"`
	// Why the answer is unknown, in git's own words. Empty otherwise.
	Reason string `json:"reason,omitempty"`
}

// A commit name reaches git's argv, so it is checked against the only shape a
// commit ever has rather than trusted. Anything else is refused before a
// process starts: no leading dash can become an option, and no separator can
// become a second argument.
var commitName = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// LandingLimit bounds one batch. A thread asks about the handful of heads on
// its own rail, not about the log.
const LandingLimit = 16

// Landings reports, for each commit, whether branch carries it. The branch is
// chosen by the caller, never by a browser; the commits are validated here.
// Order matches the input, and a malformed name is answered rather than
// dropped, so a caller can always pair request with response.
func (s Store) Landings(ctx context.Context, branch string, commits []string) []Landing {
	if len(commits) > LandingLimit {
		commits = commits[:LandingLimit]
	}
	landings := make([]Landing, 0, len(commits))
	for _, commit := range commits {
		landings = append(landings, s.landing(ctx, branch, commit))
	}
	return landings
}

func (s Store) landing(ctx context.Context, branch, commit string) Landing {
	if !commitName.MatchString(commit) {
		return Landing{Commit: commit, Status: LandingUnknown, Reason: "not a commit name"}
	}
	code, err := s.exitCode(ctx, "merge-base", "--is-ancestor", commit, branch)
	if err != nil {
		return Landing{Commit: commit, Status: LandingUnknown, Reason: err.Error()}
	}
	if code == 1 {
		return Landing{Commit: commit, Status: LandingAbsent}
	}

	landing := Landing{Commit: commit, Status: LandingLanded}
	// The last merge on the ancestry path is the one that brought the commit
	// onto the branch. There is none when the branch was fast-forwarded onto
	// the commit itself, which is an ordinary way to land.
	output, err := s.run(ctx, nil, nil,
		"rev-list", "--ancestry-path", "--merges", "--format=%H%x1f%at", commit+".."+branch)
	if err == nil {
		if merge, when, ok := lastMerge(string(output)); ok {
			landing.Merge, landing.Time = merge, when
			return landing
		}
	}
	if output, err := s.run(ctx, nil, nil, "log", "-1", "--format=%at", commit); err == nil {
		if seconds, err := strconv.ParseInt(strings.TrimSpace(string(output)), 10, 64); err == nil {
			landing.Time = seconds
		}
	}
	return landing
}

// lastMerge reads the oldest merge out of `rev-list --format`, whose output
// interleaves a "commit <sha>" header line with each formatted line. Newest
// first is rev-list's order, so the last formatted line is the merge that
// landed the commit.
func lastMerge(output string) (string, int64, bool) {
	var hash string
	var when int64
	var found bool
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Split(strings.TrimSpace(line), "\x1f")
		if len(fields) != 2 || !commitName.MatchString(fields[0]) {
			continue
		}
		hash, found = fields[0], true
		if seconds, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
			when = seconds
		} else {
			when = 0
		}
	}
	return hash, when, found
}

// exitCode runs git for its verdict rather than its output. A non-zero exit is
// an answer here, not a failure, so it is returned as a number; only a git
// that could not be started at all is an error.
func (s Store) exitCode(ctx context.Context, args ...string) (int, error) {
	cmd := exec.CommandContext(ctx, "git", storeGitArguments(s.Repo, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return 0, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if exit.ExitCode() > 1 {
			return exit.ExitCode(), fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(stderr.String()))
		}
		return exit.ExitCode(), nil
	}
	return -1, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
}
