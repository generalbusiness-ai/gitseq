// gitseq-report runs the six adversarial cases and writes evidence projections.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

type testEvent struct {
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Output  string  `json:"Output"`
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

type testSelection struct {
	Package string
	Tests   []string
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
		"github.com/generalbusiness-ai/gitseq/internal/kernel.TestVerifierRejectsIntentReboundToAnotherLog",
		"github.com/generalbusiness-ai/gitseq/internal/kernel.TestVerifierRejectsAlteredCausalTrailersWithFreshIdentity",
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
	output, stderr, commandDescription, runErr := runNamedTests(root, goTool)

	results := make(map[string]testResult)
	var runnerOutput strings.Builder
	decoder := json.NewDecoder(bytes.NewReader(output))
	var decodeErr error
	for {
		var event testEvent
		if err := decoder.Decode(&event); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			decodeErr = err
			break
		}
		if event.Output != "" {
			runnerOutput.WriteString(event.Output)
		}
		if event.Test == "" {
			continue
		}
		if event.Action == "pass" || event.Action == "fail" || event.Action == "skip" {
			key := event.Package + "." + event.Test
			results[key] = testResult{Package: event.Package, Test: event.Test, Status: event.Action, Seconds: event.Elapsed}
		}
	}

	gitVersion := strings.TrimSpace(string(mustOutput("git", "--version")))
	report := evidence{Schema: "gitseq.spike-evidence.v0", GoVersion: runtime.Version(), GitVersion: gitVersion, Command: commandDescription, Status: "pass"}
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
	if runErr != nil || decodeErr != nil {
		report.Status = "fail"
	}
	details := stderr
	if report.Status != "pass" {
		details = failureDetails(report, runnerOutput.String(), stderr, runErr, decodeErr)
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
	if err := os.WriteFile(filepath.Join(directory, "SPIKE-RESULTS.md"), markdown(report, details), 0o644); err != nil {
		fatal(err)
	}
	stablePath := filepath.Join(root, "spike", "SPIKE-RESULTS.md")
	if err := os.WriteFile(stablePath, stableMarkdown(report), 0o644); err != nil {
		fatal(err)
	}
	fmt.Printf("%s: wrote %s, %s, and %s\n", report.Status, filepath.Join(directory, "evidence.json"), filepath.Join(directory, "SPIKE-RESULTS.md"), stablePath)
	if report.Status != "pass" {
		fmt.Fprint(os.Stderr, details)
		os.Exit(1)
	}
}

func runNamedTests(root, goTool string) ([]byte, string, string, error) {
	selections, err := namedTestSelections(definitions)
	if err != nil {
		return nil, "", "", err
	}
	var output, stderr strings.Builder
	commands := make([]string, 0, len(selections))
	var failures []error
	for _, selection := range selections {
		expression := "^(" + strings.Join(quotedTests(selection.Tests), "|") + ")$"
		args := []string{"test", "-count=1", "-json", "-run", expression, selection.Package}
		commands = append(commands, fmt.Sprintf("go test -count=1 -json -run %q %s", expression, selection.Package))
		command := exec.Command(goTool, args...)
		command.Dir = root
		var commandOutput, commandError bytes.Buffer
		command.Stdout = &commandOutput
		command.Stderr = &commandError
		if err := command.Run(); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", selection.Package, err))
		}
		output.Write(commandOutput.Bytes())
		if commandError.Len() > 0 {
			fmt.Fprintf(&stderr, "%s:\n%s", selection.Package, commandError.String())
			if !strings.HasSuffix(commandError.String(), "\n") {
				stderr.WriteByte('\n')
			}
		}
	}
	return []byte(output.String()), stderr.String(), strings.Join(commands, " && "), errors.Join(failures...)
}

func namedTestSelections(cases []caseDefinition) ([]testSelection, error) {
	byPackage := make(map[string]map[string]bool)
	for _, definition := range cases {
		for _, qualified := range definition.Tests {
			separator := strings.LastIndex(qualified, ".Test")
			if separator < 0 {
				return nil, fmt.Errorf("case %d has invalid test name %q", definition.Number, qualified)
			}
			packageName, testName := qualified[:separator], qualified[separator+1:]
			if packageName == "" || testName == "" {
				return nil, fmt.Errorf("case %d has invalid test name %q", definition.Number, qualified)
			}
			if byPackage[packageName] == nil {
				byPackage[packageName] = make(map[string]bool)
			}
			byPackage[packageName][testName] = true
		}
	}
	packages := make([]string, 0, len(byPackage))
	for packageName := range byPackage {
		packages = append(packages, packageName)
	}
	sort.Strings(packages)
	selections := make([]testSelection, 0, len(packages))
	for _, packageName := range packages {
		tests := make([]string, 0, len(byPackage[packageName]))
		for testName := range byPackage[packageName] {
			tests = append(tests, testName)
		}
		sort.Strings(tests)
		selections = append(selections, testSelection{Package: packageName, Tests: tests})
	}
	return selections, nil
}

func quotedTests(tests []string) []string {
	quoted := make([]string, len(tests))
	for index, testName := range tests {
		quoted[index] = regexp.QuoteMeta(testName)
	}
	return quoted
}

func failureDetails(report evidence, runnerOutput, stderr string, runErr, decodeErr error) string {
	var text strings.Builder
	text.WriteString("adversarial evidence failed\n")
	for _, result := range report.Cases {
		for _, test := range result.Tests {
			if test.Status != "pass" {
				fmt.Fprintf(&text, "- case %d, %s.%s: %s\n", result.Number, test.Package, test.Test, test.Status)
			}
		}
	}
	if runErr != nil {
		fmt.Fprintf(&text, "test command: %v\n", runErr)
	}
	if decodeErr != nil {
		fmt.Fprintf(&text, "decode test events: %v\n", decodeErr)
	}
	if strings.TrimSpace(runnerOutput) != "" {
		fmt.Fprintf(&text, "\nTest output:\n%s", runnerOutput)
		if !strings.HasSuffix(runnerOutput, "\n") {
			text.WriteByte('\n')
		}
	}
	if strings.TrimSpace(stderr) != "" {
		fmt.Fprintf(&text, "\nTest runner stderr:\n%s", stderr)
		if !strings.HasSuffix(stderr, "\n") {
			text.WriteByte('\n')
		}
	}
	return text.String()
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

func markdown(report evidence, runnerDetails string) []byte {
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
	if strings.TrimSpace(runnerDetails) != "" {
		fmt.Fprintf(&text, "\n## Test runner details\n\n```text\n%s\n```\n", strings.TrimSpace(runnerDetails))
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
