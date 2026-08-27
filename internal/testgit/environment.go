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
	for name, value := range map[string]string{
		"GIT_CONFIG_NOSYSTEM": "1",
		"GIT_CONFIG_SYSTEM":   os.DevNull,
		"GIT_CONFIG_GLOBAL":   os.DevNull,
	} {
		if err := os.Setenv(name, value); err != nil {
			return err
		}
	}
	return nil
}
