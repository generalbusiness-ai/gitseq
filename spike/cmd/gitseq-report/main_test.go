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
	report := evidence{Cases: []caseResult{{Number: 2, Tests: []testResult{{Package: "example/kernel", Test: "TestGuard", Status: "fail"}}}}}
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
