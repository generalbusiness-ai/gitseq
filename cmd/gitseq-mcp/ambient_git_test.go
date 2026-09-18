package main

import (
	"os"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/residentclient"
	"github.com/generalbusiness-ai/gitseq/internal/testgit"
)

func TestMain(m *testing.M) {
	// The adapter reads GITSEQ_SUBMIT_DEADLINE when it builds a server, so a
	// developer who has set it for their own work would otherwise see these tests
	// answer for their environment rather than for the code. The tests that are
	// about the deadline set it themselves.
	os.Unsetenv(residentclient.SubmitDeadlineEnvironment)
	code := testgit.Run(m)
	cleanupSignedWorkspaceTemplates()
	os.Exit(code)
}
