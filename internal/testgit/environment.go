// Package testgit keeps Git-backed tests independent of contributor config.
//
// The boundary is process-wide because Git is invoked both by fixtures and by
// the code they exercise. One boundary per Git-using test package covers both
// without repeating an incomplete list of -c overrides at every call site.
// The motivating survey covered commit.gpgsign, core.hooksPath, core.autocrlf,
// commit.template, gpg.format, and init.defaultBranch. System, global, and
// command-scoped configuration are excluded wholesale; repository-local
// configuration remains available for tests that exercise it deliberately.
package testgit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Main runs a package's tests without reading system or global Git config.
func Main(m *testing.M) {
	os.Exit(Run(m))
}

// Run prepares a hermetic Git configuration boundary, then runs the tests.
// It is separate from Main so packages with TestMain cleanup can retain it.
func Run(m *testing.M) int {
	if err := Isolate(); err != nil {
		fmt.Fprintf(os.Stderr, "isolate test Git configuration: %v\n", err)
		return 2
	}
	return m.Run()
}

// Isolate excludes machine-level Git configuration from the current test
// process and every child it starts. Repository-local configuration remains
// visible, so a test can still exercise it deliberately.
//
// It also points GIT_TEMPLATE_DIR at a template whose config file turns off
// Git's own fsync and automatic gc. git init copies that file into every
// repository created afterwards, by a fixture or by the code under test, and
// then edits it in place, so the settings reach repository-local scope, the
// one scope the store's hermetic environment keeps. A throwaway repository has
// no durability to protect, and on macOS each object and ref write otherwise
// pays an F_FULLFSYNC, several per signed append. Code that asserts durability
// of its own files syncs them itself through os.File.Sync, which Git
// configuration does not touch. The template replaces Git's default one, so
// test repositories also start without the sample hooks, info/exclude and
// description files that default carries; nothing here reads them.
func Isolate() error {
	for _, entry := range os.Environ() {
		name, _, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		if name == "GIT_CONFIG_COUNT" || name == "GIT_CONFIG_PARAMETERS" ||
			strings.HasPrefix(name, "GIT_CONFIG_KEY_") || strings.HasPrefix(name, "GIT_CONFIG_VALUE_") {
			if err := os.Unsetenv(name); err != nil {
				return err
			}
		}
	}
	template, err := templateDir()
	if err != nil {
		return err
	}
	for name, value := range map[string]string{
		"GIT_CONFIG_NOSYSTEM": "1",
		"GIT_CONFIG_SYSTEM":   os.DevNull,
		"GIT_CONFIG_GLOBAL":   os.DevNull,
		"GIT_TEMPLATE_DIR":    template,
	} {
		if err := os.Setenv(name, value); err != nil {
			return err
		}
	}
	return nil
}

// templateConfig is the repository-local configuration every test repository
// starts with. Git init copies it verbatim before adding its own core keys.
const templateConfig = "[core]\n\tfsync = none\n[gc]\n\tauto = 0\n"

// templateDir writes the init template once per process. It lives under the
// process temporary directory and is small enough to leave behind.
func templateDir() (string, error) {
	dir, err := os.MkdirTemp("", "gitseq-testgit-template-")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte(templateConfig), 0o600); err != nil {
		return "", err
	}
	return dir, nil
}
