package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// preflightFixture is a small finished world: one request from the operator to
// the worker, the worker's promise on it, the worker's report closing it, one
// assert, one artifact, and one promise the fold already refused. Between them
// they reach every refusal the pre-signing fold check is meant to catch, and
// every act that satisfies it.
//
// It is built once for the package and copied per test, like every other
// fixture here: building it per subtest cost more than every command under
// test put together.
type preflightFixture struct {
	batchFixture
	request  string
	promise  string
	report   string
	assert   string
	artifact string
	// refused is an event the fold already ruled ineffective: a promise
	// standing on nothing.
	refused string
}

type preflightTemplateRepo struct {
	fixtureTemplate
	request  string
	promise  string
	report   string
	assert   string
	artifact string
	refused  string
}

var preflightTemplate = newPreflightTemplate()

func newPreflightTemplate() *preflightTemplateRepo {
	template := &preflightTemplateRepo{}
	template.build = template.buildPreflight
	return template
}

func (template *preflightTemplateRepo) buildPreflight(root string) error {
	ctx := context.Background()
	repo := filepath.Join(root, "repo")
	if _, err := gitCommand("", "init", "-b", "main", repo); err != nil {
		return err
	}
	workspace, _, err := app.Init(ctx, repo, "operator", 1<<20)
	if err != nil {
		return err
	}
	if _, _, err := workspace.AddActor(ctx, "operator", "worker", "agent"); err != nil {
		return err
	}
	genesis := workspace.EventID(workspace.View().Genesis)
	worker := workspace.View().Actors["worker"].Fingerprint
	file := func(actor string, act app.Act) (string, error) {
		submission, err := workspace.Act(ctx, actor, act)
		if err != nil {
			return "", err
		}
		return submission.Record.ID, nil
	}
	if template.request, err = file("operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindRequest, Text: "do the work",
		Body:    map[string]string{"to": worker, "conditions": "the tests pass", "no_git_artifact": "true"},
		RestsOn: []string{genesis}, IdempotencyKey: "preflight-request",
	}); err != nil {
		return err
	}
	if template.promise, err = file("worker", app.Act{
		Verb: app.VerbState, Kind: workroom.KindPromise, Text: "on it",
		RestsOn: []string{template.request}, IdempotencyKey: "preflight-promise",
	}); err != nil {
		return err
	}
	if template.report, err = file("worker", app.Act{
		Verb: app.VerbState, Kind: workroom.KindReport, Text: "the tests pass",
		RestsOn: []string{template.promise}, IdempotencyKey: "preflight-report",
	}); err != nil {
		return err
	}
	if template.assert, err = file("worker", app.Act{
		Verb: app.VerbState, Kind: workroom.KindAssert, Text: "a fact worth recording",
		RestsOn: []string{genesis}, IdempotencyKey: "preflight-assert",
	}); err != nil {
		return err
	}
	if template.artifact, err = file("worker", app.Act{
		Verb: app.VerbState, Kind: workroom.KindArtifact, Text: "the head",
		Body:    map[string]string{"path": "notes/one.md", "commit": workspace.View().Genesis},
		RestsOn: []string{genesis}, IdempotencyKey: "preflight-artifact",
	}); err != nil {
		return err
	}
	// A record the fold refused, so a later act can name a target that stands
	// in the log and stands for nothing. The application boundary signs it as
	// asked; only the fold judges it.
	template.refused, err = file("worker", app.Act{
		Verb: app.VerbState, Kind: workroom.KindPromise, Text: "a promise with no request",
		RestsOn: []string{genesis}, IdempotencyKey: "preflight-dangling",
	})
	return err
}

func newPreflightFixture(t *testing.T) preflightFixture {
	t.Helper()
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	preflightTemplate.copyRepo(t, repo)
	workspace, err := app.Open(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	return preflightFixture{
		batchFixture: batchFixture{
			t: t, ctx: ctx, repo: repo, workspace: workspace,
			genesis: workspace.EventID(workspace.View().Genesis),
		},
		request: preflightTemplate.request, promise: preflightTemplate.promise,
		report: preflightTemplate.report, assert: preflightTemplate.assert,
		artifact: preflightTemplate.artifact, refused: preflightTemplate.refused,
	}
}

func (f preflightFixture) act(t *testing.T, actor string, act app.Act) string {
	t.Helper()
	submission, err := f.workspace.Act(f.ctx, actor, act)
	if err != nil {
		t.Fatal(err)
	}
	return submission.Record.ID
}

// withoutKey runs one command with the actor's signing key taken off disk. A
// refusal under these conditions is a refusal that happened before the key was
// read: any path that reached the key would have failed saying so instead.
func (f preflightFixture) withoutKey(t *testing.T, actor string, run func() error) error {
	t.Helper()
	keyFile := f.workspace.View().Actors[actor].KeyFile
	contents, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(keyFile); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.WriteFile(keyFile, contents, 0o600); err != nil {
			t.Fatal(err)
		}
	}()
	return run()
}

// quiet runs a command with both streams captured, because a refusal prints
// usage and a landed act prints its event id, and neither belongs in the test
// output.
func quiet(t *testing.T, run func() error) (string, error) {
	t.Helper()
	outReader, outWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errReader, errWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outWriter, errWriter
	runErr := run()
	os.Stdout, os.Stderr = stdout, stderr
	outWriter.Close()
	errWriter.Close()
	printed, err := io.ReadAll(outReader)
	if err != nil {
		t.Fatal(err)
	}
	warned, err := io.ReadAll(errReader)
	if err != nil {
		t.Fatal(err)
	}
	outReader.Close()
	errReader.Close()
	return string(printed) + string(warned), runErr
}

func TestPreflightRefusesStateActsBeforeAnyKeyIsRead(t *testing.T) {
	for _, probe := range []struct {
		name string
		// arguments are appended to --repo/--as/--kind/--text, which every case
		// shares.
		refused  []string
		admitted []string
		actor    string
		// admittedActor is who files the satisfied act, when satisfying the
		// precondition means a different actor rather than different arguments.
		admittedActor string
		kind          string
		wantWords     []string
	}{
		{
			name: "artifact with no path", actor: "worker", kind: "artifact",
			refused:   []string{"--body", "commit=COMMIT"},
			admitted:  []string{"--body", "commit=COMMIT", "--body", "path=notes/two.md"},
			wantWords: []string{"artifact state requires body.path", "--body path="},
		},
		{
			name: "artifact with no commit", actor: "worker", kind: "artifact",
			refused:   []string{"--body", "path=notes/three.md"},
			admitted:  []string{"--body", "path=notes/three.md", "--body", "commit=COMMIT"},
			wantWords: []string{"artifact state requires body.commit", "--body commit="},
		},
		{
			name: "promise standing on nothing", actor: "worker", kind: "promise",
			refused:   []string{"--rests-on", "GENESIS"},
			admitted:  []string{"--rests-on", "REQUEST"},
			wantWords: []string{"dangling promise has no request", "rest the promise on the request"},
		},
		{
			// The precondition is being the actor the request addresses, so the
			// same promise on the same request lands from the worker.
			name: "promise by an actor the request does not address", actor: "operator", kind: "promise",
			refused:       []string{"--rests-on", "REQUEST"},
			admitted:      []string{"--rests-on", "REQUEST"},
			admittedActor: "worker",
			wantWords:     []string{"promise actor is not the requested performer", "body.to"},
		},
		{
			name: "report standing on nothing", actor: "worker", kind: "report",
			refused:   []string{"--rests-on", "GENESIS"},
			admitted:  []string{"--rests-on", "PROMISE"},
			wantWords: []string{"report has no promise or request", "rest the report on your promise"},
		},
	} {
		t.Run(probe.name, func(t *testing.T) {
			fixture := newPreflightFixture(t)
			before := fixture.snapshot()
			arguments := func(actor string, extra []string, key string) []string {
				resolved := []string{
					"--repo", fixture.repo, "--as", actor, "--kind", probe.kind,
					"--text", probe.name, "--idempotency-key", key,
				}
				for _, argument := range extra {
					switch argument {
					case "GENESIS":
						argument = fixture.genesis
					case "REQUEST":
						argument = fixture.request
					case "PROMISE":
						argument = fixture.promise
					case "commit=COMMIT":
						// An artifact commit is checked against the repository
						// before anything else, so the fixture's own genesis
						// commit stands in for a reviewed head.
						argument = "commit=" + fixture.workspace.View().Genesis
					}
					resolved = append(resolved, argument)
				}
				if !containsFlag(extra, "--rests-on") {
					resolved = append(resolved, "--rests-on", fixture.genesis)
				}
				return resolved
			}
			output, err := quiet(t, func() error {
				return fixture.withoutKey(t, probe.actor, func() error {
					return stateCommand(fixture.ctx, arguments(probe.actor, probe.refused, "refused"))
				})
			})
			if err == nil {
				t.Fatal("the act was not refused")
			}
			for _, want := range append(probe.wantWords, "--no-preflight") {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("refusal %q does not say %q (output: %s)", err, want, output)
				}
			}
			if after := fixture.snapshot(); after.Head != before.Head || after.Depth != before.Depth {
				t.Fatalf("a refused act moved the workroom: %s/%d -> %s/%d", before.Head, before.Depth, after.Head, after.Depth)
			}
			if probe.admitted == nil {
				return
			}
			admittedActor := probe.actor
			if probe.admittedActor != "" {
				admittedActor = probe.admittedActor
			}
			landed, err := quiet(t, func() error {
				return stateCommand(fixture.ctx, arguments(admittedActor, probe.admitted, "admitted"))
			})
			if err != nil {
				t.Fatalf("the satisfied act was refused: %v", err)
			}
			after := fixture.snapshot()
			if after.Depth != before.Depth+1 {
				t.Fatalf("the satisfied act did not land: depth %d -> %d", before.Depth, after.Depth)
			}
			if decision := decisionByEvent(t, after.Projection, printedEvent(landed)); decision.Verdict != workroom.Effective {
				t.Fatalf("the satisfied act landed %s: %s", decision.Verdict, decision.Reason)
			}
		})
	}
}

func containsFlag(arguments []string, flag string) bool {
	for _, argument := range arguments {
		if argument == flag {
			return true
		}
	}
	return false
}

// A report may cite the request its promise answers, as provenance, and no
// other. The fold refuses the other one; so does this, before signing.
func TestPreflightRefusesAReportCitingTheWrongRequest(t *testing.T) {
	fixture := newPreflightFixture(t)
	worker := fixture.workspace.View().Actors["worker"].Fingerprint
	other := fixture.act(t, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindRequest, Text: "a different request",
		Body:    map[string]string{"to": worker, "conditions": "something else", "no_git_artifact": "true"},
		RestsOn: []string{fixture.genesis}, IdempotencyKey: "preflight-other-request",
	})
	before := fixture.snapshot()
	_, err := quiet(t, func() error {
		return fixture.withoutKey(t, "worker", func() error {
			return stateCommand(fixture.ctx, []string{
				"--repo", fixture.repo, "--as", "worker", "--kind", "report", "--text", "done",
				"--rests-on", fixture.promise, "--rests-on", other, "--idempotency-key", "wrong-request",
			})
		})
	})
	if err == nil || !strings.Contains(err.Error(), "report cites a request other than the one its promise answers") {
		t.Fatalf("error = %v", err)
	}
	if after := fixture.snapshot(); after.Depth != before.Depth {
		t.Fatalf("a refused report moved the workroom: depth %d -> %d", before.Depth, after.Depth)
	}
	// The same report, citing the request its promise does answer, lands.
	if _, err := quiet(t, func() error {
		return stateCommand(fixture.ctx, []string{
			"--repo", fixture.repo, "--as", "worker", "--kind", "report", "--text", "done",
			"--rests-on", fixture.promise, "--rests-on", fixture.request, "--idempotency-key", "right-request",
		})
	}); err != nil {
		t.Fatalf("the satisfied report was refused: %v", err)
	}
	if after := fixture.snapshot(); after.Depth != before.Depth+1 {
		t.Fatalf("the satisfied report did not land: depth %d -> %d", before.Depth, after.Depth)
	}
}

func TestPreflightRefusesRatifications(t *testing.T) {
	for _, probe := range []struct {
		name  string
		actor string
		// target is the one the refusal names. admitted is the actor whose
		// ratification of admittedTarget must land instead — the same act with
		// its precondition satisfied. admittedTarget defaults to target, for
		// the cases where only the ratifier had to change.
		target         func(f preflightFixture) string
		admitted       string
		admittedTarget func(f preflightFixture) string
		wantWords      []string
	}{
		{
			// The precondition is that something stands at the target. A record
			// the fold refused stands for nothing; the report beside it, which
			// the fold admitted, is ratified by the same actor without trouble.
			name: "target the fold refused", actor: "operator",
			target:         func(f preflightFixture) string { return f.refused },
			admitted:       "operator",
			admittedTarget: func(f preflightFixture) string { return f.report },
			wantWords:      []string{"ratify target is not effective", "gs inspect"},
		},
		{
			name: "kind with no satisfier", actor: "operator",
			target:    func(f preflightFixture) string { return f.artifact },
			wantWords: []string{"statement kind is not ratifiable"},
		},
		{
			name: "statement whose satisfier is a role the actor lacks", actor: "worker",
			target:    func(f preflightFixture) string { return f.assert },
			admitted:  "operator",
			wantWords: []string{"actor lacks ratifier role", "ask an actor holding ratifier"},
		},
		{
			name: "report ratified by someone other than its requester", actor: "worker",
			target:    func(f preflightFixture) string { return f.report },
			admitted:  "operator",
			wantWords: []string{"only the requester may declare satisfaction", "originating requester"},
		},
		{
			// The precondition is that this workroom holds the target at all.
			name: "target from another workroom", actor: "operator",
			target:         func(f preflightFixture) string { return foreignEvent() },
			admitted:       "operator",
			admittedTarget: func(f preflightFixture) string { return f.report },
			wantWords:      []string{"ratify target is unknown", "full event id"},
		},
	} {
		t.Run(probe.name, func(t *testing.T) {
			fixture := newPreflightFixture(t)
			before := fixture.snapshot()
			target := probe.target(fixture)
			_, err := quiet(t, func() error {
				return fixture.withoutKey(t, probe.actor, func() error {
					return ratifyCommand(fixture.ctx, []string{
						"--repo", fixture.repo, "--as", probe.actor, "--idempotency-key", "refused", target,
					})
				})
			})
			if err == nil {
				t.Fatal("the ratification was not refused")
			}
			for _, want := range probe.wantWords {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("refusal %q does not say %q", err, want)
				}
			}
			if after := fixture.snapshot(); after.Depth != before.Depth {
				t.Fatalf("a refused ratification moved the workroom: depth %d -> %d", before.Depth, after.Depth)
			}
			if probe.admitted == "" {
				return
			}
			admitted := target
			if probe.admittedTarget != nil {
				admitted = probe.admittedTarget(fixture)
			}
			landed, err := quiet(t, func() error {
				return ratifyCommand(fixture.ctx, []string{
					"--repo", fixture.repo, "--as", probe.admitted, "--idempotency-key", "admitted", admitted,
				})
			})
			if err != nil {
				t.Fatalf("the authorized ratification was refused: %v", err)
			}
			after := fixture.snapshot()
			if after.Depth != before.Depth+1 {
				t.Fatalf("the authorized ratification did not land: depth %d -> %d", before.Depth, after.Depth)
			}
			if decision := decisionByEvent(t, after.Projection, printedEvent(landed)); decision.Verdict != workroom.Effective {
				t.Fatalf("the authorized ratification landed %s: %s", decision.Verdict, decision.Reason)
			}
		})
	}
}

// Retirement is its own authority question, and the answer names who may ask.
func TestPreflightRefusesASupersessionByAnActorWithNoStanding(t *testing.T) {
	fixture := newPreflightFixture(t)
	before := fixture.snapshot()
	_, err := quiet(t, func() error {
		return fixture.withoutKey(t, "worker", func() error {
			return supersedeCommand(fixture.ctx, []string{
				"--repo", fixture.repo, "--as", "worker", "--text", "not mine to retire",
				"--idempotency-key", "refused", fixture.request,
			})
		})
	})
	if err == nil {
		t.Fatal("the supersession was not refused")
	}
	for _, want := range []string{"actor may not supersede target", "ratifier", "operator"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q does not say %q", err, want)
		}
	}
	if after := fixture.snapshot(); after.Depth != before.Depth {
		t.Fatalf("a refused supersession moved the workroom: depth %d -> %d", before.Depth, after.Depth)
	}
	// The author of the record retires it without trouble.
	if _, err := quiet(t, func() error {
		return supersedeCommand(fixture.ctx, []string{
			"--repo", fixture.repo, "--as", "operator", "--text", "withdrawn",
			"--idempotency-key", "admitted", fixture.request,
		})
	}); err != nil {
		t.Fatalf("the author's supersession was refused: %v", err)
	}
	if after := fixture.snapshot(); after.Depth != before.Depth+1 {
		t.Fatalf("the author's supersession did not land: depth %d -> %d", before.Depth, after.Depth)
	}
}

// A target that stands in no log at all reaches the fold as unknown, and this
// says so before signing rather than after.
func TestPreflightRefusesASupersessionOfAnUnknownTarget(t *testing.T) {
	fixture := newPreflightFixture(t)
	before := fixture.snapshot()
	_, err := quiet(t, func() error {
		return fixture.withoutKey(t, "operator", func() error {
			return supersedeCommand(fixture.ctx, []string{
				"--repo", fixture.repo, "--as", "operator", "--text", "retire it",
				"--idempotency-key", "refused", foreignEvent(),
			})
		})
	})
	if err == nil || !strings.Contains(err.Error(), "supersede target is unknown") {
		t.Fatalf("error = %v", err)
	}
	if after := fixture.snapshot(); after.Depth != before.Depth {
		t.Fatalf("a refused supersession moved the workroom: depth %d -> %d", before.Depth, after.Depth)
	}
}

// A ratification of a retired record is refused, and the successor's is not.
func TestPreflightRefusesRatifyingARetiredStatement(t *testing.T) {
	fixture := newPreflightFixture(t)
	if _, err := quiet(t, func() error {
		return supersedeCommand(fixture.ctx, []string{
			"--repo", fixture.repo, "--as", "worker", "--text", "withdrawn",
			"--idempotency-key", "retire-assert", fixture.assert,
		})
	}); err != nil {
		t.Fatal(err)
	}
	before := fixture.snapshot()
	_, err := quiet(t, func() error {
		return fixture.withoutKey(t, "operator", func() error {
			return ratifyCommand(fixture.ctx, []string{
				"--repo", fixture.repo, "--as", "operator", "--idempotency-key", "refused", fixture.assert,
			})
		})
	})
	if err == nil || !strings.Contains(err.Error(), "retired statement cannot be ratified") {
		t.Fatalf("error = %v", err)
	}
	if after := fixture.snapshot(); after.Depth != before.Depth {
		t.Fatalf("a refused ratification moved the workroom: depth %d -> %d", before.Depth, after.Depth)
	}
	// A fresh assert in its place is ratified exactly as before.
	replacement := fixture.act(t, "worker", app.Act{
		Verb: app.VerbState, Kind: workroom.KindAssert, Text: "the fact, restated",
		RestsOn: []string{fixture.genesis}, IdempotencyKey: "preflight-assert-again",
	})
	if _, err := quiet(t, func() error {
		return ratifyCommand(fixture.ctx, []string{
			"--repo", fixture.repo, "--as", "operator", "--idempotency-key", "admitted", replacement,
		})
	}); err != nil {
		t.Fatalf("the live statement's ratification was refused: %v", err)
	}
}

// The escape exists for the deliberate replay of a shape the fold refuses, so
// it has to file exactly the act the check stopped.
func TestNoPreflightFilesTheRefusedActAnyway(t *testing.T) {
	fixture := newPreflightFixture(t)
	before := fixture.snapshot()
	landed, err := quiet(t, func() error {
		return stateCommand(fixture.ctx, []string{
			"--repo", fixture.repo, "--as", "worker", "--kind", "promise",
			"--text", "a promise with no request", "--rests-on", fixture.genesis,
			"--idempotency-key", "deliberate", "--no-preflight",
		})
	})
	if err != nil {
		t.Fatalf("--no-preflight refused the act: %v", err)
	}
	after := fixture.snapshot()
	if after.Depth != before.Depth+1 {
		t.Fatalf("--no-preflight appended nothing: depth %d -> %d", before.Depth, after.Depth)
	}
	decision := decisionByEvent(t, after.Projection, printedEvent(landed))
	if decision.Verdict != workroom.Ineffective || decision.Reason != "dangling promise has no request" {
		t.Fatalf("the filed act was decided %s: %s", decision.Verdict, decision.Reason)
	}
}

// The check must not change what an admitted act contains. The same act filed
// with the check on and with it off has to be the same record, byte for byte,
// which the idempotency key proves: a differing act under one key is refused,
// and a matching one replays the event already in the log.
func TestPreflightDoesNotChangeAnAdmittedAct(t *testing.T) {
	fixture := newPreflightFixture(t)
	arguments := func(extra ...string) []string {
		return append([]string{
			"--repo", fixture.repo, "--as", "worker", "--kind", "assert",
			"--text", "the same act either way", "--rests-on", fixture.genesis,
			"--idempotency-key", "same-act",
		}, extra...)
	}
	first, err := quiet(t, func() error { return stateCommand(fixture.ctx, arguments()) })
	if err != nil {
		t.Fatal(err)
	}
	before := fixture.snapshot()
	second, err := quiet(t, func() error { return stateCommand(fixture.ctx, arguments("--no-preflight")) })
	if err != nil {
		t.Fatalf("the replay under the same key was refused: %v", err)
	}
	if after := fixture.snapshot(); after.Depth != before.Depth {
		t.Fatalf("the replay appended a second act: depth %d -> %d", before.Depth, after.Depth)
	}
	if printedEvent(first) != printedEvent(second) {
		t.Fatalf("the same act filed two ways gave two events: %q and %q", printedEvent(first), printedEvent(second))
	}
}

// printedEvent is the record id a signing command prints on standard output.
func printedEvent(output string) string {
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "git:") {
			return line
		}
	}
	return ""
}

// A chain stops on the first act the fold would refuse, with nothing appended:
// half a chain in the log is worse than none of it.
func TestPreflightRefusesABatchBeforeItsFirstAppend(t *testing.T) {
	fixture := newPreflightFixture(t)
	before := fixture.snapshot()
	commit := fixture.workspace.View().Genesis
	acts := []map[string]any{
		{"verb": "state", "kind": "assert", "text": "a fine first act", "rests_on": []string{fixture.genesis}},
		{"verb": "state", "kind": "artifact", "text": "a head with no path", "body": map[string]string{"commit": commit}, "rests_on": []string{fixture.genesis}},
	}
	encoded, err := json.Marshal(acts)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "chain.json")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = quiet(t, func() error {
		return fixture.withoutKey(t, "worker", func() error {
			return batchCommand(fixture.ctx, []string{"--repo", fixture.repo, "--as", "worker", path})
		})
	})
	if err == nil {
		t.Fatal("the chain was not refused")
	}
	for _, want := range []string{"act 1", "artifact state requires body.path"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q does not say %q", err, want)
		}
	}
	if after := fixture.snapshot(); after.Depth != before.Depth {
		t.Fatalf("a refused chain appended something: depth %d -> %d", before.Depth, after.Depth)
	}
	// With the missing field supplied, the same chain lands whole.
	acts[1]["body"] = map[string]string{"commit": commit, "path": "notes/four.md"}
	encoded, err = json.Marshal(acts)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := quiet(t, func() error {
		return batchCommand(fixture.ctx, []string{"--repo", fixture.repo, "--as", "worker", path})
	}); err != nil {
		t.Fatalf("the satisfied chain was refused: %v", err)
	}
	if after := fixture.snapshot(); after.Depth != before.Depth+2 {
		t.Fatalf("the satisfied chain did not land whole: depth %d -> %d", before.Depth, after.Depth)
	}
}

// An exact retry is the one recovery a filer must always have. The act was
// accepted; the world has moved since; repeating the same command under the same
// key must replay the event the log already holds rather than be judged as a
// fresh act against a world that would now refuse it.
func TestPreflightReplaysAnAcceptedActAfterItsWorldMoved(t *testing.T) {
	fixture := newPreflightFixture(t)
	arguments := []string{
		"--repo", fixture.repo, "--as", "operator", "--idempotency-key", "ratify-once", fixture.assert,
	}
	first, err := quiet(t, func() error { return ratifyCommand(fixture.ctx, arguments) })
	if err != nil {
		t.Fatal(err)
	}
	// The author retires the target, so the same act would now be refused.
	fixture.act(t, "worker", app.Act{
		Verb: app.VerbSupersede, Target: fixture.assert, Text: "withdrawn",
		IdempotencyKey: "retire-the-target",
	})
	before := fixture.snapshot()
	second, err := quiet(t, func() error { return ratifyCommand(fixture.ctx, arguments) })
	if err != nil {
		t.Fatalf("the exact retry was refused: %v", err)
	}
	if printedEvent(second) != printedEvent(first) {
		t.Fatalf("the retry returned %q, not the accepted event %q", printedEvent(second), printedEvent(first))
	}
	if after := fixture.snapshot(); after.Depth != before.Depth {
		t.Fatalf("the retry appended something: depth %d -> %d", before.Depth, after.Depth)
	}
	// The key buys nothing else. A different act under the same key is still
	// refused, by the kernel, as the reused key it is.
	_, err = quiet(t, func() error {
		return ratifyCommand(fixture.ctx, []string{
			"--repo", fixture.repo, "--as", "operator", "--idempotency-key", "ratify-once", fixture.report,
		})
	})
	if err == nil || !strings.Contains(err.Error(), "idempotency key") {
		t.Fatalf("a different act under the same key was answered %v", err)
	}
	if after := fixture.snapshot(); after.Depth != before.Depth {
		t.Fatalf("the reused key appended something: depth %d -> %d", before.Depth, after.Depth)
	}
}

// The same recovery through a chain: a key the log already holds makes the chain
// a retry, and the kernel answers each act of it.
func TestPreflightReplaysAnAcceptedChainAfterItsWorldMoved(t *testing.T) {
	fixture := newPreflightFixture(t)
	acts := []map[string]any{
		{"verb": "ratify", "target": fixture.assert, "idempotency_key": "chain-ratify-once"},
	}
	if _, err := fixture.runChain(t, "operator", acts); err != nil {
		t.Fatal(err)
	}
	fixture.act(t, "worker", app.Act{
		Verb: app.VerbSupersede, Target: fixture.assert, Text: "withdrawn",
		IdempotencyKey: "retire-the-chain-target",
	})
	before := fixture.snapshot()
	output, err := fixture.runChain(t, "operator", acts)
	if err != nil {
		t.Fatalf("the exact chain retry was refused: %v", err)
	}
	if !strings.Contains(output, `"replayed"`) || strings.Contains(output, `"landed": 1`) {
		t.Fatalf("the chain retry did not replay: %s", output)
	}
	if after := fixture.snapshot(); after.Depth != before.Depth {
		t.Fatalf("the chain retry appended something: depth %d -> %d", before.Depth, after.Depth)
	}
}

// runChain writes a chain and runs it, with both streams captured.
func (f preflightFixture) runChain(t *testing.T, actor string, acts []map[string]any, flags ...string) (string, error) {
	t.Helper()
	encoded, err := json.Marshal(acts)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "chain.json")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	arguments := append([]string{"--repo", f.repo, "--as", actor}, flags...)
	return quiet(t, func() error { return batchCommand(f.ctx, append(arguments, path)) })
}

// A label names an act the chain has yet to mint, and skipping those acts left
// the commonest chain shape unchecked: an artifact missing its path, behind one
// label, landed ineffective. Every act is judged now, each against the world the
// acts before it would make.
func TestPreflightJudgesALabeledChainInOrder(t *testing.T) {
	fixture := newPreflightFixture(t)
	before := fixture.snapshot()
	commit := fixture.workspace.View().Genesis
	acts := []map[string]any{
		{"label": "first", "verb": "state", "kind": "assert", "text": "fine", "rests_on": []string{fixture.genesis}},
		{"verb": "state", "kind": "artifact", "text": "a head with no path",
			"body": map[string]string{"commit": commit}, "rests_on": []string{"$first"}},
	}
	output, err := fixture.runChain(t, "worker", acts)
	if err == nil {
		t.Fatalf("the malformed act behind a label was not refused: %s", output)
	}
	for _, want := range []string{"act 1", "artifact state requires body.path"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q does not say %q", err, want)
		}
	}
	if after := fixture.snapshot(); after.Depth != before.Depth {
		t.Fatalf("a refused labeled chain appended something: depth %d -> %d", before.Depth, after.Depth)
	}
	// The same chain with the field supplied lands whole, and every act of it is
	// effective: an act resting on a label must be judged against the act that
	// label will name, not refused for standing on nothing.
	acts[1]["body"] = map[string]string{"commit": commit, "path": "notes/labelled.md"}
	if _, err := fixture.runChain(t, "worker", acts); err != nil {
		t.Fatalf("the satisfied labeled chain was refused: %v", err)
	}
	after := fixture.snapshot()
	if after.Depth != before.Depth+2 {
		t.Fatalf("the satisfied chain did not land whole: depth %d -> %d", before.Depth, after.Depth)
	}
	for _, decision := range after.Projection.Decisions {
		if decision.Verdict != workroom.Effective && decision.Event != fixture.refused {
			t.Fatalf("the landed chain left %s: %s", decision.Verdict, decision.Reason)
		}
	}
}

// A chain judged act by act against one unchanging world would refuse this: the
// promise is effective only once the request it rests on exists.
func TestPreflightAdmitsAChainThatOnlyItsOwnEarlierActsMakeValid(t *testing.T) {
	fixture := newPreflightFixture(t)
	before := fixture.snapshot()
	worker := fixture.workspace.View().Actors["worker"].Fingerprint
	acts := []map[string]any{
		{"label": "ask", "verb": "state", "kind": "request", "text": "do it",
			"body":     map[string]string{"to": worker, "conditions": "it is done", "no_git_artifact": "true"},
			"rests_on": []string{fixture.genesis}},
		{"verb": "state", "kind": "promise", "text": "on it", "rests_on": []string{"$ask"}},
	}
	if _, err := fixture.runChain(t, "worker", acts); err != nil {
		t.Fatalf("a chain whose own first act makes the second valid was refused: %v", err)
	}
	after := fixture.snapshot()
	if after.Depth != before.Depth+2 {
		t.Fatalf("the chain did not land whole: depth %d -> %d", before.Depth, after.Depth)
	}
}

// The judging fold is this call's own. The projection every reader in the
// process holds must be exactly what it was, whether the chain was refused or
// admitted.
func TestPreflightLeavesTheProjectionUnchangedWhenItRefusesAChain(t *testing.T) {
	fixture := newPreflightFixture(t)
	before := fixture.snapshot()
	acts := []map[string]any{
		{"label": "first", "verb": "state", "kind": "assert", "text": "fine", "rests_on": []string{fixture.genesis}},
		{"verb": "state", "kind": "artifact", "text": "no path",
			"body": map[string]string{"commit": fixture.workspace.View().Genesis}, "rests_on": []string{"$first"}},
	}
	if _, err := fixture.runChain(t, "worker", acts); err == nil {
		t.Fatal("the chain was not refused")
	}
	after := fixture.snapshot()
	if after.Head != before.Head || after.Depth != before.Depth ||
		len(after.Projection.Statements) != len(before.Projection.Statements) ||
		len(after.Projection.Decisions) != len(before.Projection.Decisions) {
		t.Fatalf("judging a chain changed the projection: %s/%d -> %s/%d",
			before.Head, before.Depth, after.Head, after.Depth)
	}
}

// foreignEvent is a well-formed identifier of another workroom: it parses, it
// resolves to itself, and it names nothing here. It is what pasting an id from
// the wrong repository produces.
func foreignEvent() string {
	return "git:sha1:" + strings.Repeat("f", 40) + "#git:sha1:" + strings.Repeat("e", 40)
}

// The fix table is keyed on the fold's own words, so a reason the fold rewords
// stops matching and its advice disappears with no test failing anywhere near
// it. This is the failure: every key, and every fragment matched by shape, must
// still be written in the fold.
func TestEveryFixNamesAReasonTheFoldStillWrites(t *testing.T) {
	source := foldSource(t)
	for reason := range preflightFixes {
		if !strings.Contains(source, strconv.Quote(reason)) {
			t.Errorf("no fold reason is written %q; the fix for it can never print", reason)
		}
	}
	for _, pattern := range preflightFixPatterns {
		if !strings.Contains(source, pattern) {
			t.Errorf("the fold writes no reason containing %q; the fix for it can never print", pattern)
		}
	}
}

// foldSource is every non-test Go file of the fold, concatenated. The reasons
// are string literals there, so this reads them as the fold spells them rather
// than as some list kept beside it.
func foldSource(t *testing.T) string {
	t.Helper()
	directory := filepath.Join("..", "..", "internal", "workroom")
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	var source strings.Builder
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		content, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
		source.Write(content)
	}
	if source.Len() == 0 {
		t.Fatal("no fold source was read; this test proves nothing")
	}
	return source.String()
}
