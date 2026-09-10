// Append to internal/app/admission_test.go in a temporary Go overlay. Existing imports and admissionWorkspace test helper are required.
func TestPlannerAdmissionProfileDoesNotSelectLiveSubmissionRules(t *testing.T) {
 ctx := context.Background()
 w, seed := admissionWorkspace(t, ctx)
 before, err := w.interpreter()
 if err != nil { t.Fatal(err) }
 profile, err := w.Act(ctx, "human", Act{
  Verb: VerbState, Kind: workroom.KindAdmissionProfile,
  Text: "isolated audit: name an unavailable admission contract",
  Body: map[string]string{"bundle": strings.Repeat("f", 40), "contract": "unimplemented-audit-probe", "genesis": w.config.Genesis},
  RestsOn: []string{seed.ID}, IdempotencyKey: "audit-profile",
 })
 if err != nil { t.Fatal(err) }
 _, err = w.Act(ctx, "human", Act{Verb: VerbRatify, Target: profile.Record.ID, IdempotencyKey: "audit-profile-ratification"})
 if err != nil { t.Fatal(err) }
 snapshot := w.mustSnapshot(t, ctx)
 decision, ok := snapshot.Projection.Decision(profile.Record.ID)
 if !ok || decision.Verdict != workroom.Effective { t.Fatalf("profile decision = %+v", decision) }
 selected, err := w.snapshotFolder.AdmissionProfile(w.config.Genesis)
 if err != nil { t.Fatal(err) }
 if selected.Contract != "unimplemented-audit-probe" || selected.Event != profile.Record.ID || selected.Bootstrap { t.Fatalf("selection = %+v", selected) }
 appended, err := w.Act(ctx, "human", Act{Verb: VerbState, Kind: workroom.KindAssert, Text: "submission still uses installed workroom admission", RestsOn: []string{seed.ID}, IdempotencyKey: "audit-post-profile"})
 if err != nil { t.Fatalf("actual submission refused: %v", err) }
 after := w.mustSnapshot(t, ctx)
 decision, ok = after.Projection.Decision(appended.Record.ID)
 if !ok || decision.Verdict != workroom.Effective || after.Depth != snapshot.Depth+1 { t.Fatalf("actual admission = %+v, depth=%d before=%d", decision, after.Depth, snapshot.Depth) }
 interpreter, err := w.interpreter()
 if err != nil || interpreter.projectionProfile() != before.projectionProfile() { t.Fatalf("interpreter changed: %v", err) }
 t.Logf("governance resolver selected unavailable contract %q; real Act/AcceptSubmission still appended one effective assertion; interpreter unchanged", selected.Contract)
}
