package main

import (
	"context"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
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
			// The step commands take their subject as a positional argument too,
			// and share the same answer.
			name: "step command with no positional",
			run: func(fixture preflightFixture) error {
				return promiseCommand(context.Background(), []string{"--repo", fixture.repo, "--as", "worker"})
			},
			wantError: []string{"promise takes exactly one request event"},
			wantUsage: []string{"usage: gs promise [flags] <request>", "Example:", "gs promise --as bot"},
		},
		{
			name: "step command's positional given as a flag",
			run: func(fixture preflightFixture) error {
				return promiseCommand(context.Background(), []string{
					"--repo", fixture.repo, "--as", "worker", "--request", fixture.request,
				})
			},
			wantError: []string{"gs promise takes its <request> as a positional argument, not --request"},
			wantUsage: []string{"usage: gs promise", "Example:"},
		},
		{
			// Without a kind there is no act at all, and the vocabulary check
			// that would have said so runs after the key is read.
			name: "missing required kind",
			run: func(fixture preflightFixture) error {
				return stateCommand(context.Background(), []string{
					"--repo", fixture.repo, "--as", "worker", "--server", "-",
					"--text", "a fact", "--rests-on", fixture.genesis,
				})
			},
			wantError: []string{"state requires --kind"},
			wantUsage: []string{"usage: gs state", "Example:", "gs state --as bot"},
		},
		{
			name: "missing retirement reason",
			run: func(fixture preflightFixture) error {
				return supersedeCommand(context.Background(), []string{
					"--repo", fixture.repo, "--as", "worker", fixture.assert,
				})
			},
			wantError: []string{"supersede requires --text"},
			wantUsage: []string{"usage: gs supersede", "Example:"},
		},
		{
			name: "missing verdict",
			run: func(fixture preflightFixture) error {
				return reviewCommand(context.Background(), []string{
					"--repo", fixture.repo, "--as", "worker", "--checkout", fixture.repo,
					"--artifact", fixture.artifact, "--promise", fixture.promise, "--text", "looks right",
				})
			},
			wantError: []string{"review requires --verdict approved"},
			wantUsage: []string{"usage: gs review", "Example:"},
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

// A flag's value is not a flag, however it is spelled. Scanning the raw
// arguments could not tell the two apart and refused a legitimate retirement
// whose reason began with two dashes, while the joined spelling of the same
// thing passed. The parser decides what was a flag; this only reads its answer.
func TestAValueThatLooksLikeAFlagIsNotAMistake(t *testing.T) {
	for _, spelling := range [][]string{
		{"--text", "--target"},
		{"--text=--target"},
		{"--text", "-target"},
	} {
		t.Run(strings.Join(spelling, " "), func(t *testing.T) {
			fixture := newPreflightFixture(t)
			before := fixture.snapshot()
			arguments := append([]string{"--repo", fixture.repo, "--as", "worker"}, spelling...)
			if _, err := quiet(t, func() error {
				return supersedeCommand(fixture.ctx, append(arguments, fixture.assert))
			}); err != nil {
				t.Fatalf("a retirement whose reason looks like a flag was refused: %v", err)
			}
			if after := fixture.snapshot(); after.Depth != before.Depth+1 {
				t.Fatalf("the retirement did not land: depth %d -> %d", before.Depth, after.Depth)
			}
		})
	}
}

// The genuine mistake still gets the line that says where the value belongs.
func TestTheGenuinePositionalMistakeIsStillHelpful(t *testing.T) {
	fixture := newPreflightFixture(t)
	printed, err := quiet(t, func() error {
		return supersedeCommand(fixture.ctx, []string{
			"--repo", fixture.repo, "--as", "worker", "--text", "retire it", "--target", fixture.assert,
		})
	})
	if err == nil || !strings.Contains(err.Error(), "takes its <target-event> as a positional argument, not --target") {
		t.Fatalf("error = %v", err)
	}
	if !strings.Contains(printed, "Example:") {
		t.Fatalf("no example was printed:\n%s", printed)
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

// The step commands sign acts like any other, so a caller who did not say who
// they are, or who named an event this workroom does not hold, gets the same
// answer: the command's flags, one worked example, a non-zero exit, and a log
// that has not moved. Each of the four is checked, because each opens its
// session and resolves its own references.
func TestStepCommandsAnswerAMalformedInvocationWithUsage(t *testing.T) {
	const nowhere = "git:sha1:bad#git:sha1:bad"
	for _, probe := range []struct {
		name      string
		run       func(fixture preflightFixture, actor []string) error
		wantError []string
		wantUsage []string
	}{
		{
			name: "promise",
			run: func(fixture preflightFixture, actor []string) error {
				arguments := append([]string{"--repo", fixture.repo, "--server", "-"}, actor...)
				return promiseCommand(fixture.ctx, append(arguments, nowhere))
			},
			wantError: []string{"names no statement in this workroom"},
			wantUsage: []string{"usage: gs promise [flags] <request>", "Example:", "gs promise --as bot"},
		},
		{
			name: "artifact",
			run: func(fixture preflightFixture, actor []string) error {
				arguments := append([]string{
					"--repo", fixture.repo, "--server", "-",
					"--head", fixture.workspace.View().Genesis, "--promise", nowhere,
				}, actor...)
				return artifactCommand(fixture.ctx, append(arguments, "notes/one.md"))
			},
			wantError: []string{"--promise", "names no statement in this workroom"},
			wantUsage: []string{"usage: gs artifact [flags] <path…>", "Example:", "gs artifact --as bot"},
		},
		{
			name: "review-request",
			run: func(fixture preflightFixture, actor []string) error {
				return reviewRequestCommand(fixture.ctx, append([]string{
					"--repo", fixture.repo, "--server", "-",
					"--head", fixture.workspace.View().Genesis, "--to", "nobody",
				}, actor...))
			},
			wantError: []string{"--to nobody names no live actor"},
			wantUsage: []string{"usage: gs review-request", "Example:", "gs review-request --as bot"},
		},
		{
			name: "land",
			run: func(fixture preflightFixture, actor []string) error {
				return landCommand(fixture.ctx, append([]string{
					"--repo", fixture.repo, "--server", "-", "--approval", nowhere,
					"--checkout", fixture.repo, "--text", "what landed and why",
				}, actor...))
			},
			wantError: []string{"is not a review verdict in this workroom"},
			wantUsage: []string{"usage: gs land", "Example:", "gs land --as bot"},
		},
		{
			// gs work --next reads rather than signs, but it asks who you are
			// the same way, so it answers the same way.
			name: "work --next",
			run: func(fixture preflightFixture, actor []string) error {
				return workCommand(fixture.ctx, append([]string{
					"--repo", fixture.repo, "--server", "-", "--next",
				}, actor...))
			},
			wantError: []string{"actor"},
			wantUsage: []string{"usage: gs work", "Example:", "gs work"},
		},
	} {
		t.Run(probe.name+" with no signing identity", func(t *testing.T) {
			t.Setenv(actorEnvironment, "")
			fixture := newPreflightFixture(t)
			before := fixture.snapshot()
			printed, err := quiet(t, func() error { return probe.run(fixture, nil) })
			requireUsageRefusal(t, printed, err, []string{"--as"}, probe.wantUsage)
			requireUnmovedWorkroom(t, fixture, before)
		})
		if probe.name == "work --next" {
			// It names no event, so it has no second case.
			continue
		}
		t.Run(probe.name+" naming an event this workroom does not hold", func(t *testing.T) {
			fixture := newPreflightFixture(t)
			before := fixture.snapshot()
			printed, err := quiet(t, func() error {
				return fixture.withoutKey(t, "worker", func() error {
					return probe.run(fixture, []string{"--as", "worker"})
				})
			})
			requireUsageRefusal(t, printed, err, probe.wantError, probe.wantUsage)
			requireUnmovedWorkroom(t, fixture, before)
		})
	}
}

func requireUsageRefusal(t *testing.T, printed string, err error, wantError, wantUsage []string) {
	t.Helper()
	if err == nil {
		t.Fatalf("the malformed invocation was accepted; output: %s", printed)
	}
	for _, want := range wantError {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q does not say %q", err, want)
		}
	}
	for _, want := range wantUsage {
		if !strings.Contains(printed, want) {
			t.Fatalf("usage output does not contain %q:\n%s", want, printed)
		}
	}
}

func requireUnmovedWorkroom(t *testing.T, fixture preflightFixture, before app.Snapshot) {
	t.Helper()
	if after := fixture.snapshot(); after.Head != before.Head || after.Depth != before.Depth {
		t.Fatalf("a refused invocation moved the workroom: %s/%d -> %s/%d",
			before.Head, before.Depth, after.Head, after.Depth)
	}
}
