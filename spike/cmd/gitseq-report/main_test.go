package main

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestModuleRootFromRepositoryAndSpike(t *testing.T) {
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(original); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	root, err := moduleRoot()
	if err != nil {
		t.Fatal(err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{root, filepath.Join(root, "spike")} {
		if err := os.Chdir(directory); err != nil {
			t.Fatal(err)
		}
		got, err := moduleRoot()
		if err != nil {
			t.Fatal(err)
		}
		canonicalGot, err := filepath.EvalSymlinks(got)
		if err != nil {
			t.Fatal(err)
		}
		if canonicalGot != canonicalRoot {
			t.Fatalf("moduleRoot() from %s = %s, want %s", directory, canonicalGot, canonicalRoot)
		}
	}
}

func TestEvidenceDefinitionsUsePromotedModulePaths(t *testing.T) {
	const module = "github.com/generalbusiness-ai/gitseq/"
	for _, definition := range definitions {
		for _, testName := range definition.Tests {
			if !strings.HasPrefix(testName, module) {
				t.Errorf("case %d test %q is outside root module", definition.Number, testName)
			}
			if strings.Contains(testName, "gitseq/spike/internal/") {
				t.Errorf("case %d test %q still names the old module layout", definition.Number, testName)
			}
		}
	}
}

func TestRebindingEvidencePinsBothSplitVerifierGuards(t *testing.T) {
	want := map[string]bool{
		"github.com/generalbusiness-ai/gitseq/internal/kernel.TestVerifierRejectsIntentReboundToAnotherLog":              false,
		"github.com/generalbusiness-ai/gitseq/internal/kernel.TestVerifierRejectsAlteredCausalTrailersWithFreshIdentity": false,
	}
	for _, definition := range definitions {
		if definition.Number != 2 {
			continue
		}
		for _, testName := range definition.Tests {
			if _, ok := want[testName]; ok {
				want[testName] = true
			}
		}
	}
	for testName, found := range want {
		if !found {
			t.Errorf("rebinding evidence does not require %s", testName)
		}
	}
}

func TestNamedTestSelectionsContainOnlyDeclaredCases(t *testing.T) {
	selections, err := namedTestSelections(definitions)
	if err != nil {
		t.Fatal(err)
	}
	declared := make(map[string]bool)
	for _, definition := range definitions {
		for _, qualified := range definition.Tests {
			declared[qualified] = true
		}
	}
	selected := make(map[string]bool)
	for _, selection := range selections {
		if selection.Package == "./..." || len(selection.Tests) == 0 {
			t.Fatalf("unbounded selection: %#v", selection)
		}
		expression := regexp.MustCompile("^(" + strings.Join(quotedTests(selection.Tests), "|") + ")$")
		if expression.MatchString("TestUnrelatedLatencyThreshold") {
			t.Fatalf("selection %s admits an unrelated test", selection.Package)
		}
		for _, testName := range selection.Tests {
			qualified := selection.Package + "." + testName
			if !declared[qualified] {
				t.Errorf("selected undeclared test %s", qualified)
			}
			selected[qualified] = true
		}
	}
	if len(selected) != len(declared) {
		t.Fatalf("selected %d tests, want all %d declarations", len(selected), len(declared))
	}
}

func TestFailureDetailsExposeUnderlyingRunnerOutput(t *testing.T) {
	report := evidence{Status: statusFail, Cases: []caseResult{{Number: 2, Tests: []testResult{{Package: "example/kernel", Test: "TestGuard", Status: "fail"}}}}}
	details := failureDetails(report, "guard mismatch: got open\n", "compiler detail\n", errors.New("exit status 1"), nil)
	for _, wanted := range []string{
		"adversarial evidence failed",
		"case 2, example/kernel.TestGuard: fail",
		"exit status 1",
		"guard mismatch: got open",
		"compiler detail",
	} {
		if !strings.Contains(details, wanted) {
			t.Errorf("failure details omit %q:\n%s", wanted, details)
		}
	}
}

// allNamedTestsPassing builds the result set a healthy run produces, so each
// test below can spoil exactly one thing and attribute the change to it.
func allNamedTestsPassing() map[string]testResult {
	results := make(map[string]testResult)
	for _, definition := range definitions {
		for _, qualified := range definition.Tests {
			separator := strings.LastIndex(qualified, ".Test")
			results[qualified] = testResult{
				Package: qualified[:separator],
				Test:    qualified[separator+1:],
				Status:  "pass",
			}
		}
	}
	return results
}

// A red run has to say which of two different things happened. "One of the six
// adversarial security properties failed" should stop everything; "the harness
// died before it could tell us" is a runner defect. Both exit non-zero, so the
// word is the only thing carrying the difference to a reader.
func TestARedRunSaysWhetherThePropertyOrTheHarnessFailed(t *testing.T) {
	t.Run("healthy run passes", func(t *testing.T) {
		if _, status := summarise(allNamedTestsPassing(), nil, nil); status != statusPass {
			t.Fatalf("status %q, want %q", status, statusPass)
		}
	})

	t.Run("a named test failing is a property failure", func(t *testing.T) {
		results := allNamedTestsPassing()
		spoiled := definitions[1].Tests[0]
		entry := results[spoiled]
		entry.Status = "fail"
		results[spoiled] = entry

		cases, status := summarise(results, nil, nil)
		if status != statusFail {
			t.Fatalf("status %q, want %q", status, statusFail)
		}
		if cases[1].Status != statusFail {
			t.Errorf("case 2 status %q, want %q", cases[1].Status, statusFail)
		}
		if cases[0].Status != statusPass {
			t.Errorf("an unrelated case became %q; one failing test should not spoil the others", cases[0].Status)
		}
	})

	t.Run("a dead runner is inconclusive, not a property failure", func(t *testing.T) {
		// This is the case that used to read as a security regression: the
		// process errored, every case still reported pass, and the run printed
		// the same word as a genuine break.
		_, status := summarise(allNamedTestsPassing(), errors.New("signal: killed"), nil)
		if status != statusError {
			t.Fatalf("status %q, want %q", status, statusError)
		}
		if status == statusFail {
			t.Error("a harness death was reported as a failed security property")
		}
	})

	t.Run("undecodable output is inconclusive", func(t *testing.T) {
		if _, status := summarise(allNamedTestsPassing(), nil, errors.New("invalid character")); status != statusError {
			t.Fatalf("status %q, want %q", status, statusError)
		}
	})

	t.Run("a test that never reported is inconclusive", func(t *testing.T) {
		results := allNamedTestsPassing()
		delete(results, definitions[3].Tests[0])
		cases, status := summarise(results, nil, nil)
		if status != statusError {
			t.Fatalf("status %q, want %q", status, statusError)
		}
		if cases[3].Status != statusError {
			t.Errorf("case 4 status %q, want %q", cases[3].Status, statusError)
		}
	})

	t.Run("a real failure outranks a dead runner", func(t *testing.T) {
		// If both happened, the security finding is the more serious one and
		// must not be softened into a runner complaint.
		results := allNamedTestsPassing()
		spoiled := definitions[0].Tests[0]
		entry := results[spoiled]
		entry.Status = "fail"
		results[spoiled] = entry

		if _, status := summarise(results, errors.New("signal: killed"), nil); status != statusFail {
			t.Fatalf("status %q, want %q", status, statusFail)
		}
	})
}

// The distinction is worth nothing if it only lives in a JSON field. It has to
// reach the console, because a CI log is often all anyone keeps.
func TestTheHeadlineNamesWhichFailureModeItWas(t *testing.T) {
	property := headline(statusFail)
	harness := headline(statusError)
	if property == harness {
		t.Fatal("both failure modes print the same headline")
	}
	if !strings.Contains(property, "security property") {
		t.Errorf("a property failure does not say so: %q", property)
	}
	if !strings.Contains(harness, "runner defect") {
		t.Errorf("a harness failure does not say so: %q", harness)
	}
	details := failureDetails(evidence{Status: statusError}, "", "dial tcp: connection refused\n", errors.New("signal: killed"), nil)
	if !strings.Contains(details, "runner defect") {
		t.Errorf("the inconclusive headline is missing from the printed details:\n%s", details)
	}
	if !strings.Contains(details, "dial tcp: connection refused") {
		t.Errorf("the captured runner stderr did not reach the details:\n%s", details)
	}
}
