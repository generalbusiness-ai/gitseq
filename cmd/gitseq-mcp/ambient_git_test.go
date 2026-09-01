package main

import (
	"os"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/testgit"
)

func TestMain(m *testing.M) {
	code := testgit.Run(m)
	cleanupSignedWorkspaceTemplates()
	os.Exit(code)
}
