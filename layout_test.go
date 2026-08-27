package gitseq_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestShippingLayout pins the layout facts that neither compilation nor the
// documentation gates prove: the public module name, the tracked spike result
// promised to readers, and the absence of the shipping paths that were moved
// out of the adversarial spike. Compilation proves a path exists; nothing but
// this proves one stays gone.
func TestShippingLayout(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate layout test")
	}
	root := filepath.Dir(filename)

	if _, err := os.Stat(filepath.Join(root, "spike", "SPIKE-RESULTS.md")); err != nil {
		t.Errorf("tracked spike result: %v", err)
	}

	// Shipping code moved out of the spike. Nothing else asserts these stay
	// absent: a nested spike/go.mod would quietly drop the spike packages from
	// the module the one CI suite runs, and re-created command or internal
	// trees would compile perfectly well beside the real ones.
	for _, path := range []string{
		"spike/go.mod",
		"spike/internal",
		"spike/cmd/gs",
		"spike/cmd/gitseq-mcp",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); !os.IsNotExist(err) {
			t.Errorf("obsolete shipping path %s still exists", path)
		}
	}

	module, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(module), "module github.com/generalbusiness-ai/gitseq\n") {
		t.Fatalf("unexpected public module declaration: %q", strings.SplitN(string(module), "\n", 2)[0])
	}
}
