package gitseq_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestShippingLayout pins the public module and command boundary so future
// work cannot quietly move shipping code back under the adversarial spike.
func TestShippingLayout(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate layout test")
	}
	root := filepath.Dir(filename)

	required := []string{
		"go.mod",
		"cmd/gs/main.go",
		"cmd/gitseq-mcp/main.go",
		"internal/kernel/kernel.go",
		"spike/cmd/gitseq-spike/main.go",
		"spike/cmd/gitseq-report/main.go",
		"spike/SPIKE-RESULTS.md",
	}
	for _, path := range required {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); err != nil {
			t.Errorf("required layout path %s: %v", path, err)
		}
	}

	forbidden := []string{
		"spike/go.mod",
		"spike/internal",
		"spike/cmd/gs",
		"spike/cmd/gitseq-mcp",
	}
	for _, path := range forbidden {
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
