package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// A signed workspace template pays the key generation, genesis, and durable
// append costs once for the whole package. Every test receives a private copy,
// so tests keep the same filesystem and mutation isolation as t.TempDir-built
// workspaces without rebuilding identical signed history.
type signedWorkspaceTemplate struct {
	depth   int
	once    sync.Once
	root    string
	genesis workroom.Record
	err     error
}

var signedWorkspaceTemplates = struct {
	sync.Mutex
	byDepth map[int]*signedWorkspaceTemplate
}{byDepth: make(map[int]*signedWorkspaceTemplate)}

// The Go runner otherwise admits GOMAXPROCS package-local parallel tests at
// once. Four retains most of the wall-time gain without saturating the Git
// subprocess and loopback HTTP resources that the integration tests share at
// the machine boundary. Calling t.Parallel before taking the slot also keeps
// every parallel test paused until all sequential timing tests have finished.
var parallelTestSlots = make(chan struct{}, 4)

func parallelTest(t *testing.T) {
	t.Helper()
	t.Parallel()
	parallelTestSlots <- struct{}{}
	t.Cleanup(func() { <-parallelTestSlots })
}

func templateAtDepth(depth int) *signedWorkspaceTemplate {
	signedWorkspaceTemplates.Lock()
	defer signedWorkspaceTemplates.Unlock()
	template := signedWorkspaceTemplates.byDepth[depth]
	if template == nil {
		template = &signedWorkspaceTemplate{depth: depth}
		signedWorkspaceTemplates.byDepth[depth] = template
	}
	return template
}

func (template *signedWorkspaceTemplate) build() {
	template.root, template.err = os.MkdirTemp("", fmt.Sprintf("gitseq-mcp-depth-%d-", template.depth))
	if template.err != nil {
		return
	}
	repo := filepath.Join(template.root, "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		template.err = fmt.Errorf("git init: %w: %s", err, output)
		return
	}
	workspace, genesis, err := app.Init(context.Background(), repo, "human", 1<<20)
	if err != nil {
		template.err = err
		return
	}
	for index := 1; index < template.depth; index++ {
		if _, err := workspace.Act(context.Background(), "human", app.Act{
			Verb: app.VerbState, Kind: workroom.KindAssert, Text: "signed orientation history",
			RestsOn: []string{genesis.ID}, IdempotencyKey: fmt.Sprintf("orientation-history-%d", index),
		}); err != nil {
			template.err = err
			return
		}
	}
	template.genesis = genesis
}

func (template *signedWorkspaceTemplate) copy(tb testing.TB, name string) (*app.Workspace, workroom.Record) {
	tb.Helper()
	template.once.Do(template.build)
	if template.err != nil {
		tb.Fatal(template.err)
	}
	source := filepath.Join(template.root, "repo")
	destination := filepath.Join(tb.TempDir(), name)
	if err := copyTree(source, destination); err != nil {
		tb.Fatal(err)
	}
	configPath := filepath.Join(destination, ".git", "gitseq", "config.json")
	config, err := os.ReadFile(configPath)
	if err != nil {
		tb.Fatal(err)
	}
	config = bytes.ReplaceAll(config, []byte(source), []byte(destination))
	if err := os.WriteFile(configPath, config, 0o600); err != nil {
		tb.Fatal(err)
	}
	workspace, err := app.Open(context.Background(), destination)
	if err != nil {
		tb.Fatal(err)
	}
	return workspace, template.genesis
}

func copyTree(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("copy fixture %s: unsupported mode %s", path, entry.Type())
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			_ = input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		inputCloseErr := input.Close()
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if inputCloseErr != nil {
			return inputCloseErr
		}
		return closeErr
	})
}

func cleanupSignedWorkspaceTemplates() {
	signedWorkspaceTemplates.Lock()
	defer signedWorkspaceTemplates.Unlock()
	for _, template := range signedWorkspaceTemplates.byDepth {
		if template.root != "" {
			_ = os.RemoveAll(template.root)
		}
	}
}
