package main

import (
	"context"
	"strings"
	"testing"
)

// A malformed invocation is answered with the command's own usage and one
// worked example, and nothing else happens: no log, no key, no act. These are
// the four shapes a caller gets wrong — an undefined flag, a missing required
// flag, a positional offered as a flag, and an event reference that names
// nothing — and each has to end the same way.
func TestMalformedInvocationPrintsUsageWithAnExample(t *testing.T) {
	for _, probe := range []struct {
		name      string
		run       func(fixture preflightFixture) error
		wantError []string
		wantUsage []string
	}{
		{
			name: "undefined flag",
			run: func(fixture preflightFixture) error {
				return stateCommand(context.Background(), []string{
					"--repo", fixture.repo, "--as", "worker", "--kind", "assert", "--text", "x", "--subject", "y",
				})
			},
			wantError: []string{"not defined"},
			wantUsage: []string{"usage: gs state", "Example:", "gs state --as bot"},
		},
		{
			name: "missing required flag",
			run: func(fixture preflightFixture) error {
				return mergeCommand(context.Background(), []string{"--repo", fixture.repo, "--as", "worker"})
			},
			wantError: []string{"merge requires --checkout, --candidate, and --approval"},
			wantUsage: []string{"usage: gs merge", "Example:", "gs merge --as bot"},
		},
		{
			name: "missing statement text",
			run: func(fixture preflightFixture) error {
				return stateCommand(context.Background(), []string{
					"--repo", fixture.repo, "--as", "worker", "--kind", "promise", "--rests-on", fixture.request,
				})
			},
			wantError: []string{"--text or --text-file is required"},
			wantUsage: []string{"usage: gs state", "Example:", "gs state --as bot"},
		},
		{
			name: "no signing identity",
			run: func(fixture preflightFixture) error {
				return ratifyCommand(context.Background(), []string{"--repo", fixture.repo, fixture.report})
			},
			wantError: []string{"--as"},
			wantUsage: []string{"usage: gs ratify", "Example:", "gs ratify --as alice"},
		},
		{
			name: "no positional at all",
			run: func(fixture preflightFixture) error {
				return ratifyCommand(context.Background(), []string{"--repo", fixture.repo, "--as", "operator"})
			},
			wantError: []string{"ratify requires one target event"},
			wantUsage: []string{"usage: gs ratify [flags] <target-event>", "Example:", "gs ratify --as alice"},
		},
		{
			name: "positional given as a flag",
			run: func(fixture preflightFixture) error {
				return ratifyCommand(context.Background(), []string{
					"--repo", fixture.repo, "--as", "operator", "--target", fixture.report,
				})
			},
			wantError: []string{"gs ratify takes its <target-event> as a positional argument, not --target"},
			wantUsage: []string{"usage: gs ratify", "Example:"},
		},
		{
			name: "supersede target given as a flag",
			run: func(fixture preflightFixture) error {
				return supersedeCommand(context.Background(), []string{
					"--repo", fixture.repo, "--as", "operator", "--text", "retire it", "--target=" + fixture.report,
				})
			},
			wantError: []string{"gs supersede takes its <target-event> as a positional argument, not --target"},
			wantUsage: []string{"usage: gs supersede", "Example:"},
		},
		{
			name: "read command's event given as a flag",
			run: func(fixture preflightFixture) error {
				return inspectCommand(context.Background(), []string{
					"--repo", fixture.repo, "--event", fixture.report,
				})
			},
			wantError: []string{"gs inspect takes its <event> as a positional argument, not --event"},
			wantUsage: []string{"usage: gs inspect", "Example:", "gs inspect"},
		},
		{
			name: "event reference that names nothing",
			run: func(fixture preflightFixture) error {
				return ratifyCommand(context.Background(), []string{
					"--repo", fixture.repo, "--as", "operator", "#4000",
				})
			},
			wantError: []string{"#4000"},
			wantUsage: []string{"usage: gs ratify", "Example:", "gs ratify --as alice"},
		},
		{
			name: "basis reference that names nothing",
			run: func(fixture preflightFixture) error {
				return stateCommand(context.Background(), []string{
					"--repo", fixture.repo, "--as", "worker", "--kind", "assert",
					"--text", "x", "--rests-on", "#4000",
				})
			},
			wantError: []string{"#4000"},
			wantUsage: []string{"usage: gs state", "Example:"},
		},
	} {
		t.Run(probe.name, func(t *testing.T) {
			// A command with no --as reads the environment for the identity,
			// and a shell that names one would hide the refusal under test.
			t.Setenv(actorEnvironment, "")
			fixture := newPreflightFixture(t)
			before := fixture.snapshot()
			printed, err := quiet(t, func() error {
				return fixture.withoutKey(t, "worker", func() error { return probe.run(fixture) })
			})
			if err == nil {
				t.Fatal("the malformed invocation was not refused")
			}
			for _, want := range probe.wantError {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("refusal %q does not say %q", err, want)
				}
			}
			for _, want := range probe.wantUsage {
				if !strings.Contains(printed, want) {
					t.Fatalf("usage output does not contain %q:\n%s", want, printed)
				}
			}
			if after := fixture.snapshot(); after.Head != before.Head || after.Depth != before.Depth {
				t.Fatalf("a malformed invocation moved the workroom: %s/%d -> %s/%d", before.Head, before.Depth, after.Head, after.Depth)
			}
		})
	}
}

// Every command names an example, and every example starts with that command.
// A worked example nobody keeps current is worse than none, so the shape is
// checked here rather than trusted.
func TestEveryCommandHasAnExample(t *testing.T) {
	for _, name := range strings.Split(commandNames, ", ") {
		example, known := commandExamples[name]
		if !known {
			t.Errorf("gs %s has no worked example", name)
			continue
		}
		if !strings.HasPrefix(example, "gs "+name+" ") && example != "gs "+name {
			t.Errorf("the example for gs %s is %q", name, example)
		}
	}
}
