// gitseq-report runs the six adversarial cases and writes evidence projections.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

type testEvent struct {
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Elapsed float64 `json:"Elapsed"`
}

type testResult struct {
	Package string  `json:"package"`
	Test    string  `json:"test"`
	Status  string  `json:"status"`
	Seconds float64 `json:"seconds"`
}

type caseDefinition struct {
	Number  int
	Name    string
	Tests   []string
	Finding string
}

type caseResult struct {
	Number  int          `json:"number"`
	Name    string       `json:"name"`
	Status  string       `json:"status"`
	Finding string       `json:"finding"`
	Tests   []testResult `json:"tests"`
}

type evidence struct {
	Schema     string       `json:"schema"`
	GoVersion  string       `json:"go_version"`
	GitVersion string       `json:"git_version"`
	Command    string       `json:"command"`
	Status     string       `json:"status"`
	Cases      []caseResult `json:"cases"`
}

var definitions = []caseDefinition{
	{1, "Concurrent retry and failover", []string{
		"github.com/generalbusiness-ai/gitseq/spike/cmd/gitseq-spike.TestActualProcessExitRecoversFromGitAlone",
		"github.com/generalbusiness-ai/gitseq/internal/kernel.TestCreateSubmitReplayVerifyObjectFormats",
		"github.com/generalbusiness-ai/gitseq/internal/kernel.TestConcurrentCASProducesOneLinearChain",
		"github.com/generalbusiness-ai/gitseq/internal/kernel.TestCrashBoundariesRecoverFromLog",
	}, "Cold processes rebuild head and idempotency from Git; a CAS loser retries into one signed chain."},
	{2, "Rebinding attacks", []string{
		"github.com/generalbusiness-ai/gitseq/internal/intent.TestSignedBindingFieldsCannotBeSwapped",
		"github.com/generalbusiness-ai/gitseq/internal/kernel.TestVerifierRejectsRebindingAndTrailerMutation",
		"github.com/generalbusiness-ai/gitseq/internal/kernel.TestIdempotencyConflict",
		"github.com/generalbusiness-ai/gitseq/internal/kernel.TestSizeCeilingAndEnvelopeOnlyAdmissionHook",
	}, "Actor intent binds target, payload tree, causal trailers and idempotency identity."},
	{3, "Nexus crash with live ephemera", []string{
		"github.com/generalbusiness-ai/gitseq/internal/nexus.TestCrashChangesGenerationAndOldCursorResets",
		"github.com/generalbusiness-ai/gitseq/internal/nexus.TestRetainedFramesVerifyWithoutHub",
		"github.com/generalbusiness-ai/gitseq/internal/nexus.TestSelfAssertedNexusKeyIsNotTrust",
		"github.com/generalbusiness-ai/gitseq/internal/nexus.TestNexusDoesNotTouchGit",
	}, "A new nexus generation resets live state; retained participant copies remain independently attestable."},
	{4, "Unauthorized fetch across a domain", []string{
		"github.com/generalbusiness-ai/gitseq/internal/domain.TestRepositoryIsTheHTTPReadBoundaryEvenForKnownOID",
	}, "Repository-scoped smart-HTTP authorization denies fetch-by-known-hash across domains."},
	{5, "Snapshot/watch race", []string{
		"github.com/generalbusiness-ai/gitseq/internal/nexus.TestSnapshotWatchBarrierCannotMissTransition",
	}, "The snapshot cursor and state share one barrier; the next transition appears strictly after that cursor."},
	{6, "Conflicting multi-log custody transition", []string{
		"github.com/generalbusiness-ai/gitseq/internal/custody.TestThreeStepSagaAcrossSecurityDomains",
		"github.com/generalbusiness-ai/gitseq/internal/custody.TestMultipleCompletedSettlementsBecomeDisputed",
	}, "The saga branch leaves competing settlements unorderable but total: every event projects as disputed. An asset-owned log excludes that dispute by construction — evidence for the entity-log default."},
}

func main() {
	root, err := moduleRoot()
	if err != nil {
		fatal(err)
	}
	goTool := filepath.Join(runtime.GOROOT(), "bin", "go")
	command := exec.Command(goTool, "test", "-count=1", "-json", "./...")
	command.Dir = root
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, runErr := command.Output()

	results := make(map[string]testResult)
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		var event testEvent
		if json.Unmarshal(scanner.Bytes(), &event) != nil || event.Test == "" {
			continue
		}
		if event.Action == "pass" || event.Action == "fail" || event.Action == "skip" {
			key := event.Package + "." + event.Test
			results[key] = testResult{Package: event.Package, Test: event.Test, Status: event.Action, Seconds: event.Elapsed}
		}
	}

	gitVersion := strings.TrimSpace(string(mustOutput("git", "--version")))
	report := evidence{Schema: "gitseq.spike-evidence.v0", GoVersion: runtime.Version(), GitVersion: gitVersion, Command: "go test -count=1 -json ./...", Status: "pass"}
	for _, definition := range definitions {
		result := caseResult{Number: definition.Number, Name: definition.Name, Status: "pass", Finding: definition.Finding}
		for _, key := range definition.Tests {
			test, found := results[key]
			if !found {
				test = testResult{Package: strings.Split(key, ".Test")[0], Test: "Test" + strings.Split(key, ".Test")[1], Status: "missing"}
			}
			if test.Status != "pass" {
				result.Status = "fail"
				report.Status = "fail"
			}
			result.Tests = append(result.Tests, test)
		}
		report.Cases = append(report.Cases, result)
	}
	if runErr != nil || scanner.Err() != nil {
		report.Status = "fail"
	}

	// Keep run-specific and stable evidence beside the adversarial fixture even
	// though the test process now runs from the shipping module root.
	directory := filepath.Join(root, "spike", ".spike")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		fatal(err)
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fatal(err)
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(filepath.Join(directory, "evidence.json"), encoded, 0o644); err != nil {
		fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "SPIKE-RESULTS.md"), markdown(report, stderr.String()), 0o644); err != nil {
		fatal(err)
	}
	stablePath := filepath.Join(root, "spike", "SPIKE-RESULTS.md")
	if err := os.WriteFile(stablePath, stableMarkdown(report), 0o644); err != nil {
		fatal(err)
	}
	fmt.Printf("%s: wrote %s, %s, and %s\n", report.Status, filepath.Join(directory, "evidence.json"), filepath.Join(directory, "SPIKE-RESULTS.md"), stablePath)
	if report.Status != "pass" {
		os.Exit(1)
	}
}

func stableMarkdown(report evidence) []byte {
	var text strings.Builder
	text.WriteString("# gitseq spike results\n\n")
	text.WriteString("This tracked file is the stable projection of the six adversarial cases. Run-specific tool versions, timings, and JSON evidence are regenerated under ignored `.spike/`.\n\n")
	fmt.Fprintf(&text, "Overall: **%s**\n\n", report.Status)
	text.WriteString("| Case | Result | Evidence |\n|---|---|---|\n")
	for _, result := range report.Cases {
		tests := append([]testResult(nil), result.Tests...)
		sort.Slice(tests, func(i, j int) bool { return tests[i].Test < tests[j].Test })
		names := make([]string, 0, len(tests))
		for _, test := range tests {
			names = append(names, fmt.Sprintf("`%s` (%s)", test.Test, test.Status))
		}
		fmt.Fprintf(&text, "| %d. %s | %s | %s |\n", result.Number, result.Name, result.Status, strings.Join(names, "<br>"))
	}
	text.WriteString("\n## Findings\n\n")
	for _, result := range report.Cases {
		fmt.Fprintf(&text, "%d. %s\n", result.Number, result.Finding)
	}
	text.WriteString("\nRegenerate with `make spike` from the repository root.\n")
	return []byte(text.String())
}

func markdown(report evidence, stderr string) []byte {
	var text strings.Builder
	fmt.Fprintf(&text, "# gitseq spike results\n\nOverall: **%s**\n\n", report.Status)
	fmt.Fprintf(&text, "Environment: `%s`; `%s`\n\n", report.GoVersion, report.GitVersion)
	text.WriteString("| Case | Result | Evidence |\n|---|---|---|\n")
	for _, result := range report.Cases {
		tests := append([]testResult(nil), result.Tests...)
		sort.Slice(tests, func(i, j int) bool { return tests[i].Test < tests[j].Test })
		names := make([]string, 0, len(tests))
		for _, test := range tests {
			names = append(names, fmt.Sprintf("`%s` (%s, %.3fs)", test.Test, test.Status, test.Seconds))
		}
		fmt.Fprintf(&text, "| %d. %s | %s | %s |\n", result.Number, result.Name, result.Status, strings.Join(names, "<br>"))
	}
	text.WriteString("\n## Findings\n\n")
	for _, result := range report.Cases {
		fmt.Fprintf(&text, "%d. %s\n", result.Number, result.Finding)
	}
	if strings.TrimSpace(stderr) != "" {
		fmt.Fprintf(&text, "\n## Test runner stderr\n\n```text\n%s\n```\n", strings.TrimSpace(stderr))
	}
	return []byte(text.String())
}

func mustOutput(name string, args ...string) []byte {
	output, err := exec.Command(name, args...).Output()
	if err != nil {
		return []byte("unknown")
	}
	return output
}

// moduleRoot makes the evidence command work from either the repository root
// or a directory below it, while keeping one authoritative root go.mod.
func moduleRoot() (string, error) {
	start, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	current := start
	for {
		info, statErr := os.Stat(filepath.Join(current, "go.mod"))
		if statErr == nil && !info.IsDir() {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("find module root from %s: go.mod not found", start)
		}
		current = parent
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(2)
}
