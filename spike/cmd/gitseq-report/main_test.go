package main

import (
	"os"
	"path/filepath"
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
