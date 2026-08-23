package ciguard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolving repository root: %v", err)
	}
	return root
}

func workflows(t *testing.T) map[string]workflow {
	t.Helper()
	dir := filepath.Join(repoRoot(t), ".github", "workflows")
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
		decoded, err := parseWorkflow(data)
		if err != nil {
			t.Fatalf("%s is not valid YAML: %v", name, err)
		}
		parsed[name] = decoded
	}
	if len(parsed) == 0 {
		t.Fatal("no workflows found; this gate is looking in the wrong place")
	}
	return parsed
}

// Every workflow is held to the full admission rule: the exact permissions
// allowlist at the workflow boundary and at every job-level override, one
// classifier over job-level and step-level `uses` alike, and the explicit
// refusal of unmodelled containers. The rule itself is exercised against
// fixtures in negatives_test.go; this test only holds the real automation to
// it.
func TestRepositoryAutomationIsAdmissible(t *testing.T) {
	root := repoRoot(t)
	for name, flow := range workflows(t) {
		for _, violation := range checkWorkflow(root, name, flow) {
			t.Error(violation)
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
	data, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "dependabot.yml"))
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
