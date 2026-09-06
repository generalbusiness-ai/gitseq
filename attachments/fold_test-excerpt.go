// internal/workroom/fold_test.go @ 3b71806061c90fe462ba857f788dc1ec7e2d2580; original lines 3495-3505
// A report with no verdict is ordinary completion, not a review.
func TestReportWithoutVerdictIsNotAReview(t *testing.T) {
	projection := Fold(reviewRecords(t,
		event(t, "v1", other, SchemaState, State{Kind: KindReport, Text: "ready for review"}, "reviewer-promise"),
	))
	if len(projection.Reviews) != 0 {
		t.Fatalf("reviews = %+v, want none", projection.Reviews)
	}
}

// A withdrawn verdict keeps its projected independence but must not be listed
