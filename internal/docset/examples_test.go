package docset

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Gate 2, examples run. Every `sh` block in the documentation is executed
// against a scratch workroom built from nothing. A page's blocks run in order
// as one script, because an ordered walkthrough is only correct as a whole.
//
// The convention the gate enforces is: a block tagged `sh` is a command the
// reader can run, and is run here; a block tagged `text` is a form, a file, or
// output, and is not run. To keep that distinction from becoming an escape
// hatch, the gate also requires runnable coverage — every `gs` subcommand page
// must actually invoke its subcommand, and every how-to page must run
// something.

const exampleTimeout = 4 * time.Minute

func TestGateDocumentedCommandsRun(t *testing.T) {
	if testing.Short() {
		t.Skip("documented command execution belongs to the dedicated make docs gate")
	}
	requireTool(t, "git")
	requireTool(t, "bash")
	binaries := buildBinaries(t)
	root := mustRoot(t)

	for _, page := range mustPages(t, root) {
		script, first := shellScript(page)
		if script == "" {
			continue
		}
		t.Run(page.Path, func(t *testing.T) {
			t.Parallel()
			runScript(t, binaries, page, script, first)
		})
	}
}

func TestGateEveryReferenceAndRecipePageRunsSomething(t *testing.T) {
	root := mustRoot(t)
	commands, err := CLISurface(root)
	if err != nil {
		t.Fatal(err)
	}
	pages := pagesUnder(t, root, gsPages)
	for _, command := range commands {
		page, ok := pages[command.Name]
		if !ok {
			continue // reported by the surface gate
		}
		invocation := "gs " + command.Name
		found := false
		for _, block := range page.Blocks() {
			if block.Lang == "sh" && strings.Contains(block.Code, invocation) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s: no runnable `sh` block invokes `%s`; an example that is never executed cannot be trusted", page.Path, invocation)
		}
	}
	for _, page := range mustPages(t, root) {
		if path.Dir(page.Path) != DocsDir+"/how-to" {
			continue
		}
		if script, _ := shellScript(page); script == "" {
			t.Errorf("%s: a recipe with no runnable `sh` block", page.Path)
		}
	}
}

// shellScript joins a page's runnable blocks into one script and reports the
// line of the first of them.
func shellScript(page Page) (string, int) {
	var script strings.Builder
	first := 0
	for _, block := range page.Blocks() {
		if block.Lang != "sh" {
			continue
		}
		if first == 0 {
			first = block.Line
		}
		script.WriteString(block.Code)
	}
	return script.String(), first
}

func runScript(t *testing.T, binaries string, page Page, script string, line int) {
	t.Helper()
	scratch := t.TempDir()
	home := filepath.Join(scratch, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(scratch, "example.sh")
	preamble := "set -euo pipefail\ncd \"$SCRATCH\"\n"
	if err := os.WriteFile(file, []byte(preamble+script), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), exampleTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, "bash", file)
	command.Dir = scratch
	command.Env = append(os.Environ(),
		"PATH="+binaries+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOME="+home,
		"SCRATCH="+scratch,
		"PORT="+strconv.Itoa(freePort(t)),
		"GIT_CONFIG_GLOBAL="+filepath.Join(home, "gitconfig"),
		"GIT_CONFIG_SYSTEM="+os.DevNull,
		"GIT_AUTHOR_NAME=Documentation Gate",
		"GIT_AUTHOR_EMAIL=docs@example.invalid",
		"GIT_COMMITTER_NAME=Documentation Gate",
		"GIT_COMMITTER_EMAIL=docs@example.invalid",
		"GIT_TERMINAL_PROMPT=0",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s:%d: documented commands failed: %v\n--- output ---\n%s", page.Path, line, err, output)
	}
}

var (
	buildOnce sync.Once
	buildDir  string
	buildErr  error
)

// buildBinaries builds gs and gitseq-mcp once for the whole run and returns the
// directory holding them.
func buildBinaries(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		root, err := Root()
		if err != nil {
			buildErr = err
			return
		}
		directory, err := os.MkdirTemp("", "docset-bin")
		if err != nil {
			buildErr = err
			return
		}
		buildDir = directory
		for _, program := range []string{"gs", "gitseq-mcp"} {
			build := exec.Command(goTool(), "build", "-o", filepath.Join(directory, program), "./cmd/"+program)
			build.Dir = root
			if output, err := build.CombinedOutput(); err != nil {
				buildErr = fmt.Errorf("build %s: %w: %s", program, err, output)
				return
			}
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return buildDir
}

func goTool() string {
	if found, err := exec.LookPath("go"); err == nil {
		return found
	}
	if root := runtime.GOROOT(); root != "" {
		return filepath.Join(root, "bin", "go")
	}
	return "go"
}

func requireTool(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			t.Skipf("%s is not installed; the documented commands cannot be executed here", name)
		}
		t.Fatal(err)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}
