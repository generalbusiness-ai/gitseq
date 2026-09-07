Exact excerpt from cmd/gs/main_test.go at 07753351d69c05dedec58f4e2561a793054312a5, lines 724–746. Full file exceeds the current preview size limit. The excerpt is copied verbatim; no test was modified or rerun for this note.

```go
// Retirement is the other fact, and it still refuses: a withdrawn pointer
// names nothing left to review.
func TestReviewGuardRefusesARetiredArtifact(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	if _, err := fixture.workspace.Act(fixture.ctx, "operator", app.Act{
		Verb: app.VerbSupersede, Target: fixture.artifact, Text: "that head is withdrawn",
		IdempotencyKey: "retire-artifact",
	}); err != nil {
		t.Fatal(err)
	}
	if artifact := artifactByEvent(t, fixture.snapshot(t).Projection, fixture.artifact); !artifact.Retired {
		t.Fatal("the supersession was ineffective, so this case is untested")
	}
	before := fixture.snapshot(t).Depth
	err := fixture.reviewError()
	if err == nil || !strings.Contains(err.Error(), "artifact is retired") {
		t.Fatalf("retired artifact review error = %v", err)
	}
	if after := fixture.snapshot(t).Depth; after != before {
		t.Fatalf("retired artifact signed a verdict: depth %d -> %d", before, after)
	}
}
```
