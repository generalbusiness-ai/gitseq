package boundary

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Inventory's evaluator and corpus proofs belong to its external module.
// This gate checks the actual repository and both production/test dependency
// graphs so an unused restored source tree or module cannot hide behind a
// passing Workroom build.
func TestInventoryStaysOutsideGitseq(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate boundary test source")
	}
	root := filepath.Join(filepath.Dir(source), "..", "..")
	removed := []string{"spike/jsonataddl", "spike/cmd/jsonata-inventory", "spike/cmd/jsonata-inventory-ui"}
	for _, path := range removed {
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(path))); !os.IsNotExist(err) {
			t.Errorf("removed inventory surface %s is present or unreadable: %v", path, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	list := func(args ...string) []string {
		t.Helper()
		cmd := exec.CommandContext(ctx, "go", append([]string{"list", "-mod=readonly"}, args...)...)
		cmd.Dir = root
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("go list %v: %v\n%s", args, err, output)
		}
		return strings.Fields(string(output))
	}
	querySandbox := false
	for _, path := range list("-deps", "-test", "-f", "{{.ImportPath}}", "./...") {
		for _, removedPath := range removed {
			if strings.HasPrefix(path, "github.com/generalbusiness-ai/gitseq/"+removedPath) {
				t.Errorf("local inventory package remains in production/test graph: %s", path)
			}
		}
		if strings.HasPrefix(path, "github.com/jsonata-go/jsonata") {
			t.Errorf("JSONata evaluator remains in production/test graph: %s", path)
		}
		if path == "github.com/generalbusiness-ai/gitseq/spike/querysandbox" {
			querySandbox = true
		}
	}
	if !querySandbox {
		t.Error("unrelated spike/querysandbox package must remain")
	}
	for _, path := range list("-m", "-f", "{{.Path}}", "all") {
		if path == "github.com/jsonata-go/jsonata" {
			t.Error("unused JSONata evaluator remains in module graph")
		}
	}
}
