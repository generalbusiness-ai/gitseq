// Package ciguard holds the repository's checks over its own automation. The
// workflows decide what runs before code lands, so they are security surface,
// and nothing else in the build inspects them.
//
// Everything here decodes YAML rather than matching text. An earlier revision
// used regular expressions and a hand-written line parser, which cannot tell a
// value from a comment, an alias from a literal, a nested key from a top-level
// one, or a duplicate from an override - so it could report a workflow safe on
// the strength of a string that never takes effect.
package ciguard

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type workflow struct {
	Name        string         `yaml:"name"`
	Permissions any            `yaml:"permissions"`
	Jobs        map[string]job `yaml:"jobs"`
	On          any            `yaml:"on"`
}

type job struct {
	Permissions any    `yaml:"permissions"`
	Steps       []step `yaml:"steps"`
	// A reusable-workflow job carries `uses` at the job level and has no
	// steps. Leaving it unmodelled meant an unpinned reusable workflow passed
	// the pin check by being invisible to it.
	Uses string `yaml:"uses"`
}

type step struct {
	Name string `yaml:"name"`
	Uses string `yaml:"uses"`
	Run  string `yaml:"run"`
}

func workflows(t *testing.T) map[string]workflow {
	t.Helper()
	dir := filepath.Join("..", "..", ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	parsed := map[string]workflow{}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		// Deliberately not KnownFields: workflows legitimately carry keys these
		// checks do not own - concurrency, env, timeouts - and rejecting them
		// would make this gate an obstacle rather than a check. The cost is
		// that a security-relevant construct these structs do not model is
		// invisible here, so every such location must be modelled explicitly.
		// An earlier revision claimed KnownFields was enabled; it was not.
		var decoded workflow
		if err := yaml.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("%s is not valid YAML: %v", name, err)
		}
		parsed[name] = decoded
	}
	if len(parsed) == 0 {
		t.Fatal("no workflows found; this gate is looking in the wrong place")
	}
	return parsed
}

var commitPin = regexp.MustCompile(`^[^@]+@[0-9a-f]{40}$`)

// A tag or branch reference can be moved by whoever controls the action, so a
// commit pin is the difference between a supply chain we chose and one that can
// be changed under us after review. Read from decoded steps, so a pinned
// reference sitting in a comment satisfies nothing.
func TestEveryActionIsPinnedToACommit(t *testing.T) {
	for name, flow := range workflows(t) {
		for jobName, j := range flow.Jobs {
			// The job's own `uses`, for a reusable workflow, is as much a
			// supply-chain edge as any step's.
			if j.Uses != "" && !commitPin.MatchString(j.Uses) {
				t.Errorf("%s job %q calls reusable workflow %q without a 40-character commit pin", name, jobName, j.Uses)
			}
			for _, s := range j.Steps {
				if s.Uses == "" {
					continue
				}
				if !commitPin.MatchString(s.Uses) {
					t.Errorf("%s job %q uses %q without a 40-character commit pin", name, jobName, s.Uses)
				}
			}
		}
	}
}

// Declaring permissions is not the same as declaring least privilege. An
// earlier revision accepted any non-nil value, so `permissions: write-all`
// passed a test whose name promised the opposite.
//
// A scalar form cannot be scoped: write-all grants everything, and read-all
// grants read on scopes these jobs never touch. Only a map of explicitly named
// scopes, each read or none, is checked here - and job-level overrides are
// held to the same rule, since an override is exactly where a wider scope
// would be introduced.
func checkPermissions(t *testing.T, where string, value any) {
	t.Helper()
	switch declared := value.(type) {
	case nil:
		return
	case string:
		t.Errorf("%s declares permissions as %q; a scalar cannot be scoped, so it grants more than any job here needs", where, declared)
	case map[string]any:
		if len(declared) == 0 {
			t.Errorf("%s declares an empty permissions map", where)
		}
		for scope, level := range declared {
			text, ok := level.(string)
			if !ok {
				t.Errorf("%s grants %q a non-string level %v", where, scope, level)
				continue
			}
			if text != "read" && text != "none" {
				t.Errorf("%s grants %q level %q; these jobs read code and nothing else", where, scope, text)
			}
		}
	default:
		t.Errorf("%s declares permissions in an unrecognised form %T", where, value)
	}
}

// An absent permissions block inherits the default token scope, far wider than
// any of these jobs needs.
func TestEveryWorkflowDeclaresLeastPrivilege(t *testing.T) {
	for name, flow := range workflows(t) {
		if flow.Permissions == nil {
			for jobName, j := range flow.Jobs {
				if j.Permissions == nil {
					t.Errorf("%s declares no permissions at the workflow level and job %q declares none either", name, jobName)
				}
			}
		} else {
			checkPermissions(t, name, flow.Permissions)
		}
		for jobName, j := range flow.Jobs {
			if j.Permissions != nil {
				checkPermissions(t, name+" job "+jobName, j.Permissions)
			}
		}
	}
}

// The defect that motivated this change. `make ui` rebuilds the embedded bundle
// without comparing it to what is committed; only `make ui-check` compares. The
// reproducibility gate cannot substitute, because a rebuilt bundle lands as a
// new content-hashed file and `git diff --exit-code` ignores untracked files.
func TestCIVerifiesTheCommittedUIEmbed(t *testing.T) {
	flow, ok := workflows(t)["ci.yml"]
	if !ok {
		t.Fatal("ci.yml is missing")
	}
	verified := false
	for jobName, j := range flow.Jobs {
		for _, s := range j.Steps {
			for _, line := range strings.Split(s.Run, "\n") {
				switch strings.TrimSpace(line) {
				case "make ui-check":
					verified = true
				case "make ui":
					t.Errorf("%s job %q runs bare `make ui`, which rebuilds the embed without comparing it", "ci.yml", jobName)
				}
			}
		}
	}
	if !verified {
		t.Error("CI does not run make ui-check, so a stale committed UI embed would pass")
	}
}

type dependabot struct {
	Version int                `yaml:"version"`
	Updates []dependabotUpdate `yaml:"updates"`
}

type dependabotUpdate struct {
	Ecosystem string `yaml:"package-ecosystem"`
	Directory string `yaml:"directory"`
	Schedule  struct {
		Interval string `yaml:"interval"`
	} `yaml:"schedule"`
	Limit int `yaml:"open-pull-requests-limit"`
}

// Each ecosystem needs its own entry with its own directory: the UI lockfile
// lives under ui/, and a root-only configuration would never see it.
func TestDependabotCoversEveryEcosystem(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "dependabot.yml"))
	if err != nil {
		t.Fatalf("reading dependabot.yml: %v", err)
	}
	var config dependabot
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatalf("dependabot.yml is not valid YAML: %v", err)
	}
	if config.Version != 2 {
		t.Errorf("dependabot version = %d, want 2", config.Version)
	}
	found := map[string]dependabotUpdate{}
	for _, update := range config.Updates {
		if _, duplicate := found[update.Ecosystem]; duplicate {
			t.Errorf("dependabot.yml declares %q twice; the later entry silently wins", update.Ecosystem)
		}
		found[update.Ecosystem] = update
	}
	for ecosystem, directory := range map[string]string{
		"gomod":          "/",
		"npm":            "/ui",
		"github-actions": "/",
	} {
		update, ok := found[ecosystem]
		if !ok {
			t.Errorf("dependabot.yml has no entry for %q", ecosystem)
			continue
		}
		if update.Directory != directory {
			t.Errorf("%q watches %q, want %q", ecosystem, update.Directory, directory)
		}
		if update.Schedule.Interval == "" {
			t.Errorf("%q has no schedule interval, so its cadence is unbounded", ecosystem)
		}
	}
}
