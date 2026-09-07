package app

import (
 "context"
 "fmt"
 "os"
 "os/exec"
 "path/filepath"
 "strings"
 "testing"

 "github.com/generalbusiness-ai/gitseq/internal/kernel"
 "github.com/generalbusiness-ai/gitseq/internal/workroom"
)

func TestPlannerCaseCRetainedAncestorDuringColdAuditAfterWitnessFailure(t *testing.T) {
 for _, missingCheckpoint := range []bool{false, true} {
 t.Run(fmt.Sprintf("missing_checkpoint_%t", missingCheckpoint), func(t *testing.T) {
 ctx := context.Background()
 w, seed, err := Init(ctx, testRepo(t), "human", 1<<20)
 if err != nil { t.Fatal(err) }
 first, err := w.Snapshot(ctx)
 if err != nil { t.Fatal(err) }
 retained := w.snapshotCache
 retainedProfile := w.snapshotProfile
 external, err := Open(ctx, w.Repo)
 if err != nil { t.Fatal(err) }
 branchB := actRecord(t, ctx, external, "human", Act{Verb: VerbState, Kind: workroom.KindAssert, Text: "branch B", RestsOn: []string{seed.ID}, IdempotencyKey: "branch-b"})
 before := storedFrontier(t, w.MetaDir)
 if before.Head != first.Head { t.Fatalf("append already advanced witness: %s != %s", before.Head, first.Head) }
 config := filepath.Join(w.MetaDir, "config.json")
 saved := config + ".planner-probe-saved"
 if err := os.Rename(config, saved); err != nil { t.Fatal(err) }
 if err := os.Mkdir(config, 0700); err != nil { t.Fatal(err) }
 _, failed := w.Snapshot(ctx)
 if err := os.Remove(config); err != nil { t.Fatal(err) }
 if err := os.Rename(saved, config); err != nil { t.Fatal(err) }
 if failed == nil || !strings.Contains(failed.Error(), "local rollback witness could not advance") { t.Fatalf("want witness storage refusal, got %v", failed) }
 if w.snapshotCache != retained || w.snapshotProfile != retainedProfile { t.Fatal("failed publication changed retained snapshot/profile") }
 readerResult, err := w.reader.Load(ctx, w.config.Genesis)
 if err != nil { t.Fatal(err) }
 if w.EventID(readerResult.Verification.Head) != branchB.ID { t.Fatal("kernel reader did not advance to B before witness refusal") }
 if got := storedFrontier(t, w.MetaDir); got != before { t.Fatal("failed witness write changed persisted frontier") }
 // All refs and keys here belong to testRepo(t), never the live workroom.
 if out, err := exec.Command("git", "-C", w.Repo, "update-ref", kernel.Ref(w.config.Genesis), first.Head).CombinedOutput(); err != nil { t.Fatalf("fixture ref: %v %s", err, out) }
 forkWriter, err := Open(ctx, w.Repo)
 if err != nil { t.Fatal(err) }
 branchC := actRecord(t, ctx, forkWriter, "human", Act{Verb: VerbState, Kind: workroom.KindAssert, Text: "branch C", RestsOn: []string{seed.ID}, IdempotencyKey: "branch-c"})
 cHead := strings.TrimPrefix(branchC.ID, "git:sha1:"+w.config.Genesis+"#git:sha1:")
 if out, err := exec.Command("git", "-C", w.Repo, "merge-base", "--is-ancestor", first.Head, cHead).CombinedOutput(); err != nil { t.Fatalf("retained is not ancestor of C: %v %s", err, out) }
 if missingCheckpoint {
 // A missing checkpoint is an ordinary supported cold-audit trigger.
 // Delete only this newly created fixture checkpoint; retain the witness at A.
 if out, err := exec.Command("git", "-C", w.Repo, "update-ref", "-d", kernel.CheckpointRef(w.config.Genesis)).CombinedOutput(); err != nil { t.Fatalf("fixture checkpoint: %v %s", err, out) }
 if err := os.Remove(w.checkpointPointerPath()); err != nil && !os.IsNotExist(err) { t.Fatal(err) }
 }
 progress := &kernel.AuditProgress{}
 observed := false
 progress.SetTestGate(func(p kernel.Progress) {
  if p.Started && w.snapshotCache == retained && w.snapshotCache.Head == first.Head && w.snapshotProfile == retainedProfile { observed = true }
 })
 final, err := w.snapshotWithSource(ctx, progress)
 if err != nil { t.Fatal(err) }
 if observed != missingCheckpoint || progress.Snapshot().Started != missingCheckpoint { t.Fatalf("unexpected cold-audit condition: missing=%v observed=%v progress=%+v source=%s", missingCheckpoint, observed, progress.Snapshot(), final.Source) }
 if final.Snapshot.Head != cHead { t.Fatal("valid branch C was not published") }
 t.Logf("witness failure=%v; retained depth=%d; kernel advanced B; cold progress=%+v; same-profile retained ancestor observed=%v; valid C published depth=%d source=%s", failed, first.Depth, progress.Snapshot(), observed, final.Snapshot.Depth, final.Source)
 })
 }
}
