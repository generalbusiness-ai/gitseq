package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/gitstore"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// associationFixture is one temporary repository with a real Git binary and a real
// workroom in it. Every proof below builds its own; nothing is shared, and
// nothing outside the temporary directory is read or written.
type associationFixture struct {
	t     *testing.T
	ctx   context.Context
	repo  string
	w     *Workspace
	seed  workroom.Record
	agent string
}

func newAssociationFixture(t *testing.T) *associationFixture {
	t.Helper()
	ctx := context.Background()
	repo := testRepo(t)
	f := &associationFixture{t: t, ctx: ctx, repo: repo}
	f.git("symbolic-ref", "HEAD", "refs/heads/main")
	f.commit("seed")
	w, seed, err := Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	f.w, f.seed = w, seed
	actor, _, err := w.AddActor(ctx, "human", "agent", "agent")
	if err != nil {
		t.Fatal(err)
	}
	f.agent = actor.Fingerprint
	return f
}

func (f *associationFixture) git(args ...string) string {
	f.t.Helper()
	return landingTestGit(f.t, f.repo, args...)
}

// commit writes one empty commit whose message is exactly what the caller
// gives. The message is the whole of the claim being tested, so nothing is
// appended to it here.
func (f *associationFixture) commit(message string) string {
	f.t.Helper()
	f.git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-q", "-m", message)
	return f.git("rev-parse", "HEAD")
}

// commitAs writes one empty commit under a chosen Git author ident. An author
// ident is committer-controlled like every other field of a commit message,
// which is the whole point of the forgery proofs below.
func (f *associationFixture) commitAs(name, email, message string) string {
	f.t.Helper()
	f.git("-c", "user.name="+name, "-c", "user.email="+email,
		"-c", "committer.name=Test", "-c", "committer.email=test@example.invalid",
		"commit", "--allow-empty", "-q", "-m", message)
	return f.git("rev-parse", "HEAD")
}

func (f *associationFixture) act(actor string, act Act) workroom.Record {
	f.t.Helper()
	return actRecord(f.t, f.ctx, f.w, actor, act)
}

// assign files a request addressed to the agent and the agent's promise on it.
func (f *associationFixture) assign(key string) (request, promise workroom.Record) {
	f.t.Helper()
	request = f.act("human", Act{Verb: VerbState, Kind: workroom.KindRequest, Text: "implement " + key,
		Body:    map[string]string{"to": f.agent, "conditions": "an approved head lands", "target_ref": "refs/heads/main"},
		RestsOn: []string{f.seed.ID}, IdempotencyKey: key + "-request"})
	promise = f.act("agent", Act{Verb: VerbState, Kind: workroom.KindPromise, Text: "I will",
		RestsOn: []string{request.ID}, IdempotencyKey: key + "-promise"})
	return request, promise
}

// artifact publishes the agent's signed statement naming one exact commit. It
// is the only thing that can promote a claim.
func (f *associationFixture) artifact(key, path, commit string, bases ...string) workroom.Record {
	f.t.Helper()
	return f.act("agent", Act{Verb: VerbState, Kind: workroom.KindArtifact, Text: "exact head",
		Body:    map[string]string{"path": path, "commit": commit},
		RestsOn: bases, IdempotencyKey: key})
}

func (f *associationFixture) snapshot() Snapshot {
	f.t.Helper()
	snapshot, err := f.w.Snapshot(f.ctx)
	if err != nil {
		f.t.Fatal(err)
	}
	return snapshot
}

// associate runs the whole layer-1 pass the way the endpoint runs it.
func (f *associationFixture) associate(views []WorktreeView) AssociationTable {
	f.t.Helper()
	return f.w.Associations(f.ctx, f.snapshot(), views)
}

// branches is every local branch, counted through git rather than through the
// bound under test.
func (f *associationFixture) branches() []string {
	f.t.Helper()
	out := f.git("for-each-ref", "--format=%(refname)", "refs/heads/")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

func (f *associationFixture) rowByHead(table AssociationTable, head string) AssociationRow {
	f.t.Helper()
	for _, row := range table.Rows {
		if row.Head == head {
			return row
		}
	}
	f.t.Fatalf("no row for head %q in %+v", head, table.Rows)
	return AssociationRow{}
}

func (f *associationFixture) row(table AssociationTable, branch string) AssociationRow {
	f.t.Helper()
	for _, row := range table.Rows {
		if row.Branch == branch {
			return row
		}
	}
	f.t.Fatalf("no row for branch %q in %+v", branch, table.Rows)
	return AssociationRow{}
}

func restsOn(id string) string { return "\n\nRests-On: " + id }

// ---------------------------------------------------------------------------
// Grading

// The forgery this whole grading exists for. A commit is written by whoever
// can write a commit, and every field of its message is theirs: the subject,
// the body, the trailer value, and the author name and email. Copying a real
// performer's ident onto a commit that names a real request produces a
// perfect-looking implementing commit and no evidence whatsoever.
func TestForgedTrailerAndCopiedAuthorIdentStayClaimed(t *testing.T) {
	f := newAssociationFixture(t)
	request, promise := f.assign("real")
	f.git("checkout", "-qb", "request/honest")
	honest := f.commit("honest work" + restsOn(request.ID))
	f.artifact("honest-artifact", "internal/thing", honest, promise.ID)

	// The forger names the same request, copies the performer's author ident,
	// and signs nothing, because signing needs a key they have not got.
	f.git("checkout", "-q", "main")
	f.git("checkout", "-qb", "request/forged")
	forged := f.commitAs("agent", "agent@example.invalid", "identical-looking work"+restsOn(request.ID))
	if forged == honest {
		t.Fatal("the fixture produced one commit for two branches")
	}

	table := f.associate(nil)
	honestRow, forgedRow := f.row(table, "request/honest"), f.row(table, "request/forged")
	if honestRow.Grade != GradeCorroborated {
		t.Fatalf("the signed head is not corroborated: %+v", honestRow)
	}
	if honestRow.Governing != request.ID {
		t.Fatalf("corroborated row names %q, want the request %q", honestRow.Governing, request.ID)
	}
	if forgedRow.Grade != GradeClaimed {
		t.Fatalf("a forged trailer with a copied author ident graded %q, want %q", forgedRow.Grade, GradeClaimed)
	}
	if forgedRow.Governing != request.ID {
		t.Fatalf("the forged claim should still be reported as a claim: %+v", forgedRow)
	}
	for _, claim := range forgedRow.Claims {
		if claim.Artifact != "" || claim.Actor != "" {
			t.Fatalf("forged claim carries evidence it has none of: %+v", claim)
		}
	}

	// And it never widens the deletable set.
	views := []WorktreeView{{Checkout: "forged", Branch: "request/forged", Head: forged, State: "clean"}}
	if deletable := f.w.ClassifyWorktrees(f.ctx, f.snapshot().Projection, views); len(deletable) != 0 {
		t.Fatalf("a forged claim made a checkout deletable: %v %+v", deletable, views[0])
	}
}

// Corroboration attaches to a commit and never to a branch. A tip beyond the
// commit somebody signed for is not the head they signed for, and grading the
// branch by its best commit would say otherwise.
func TestCorroborationAttachesToTheCommitNotTheBranch(t *testing.T) {
	f := newAssociationFixture(t)
	request, promise := f.assign("percommit")
	f.git("checkout", "-qb", "request/percommit")
	signed := f.commit("reviewed work" + restsOn(request.ID))
	f.artifact("signed-artifact", "internal/thing", signed, promise.ID)

	if row := f.row(f.associate(nil), "request/percommit"); row.Grade != GradeCorroborated || row.CorroboratedAt != signed {
		t.Fatalf("the signed tip is not corroborated at itself: %+v", row)
	}

	later := f.commit("work after the signature" + restsOn(request.ID))
	row := f.row(f.associate(nil), "request/percommit")
	if row.Head != later {
		t.Fatalf("the row did not follow the branch: %+v", row)
	}
	if row.Grade != GradeClaimed {
		t.Fatalf("a tip past the signed commit graded %q, want %q", row.Grade, GradeClaimed)
	}
	if row.CorroboratedAt != signed {
		t.Fatalf("the corroborated commit is not named: %+v", row)
	}
	var gradesByCommit = map[string]string{}
	for _, claim := range row.Claims {
		gradesByCommit[claim.Commit] = claim.Grade
	}
	if gradesByCommit[signed] != GradeCorroborated || gradesByCommit[later] != GradeClaimed {
		t.Fatalf("per-commit grades wrong: %+v", gradesByCommit)
	}
}

// Evidence has to reach the record the commit claims. A signed artifact naming
// this exact commit is not enough on its own: the same performer signing under
// a different assignment says nothing about the assignment the commit names,
// and treating it as proof would let any of an actor's real artifacts vouch for
// any claim they ever wrote.
func TestEvidenceMustReachTheRecordTheCommitClaims(t *testing.T) {
	f := newAssociationFixture(t)
	claimed, _ := f.assign("claimed")
	_, elsewhere := f.assign("elsewhere")
	f.git("checkout", "-qb", "request/mismatched")
	head := f.commit("work naming one request" + restsOn(claimed.ID))
	// A real, standing, signed artifact of the same actor, naming this exact
	// commit, filed under the other assignment.
	f.artifact("mismatched-artifact", "internal/thing", head, elsewhere.ID)

	row := f.row(f.associate(nil), "request/mismatched")
	if row.Governing != claimed.ID {
		t.Fatalf("the claim is not reported: %+v", row)
	}
	if row.Grade != GradeClaimed {
		t.Fatalf("evidence filed under another assignment graded the claim %q, want %q", row.Grade, GradeClaimed)
	}
	if row.CorroboratedAt != "" {
		t.Fatalf("a mismatched artifact corroborated a commit: %+v", row)
	}
}

// An identifier of another workroom names an event this room cannot verify. It
// is carried exactly as typed and never resolved against anything local, so a
// value from a foreign genesis can never collide its way into evidence here.
func TestAssociationCarriesAForeignGenesisAsTyped(t *testing.T) {
	f := newAssociationFixture(t)
	foreign := "git:sha1:" + strings.Repeat("b", 40) + "#git:sha1:" + strings.Repeat("c", 40)
	f.git("checkout", "-qb", "request/foreign")
	f.commit("work for somewhere else" + restsOn(foreign))

	row := f.row(f.associate(nil), "request/foreign")
	if row.Grade != GradeForeign {
		t.Fatalf("a foreign identifier graded %q, want %q", row.Grade, GradeForeign)
	}
	if row.Governing != "" {
		t.Fatalf("a foreign identifier resolved to a local record: %+v", row)
	}
	if len(row.Claims) != 1 || row.Claims[0].Selector != foreign {
		t.Fatalf("the foreign value was not carried as typed: %+v", row.Claims)
	}
}

// Two ways of naming nothing this room holds, and one way of naming too much.
// An ambiguous selector carries the resolver's own typed refusal, candidates
// and all, rather than picking a winner; a resolvable-looking identifier of
// this room that names no record is unresolved and refuses nothing.
func TestAssociationReportsAmbiguousAndMissingIdentityWithoutChoosing(t *testing.T) {
	f := newAssociationFixture(t)
	for i := 0; i < 6; i++ {
		f.assign(fmt.Sprintf("filler-%d", i))
	}
	snapshot := f.snapshot()

	// A fragment several event hashes share. It is found from the projection
	// rather than assumed, because which hashes a run produces is not fixed.
	fragment := ""
	for _, character := range "0123456789abcdef" {
		matched := 0
		for _, decision := range snapshot.Projection.Decisions {
			hash := decision.Event[strings.LastIndex(decision.Event, ":")+1:]
			if strings.HasPrefix(hash, string(character)) || strings.HasSuffix(hash, string(character)) {
				matched++
			}
		}
		if matched > 1 {
			fragment = string(character)
			break
		}
	}
	if fragment == "" {
		t.Skip("no one-character fragment matched two event hashes in this run")
	}
	missing := "git:sha1:" + snapshot.Genesis + "#git:sha1:" + strings.Repeat("d", 40)

	f.git("checkout", "-qb", "request/ambiguous")
	f.commit("ambiguous claim" + restsOn(fragment))
	f.git("checkout", "-q", "main")
	f.git("checkout", "-qb", "request/missing")
	f.commit("missing claim" + restsOn(missing))

	table := f.associate(nil)
	ambiguous := f.row(table, "request/ambiguous")
	if ambiguous.Grade != GradeUnresolved || ambiguous.Governing != "" {
		t.Fatalf("an ambiguous selector was resolved: %+v", ambiguous)
	}
	if len(ambiguous.Claims) != 1 || len(ambiguous.Claims[0].Candidates) < 2 {
		t.Fatalf("the ambiguity refusal did not carry its candidates: %+v", ambiguous.Claims)
	}
	if !strings.Contains(ambiguous.Claims[0].Reason, "name one exactly") {
		t.Fatalf("the typed refusal was not carried verbatim: %q", ambiguous.Claims[0].Reason)
	}

	absent := f.row(table, "request/missing")
	if absent.Grade != GradeUnresolved || absent.Governing != "" {
		t.Fatalf("an identifier naming no record here graded %+v", absent)
	}
	if len(absent.Claims) != 1 || absent.Claims[0].Selector != missing {
		t.Fatalf("the missing selector was not carried as typed: %+v", absent.Claims)
	}
	// Unresolved is not a refusal to act. The other rows still answered.
	if !table.Complete {
		t.Fatal("an unresolved selector discarded the whole table")
	}
}

// A recut is the same governing record and a different commit. Two attempts
// must stay two rows: the one an artifact names is corroborated and the other
// is claimed, and neither inherits the other's evidence.
func TestAssociationSeparatesRecutsOfOneGoverningRecord(t *testing.T) {
	f := newAssociationFixture(t)
	request, promise := f.assign("recut")
	f.git("checkout", "-qb", "request/attempt-one")
	first := f.commit("first attempt" + restsOn(request.ID))
	f.git("checkout", "-q", "main")
	f.git("checkout", "-qb", "request/attempt-two")
	second := f.commit("second attempt" + restsOn(request.ID))
	f.artifact("recut-artifact", "internal/thing", second, promise.ID)

	table := f.associate(nil)
	one, two := f.row(table, "request/attempt-one"), f.row(table, "request/attempt-two")
	if one.Head == two.Head {
		t.Fatal("the fixture produced one commit for two attempts")
	}
	if one.Governing != request.ID || two.Governing != request.ID {
		t.Fatalf("both attempts should claim %q: %+v %+v", request.ID, one, two)
	}
	if one.Grade != GradeClaimed {
		t.Fatalf("the unsigned attempt graded %q, want %q", one.Grade, GradeClaimed)
	}
	if two.Grade != GradeCorroborated || two.CorroboratedAt != second {
		t.Fatalf("the signed attempt is not corroborated at its own commit: %+v", two)
	}
	if one.CorroboratedAt != "" {
		t.Fatalf("the unsigned attempt inherited the other's evidence: %+v", one)
	}
	_ = first
}

// Self-initiated work files no request, so the record its commits name is the
// adopted decision. The same adoption rule the review guard applies is the one
// applied here; a proposal nobody ratified corroborates nothing.
func TestSelfInitiatedClaimsAreCorroboratedByTheAdoptedDecision(t *testing.T) {
	f := newAssociationFixture(t)
	unratified := f.act("human", Act{Verb: VerbState, Kind: workroom.KindPropose, Text: "do the thing",
		RestsOn: []string{f.seed.ID}, IdempotencyKey: "proposal"})
	f.git("checkout", "-qb", "self/initiated")
	head := f.commit("self-initiated work" + restsOn(unratified.ID))
	f.artifact("self-artifact", "internal/thing", head, unratified.ID)

	if row := f.row(f.associate(nil), "self/initiated"); row.Grade != GradeClaimed {
		t.Fatalf("an unratified proposal corroborated a claim: %+v", row)
	}
	f.act("human", Act{Verb: VerbRatify, Target: unratified.ID, IdempotencyKey: "adopt"})
	row := f.row(f.associate(nil), "self/initiated")
	if row.Grade != GradeCorroborated || row.Claims[0].Decision != unratified.ID {
		t.Fatalf("the adopted decision did not corroborate its own work: %+v", row)
	}
}

// ---------------------------------------------------------------------------
// Bounds and the cache key

// The frontier alone cannot key this table. A branch moves, is renamed and is
// deleted while nothing at all is appended to the log, and a table keyed on
// the frontier would keep answering about a tip that has gone.
func TestAssociationRemeasuresAfterBranchMovementAndRename(t *testing.T) {
	f := newAssociationFixture(t)
	request, _ := f.assign("moving")
	f.git("checkout", "-qb", "request/moving")
	first := f.commit("first" + restsOn(request.ID))

	before := f.snapshot()
	if row := f.row(f.associate(nil), "request/moving"); row.Head != first {
		t.Fatalf("row head = %q, want %q", row.Head, first)
	}
	// Nothing is appended, so the frontier is unchanged from here on.
	moved := f.commit("second" + restsOn(request.ID))
	after := f.snapshot()
	if before.Head != after.Head || before.Depth != after.Depth {
		t.Fatal("the fixture moved the durable frontier; the cache key would be invalidated for the wrong reason")
	}
	if row := f.row(f.associate(nil), "request/moving"); row.Head != moved {
		t.Fatalf("a branch that moved at an unchanged frontier answered from cache: %+v", row)
	}

	f.git("branch", "-m", "request/moving", "request/renamed")
	table := f.associate(nil)
	if row := f.row(table, "request/renamed"); row.Head != moved || row.Governing != request.ID {
		t.Fatalf("the renamed branch is not associated: %+v", row)
	}
	for _, row := range table.Rows {
		if row.Branch == "request/moving" {
			t.Fatal("the old branch name survived its rename")
		}
	}

	f.git("checkout", "-q", "main")
	f.git("branch", "-D", "request/renamed")
	for _, row := range f.associate(nil).Rows {
		if row.Branch == "request/renamed" {
			t.Fatal("a deleted branch survived in the table")
		}
	}
}

// The cache is keyed on every input the table is derived from, and it really is
// a cache: a second read at an unchanged world does not re-derive. This covers
// the frontier and the ref digest; the checkout digest has its own proof
// above, and the grades this pass writes back are outputs that never key it.
func TestAssociationCachesUnderTheFrontierAndRefDigest(t *testing.T) {
	f := newAssociationFixture(t)
	request, _ := f.assign("cached")
	f.git("checkout", "-qb", "request/cached")
	head := f.commit("work" + restsOn(request.ID))
	snapshot := f.snapshot()

	first := f.w.Associations(f.ctx, snapshot, nil)
	if !first.Complete {
		t.Fatalf("first pass incomplete: %+v", first)
	}
	key := f.w.associationsKey
	if key.frontier == "" || key.refs == "" {
		t.Fatalf("the cache key is not both facts: %+v", key)
	}
	// A second pass at the same world answers from the cache, and hands out a
	// copy rather than the cached rows themselves.
	second := f.w.Associations(f.ctx, snapshot, nil)
	if f.w.associationsKey != key {
		t.Fatal("an unchanged world re-derived the table")
	}
	if len(second.Rows) == 0 || &second.Rows[0] == &f.w.associationsCached.Rows[0] {
		t.Fatal("the cache handed out its own rows")
	}

	// A ref that moves at an unchanged frontier invalidates the key.
	f.git("update-ref", "refs/heads/unrelated", head)
	if third := f.w.Associations(f.ctx, snapshot, nil); f.w.associationsKey == key {
		t.Fatalf("a new ref at an unchanged frontier answered from cache: %+v", third)
	}
	if row := f.row(f.w.Associations(f.ctx, f.snapshot(), nil), "unrelated"); row.Governing != request.ID {
		t.Fatalf("the new ref was not associated: %+v", row)
	}
}

// Budget exhaustion is unknown, and unknown is the whole batch. A partial
// association would read as an absence of work rather than an unfinished look
// for it, and the checkouts it silently dropped would look unclaimed.
func TestAssociationDiscardsTheWholeBatchOnBudgetExhaustion(t *testing.T) {
	f := newAssociationFixture(t)
	request, _ := f.assign("bounded")
	f.git("checkout", "-qb", "request/bounded")
	head := f.commit("work" + restsOn(request.ID))
	views := []WorktreeView{{Checkout: "bounded", Branch: "request/bounded", Head: head, State: "clean"}}

	projection := f.snapshot().Projection
	capture := func(remaining int) *CheckoutRead {
		read := f.w.captureAround(f.ctx, views)
		read.budget.remaining = remaining
		return read
	}
	full := capture(worktreeInspectionLimit)
	defer full.Close()
	complete := f.w.associate(full, projection)
	if !complete.Complete || len(complete.Rows) == 0 {
		t.Fatalf("the full budget did not finish: %+v", complete)
	}
	// Every budget short of what the pass costs must produce nothing, not the
	// part of the answer it reached. Sweeping the whole range rather than
	// picking a value is what makes this a bound rather than an anecdote.
	cost := worktreeInspectionLimit - full.budget.remaining
	if cost < 4 {
		t.Fatalf("the pass cost %d steps; the sweep below would prove nothing", cost)
	}
	for remaining := 0; remaining < cost; remaining++ {
		short := capture(remaining)
		table := f.w.associate(short, projection)
		short.Close()
		if table.Complete || len(table.Rows) != 0 {
			t.Fatalf("a %d-step budget produced part of a %d-step answer: %+v", remaining, cost, table)
		}
		if table.Reason == "" {
			t.Fatalf("an exhausted pass gave no reason: %+v", table)
		}
	}

	// And through the entry point, where an exhausted pass must also leave
	// every view unknown rather than blank. A cancelled context exhausts the
	// same budget without reaching inside for it.
	cancelled, stop := context.WithCancel(f.ctx)
	stop()
	views[0].Grade, views[0].Governing = GradeClaimed, request.ID
	if table := f.w.Associations(cancelled, f.snapshot(), views); table.Complete || len(table.Rows) != 0 {
		t.Fatalf("a cancelled read answered: %+v", table)
	}
	if views[0].Grade != GradeUnknown || views[0].Governing != "" {
		t.Fatalf("a cancelled read left a graded view: %+v", views[0])
	}
}

// Layer 1 writes nothing, anywhere. The whole repository is compared byte for
// byte around the pass rather than trusted to a reading of the code.
func TestTheAssociationPassWritesNothing(t *testing.T) {
	f := newAssociationFixture(t)
	request, promise := f.assign("readonly")
	f.git("checkout", "-qb", "request/readonly")
	head := f.commit("work" + restsOn(request.ID))
	f.artifact("readonly-artifact", "internal/thing", head, promise.ID)
	snapshot := f.snapshot()
	views := []WorktreeView{{Checkout: "readonly", Branch: "request/readonly", Head: head, State: "clean"}}

	// Warm every cache first, so what is measured is the pass and not the
	// checkpoint writes an ordinary first read is allowed to make.
	f.w.Associations(f.ctx, snapshot, views)
	f.w.ClassifyWorktrees(f.ctx, snapshot.Projection, views)

	before := treeDigest(t, f.repo)
	table := f.w.Associations(f.ctx, snapshot, views)
	f.w.ClassifyWorktrees(f.ctx, snapshot.Projection, views)
	if !table.Complete {
		t.Fatalf("the measured pass did not run: %+v", table)
	}
	if after := treeDigest(t, f.repo); after != before {
		t.Fatal("the read-only association changed the repository")
	}
}

// treeDigest is every path under root with its size, mode and content hash.
func treeDigest(t *testing.T, root string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, statErr := entry.Info()
		if statErr != nil {
			return statErr
		}
		line := fmt.Sprintf("%s %o %d", path, info.Mode(), info.Size())
		if entry.Type().IsRegular() {
			content, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			sum := sha256.Sum256(content)
			line += " " + hex.EncodeToString(sum[:])
		}
		lines = append(lines, line)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

// ---------------------------------------------------------------------------
// What the cache is keyed on, and what a bound is allowed to say

// A detached checkout moves, or a checkout is added, removed or renamed, and
// no ref and no durable record changes. The table's rows carry checkout labels
// and rows built for exactly those detached heads, so a cache keyed on the
// frontier and the refs alone does not go stale after a while: it answers
// about checkout A for as long as nothing unrelated happens to move.
func TestAssociationCacheIsKeyedOnTheCapturedCheckoutsToo(t *testing.T) {
	f := newAssociationFixture(t)
	request, _ := f.assign("detached-cache")
	base := f.git("rev-parse", "main")
	tree := f.git("rev-parse", "main^{tree}")
	// Real unreferenced commits: no ref points at either, so nothing about the
	// ref inventory distinguishes the two calls below.
	detachedHead := func(text string) string {
		return f.git("-c", "user.name=Test", "-c", "user.email=test@example.invalid",
			"commit-tree", tree, "-p", base, "-m", text+restsOn(request.ID))
	}
	first, second := detachedHead("detached A"), detachedHead("detached B")
	if first == second {
		t.Fatal("the fixture produced one commit for two detached heads")
	}
	snapshot := f.snapshot()

	before := []WorktreeView{{Checkout: "detached", Head: first, Detached: true, State: "clean"}}
	cold := f.w.Associations(f.ctx, snapshot, before)
	if !cold.Complete || before[0].Grade != GradeClaimed || before[0].Governing != request.ID {
		t.Fatalf("the cold read did not grade the first detached head: %+v %+v", cold, before[0])
	}

	after := []WorktreeView{{Checkout: "detached", Head: second, Detached: true, State: "clean"}}
	warm := f.w.Associations(f.ctx, snapshot, after)
	if after[0].Grade != GradeClaimed || after[0].Governing != request.ID {
		t.Fatalf("a moved detached head answered from the first head's cache: %+v", after[0])
	}
	found := false
	for _, row := range warm.Rows {
		if row.Head == first {
			t.Fatalf("the table still carries the head that has gone: %+v", row)
		}
		if row.Head == second {
			found = true
		}
	}
	if !found {
		t.Fatalf("no row for the current detached head %s: %+v", second, warm.Rows)
	}

	// A checkout that appears, and one that is renamed, are the same kind of
	// change and must invalidate the same way.
	added := []WorktreeView{
		{Checkout: "detached", Head: second, Detached: true, State: "clean"},
		{Checkout: "another", Head: second, Detached: true, State: "clean"},
	}
	if labels := f.rowByHead(f.w.Associations(f.ctx, snapshot, added), second).Checkouts; len(labels) != 2 {
		t.Fatalf("a second checkout at the same head is missing from the row: %v", labels)
	}
	renamed := []WorktreeView{{Checkout: "renamed", Head: second, Detached: true, State: "clean"}}
	if got := f.rowByHead(f.w.Associations(f.ctx, snapshot, renamed), second).Checkouts; len(got) != 1 || got[0] != "renamed" {
		t.Fatalf("a renamed checkout answered from the old label's cache: %v", got)
	}

	// The ref invalidation the cache already had must survive all of this.
	key := f.w.associationsKey
	f.git("update-ref", "refs/heads/unrelated", base)
	f.w.Associations(f.ctx, snapshot, renamed)
	if f.w.associationsKey == key {
		t.Fatal("a new ref at an unchanged frontier answered from cache")
	}
}

// The tip limit bounds the work; it does not license an answer that looks
// finished. A table of the first 256 branches marked complete is a wrong
// answer about the 257th, not a partial answer about all of them.
func TestMoreTipsThanTheLimitReadsIsIncompleteNotTruncated(t *testing.T) {
	f := newAssociationFixture(t)
	request, _ := f.assign("tip-overflow")
	f.git("checkout", "-qb", "request/overflow")
	head := f.commit("work" + restsOn(request.ID))
	f.git("checkout", "-q", "main")
	views := []WorktreeView{{Checkout: "overflow", Branch: "request/overflow", Head: head, State: "clean"}}

	// One below the limit still answers, so the test proves a boundary rather
	// than a permanent refusal.
	for i := 0; len(f.branches()) < gitstore.LineageTipLimit; i++ {
		f.git("update-ref", fmt.Sprintf("refs/heads/extra-%03d", i), head)
	}
	if table := f.w.Associations(f.ctx, f.snapshot(), views); !table.Complete || len(table.Rows) != gitstore.LineageTipLimit {
		t.Fatalf("exactly the tip limit did not answer: complete=%v rows=%d", table.Complete, len(table.Rows))
	}
	if views[0].Grade != GradeClaimed {
		t.Fatalf("exactly the tip limit left a checkout ungraded: %+v", views[0])
	}

	f.git("update-ref", "refs/heads/one-too-many", head)
	table := f.w.Associations(f.ctx, f.snapshot(), views)
	if table.Complete {
		t.Fatalf("%d branches were clipped to %d rows and reported complete", len(f.branches()), len(table.Rows))
	}
	if len(table.Rows) != 0 || table.Reason == "" {
		t.Fatalf("an over-limit read kept partial rows or gave no reason: %+v", table)
	}
	if views[0].Grade != GradeUnknown || views[0].Governing != "" {
		t.Fatalf("an over-limit read left a checkout graded: %+v", views[0])
	}
}

// The per-tip depth limit is the same question one level down. A lineage
// longer than the limit reads is cut off, and the commits it never reached
// carry claims it therefore cannot report.
func TestALineagePastThePerTipLimitIsIncompleteNotTruncated(t *testing.T) {
	f := newAssociationFixture(t)
	request, _ := f.assign("depth")
	f.git("checkout", "-qb", "request/deep")
	// One commit carries the trailer and the rest are filler, so the answer
	// this pass would give from a truncated read is visibly wrong rather than
	// merely short.
	head := f.commit("claimed work" + restsOn(request.ID))
	for i := 0; i < gitstore.LineageCommitLimit-1; i++ {
		head = f.commit(fmt.Sprintf("filler %d", i))
	}
	f.git("checkout", "-q", "main")
	views := []WorktreeView{{Checkout: "deep", Branch: "request/deep", Head: head, State: "clean"}}

	// Exactly the limit is readable and still names the claim at its root.
	if table := f.w.Associations(f.ctx, f.snapshot(), views); !table.Complete {
		t.Fatalf("a lineage of exactly the per-tip limit reported incomplete: %+v", table)
	} else if row := f.row(table, "request/deep"); row.Governing != request.ID {
		t.Fatalf("the deepest claim was not read: %+v", row)
	}

	f.git("checkout", "-q", "request/deep")
	head = f.commit("one commit too many")
	f.git("checkout", "-q", "main")
	views = []WorktreeView{{Checkout: "deep", Branch: "request/deep", Head: head, State: "clean"}}
	table := f.w.Associations(f.ctx, f.snapshot(), views)
	if table.Complete {
		t.Fatalf("a lineage past the per-tip limit was reported complete: %+v", table)
	}
	if len(table.Rows) != 0 || table.Reason == "" {
		t.Fatalf("an over-depth read kept partial rows or gave no reason: %+v", table)
	}
	if views[0].Grade != GradeUnknown {
		t.Fatalf("an over-depth read left a checkout graded: %+v", views[0])
	}
}

// A captured tip whose object this repository does not hold is an answer this
// pass has not got. Reading it as an empty history says the checkout claims
// nothing, which is the negative an unfinished read must never prove. An
// unborn repository, which really does hold no commits, stays complete.
func TestAnUnavailableTipIsIncompleteAndNotAnEmptyHistory(t *testing.T) {
	f := newAssociationFixture(t)
	request, _ := f.assign("missing")
	f.git("checkout", "-qb", "request/present")
	present := f.commit("work" + restsOn(request.ID))
	f.git("checkout", "-q", "main")

	absent := strings.Repeat("f", 40)
	views := []WorktreeView{
		{Checkout: "present", Branch: "request/present", Head: present, State: "clean"},
		{Checkout: "missing", Head: absent, Detached: true, State: "clean"},
	}
	table := f.w.Associations(f.ctx, f.snapshot(), views)
	if table.Complete {
		t.Fatalf("a tip this repository does not hold was reported in a complete table: %+v", table)
	}
	if table.Reason == "" || len(table.Rows) != 0 {
		t.Fatalf("an unavailable tip kept partial rows or gave no reason: %+v", table)
	}
	// The unknown must survive the annotation step, which is the last thing to
	// touch these views.
	for _, view := range views {
		if view.Grade != GradeUnknown || view.Governing != "" {
			t.Fatalf("an unavailable tip left checkout %q graded %q: %+v", view.Checkout, view.Grade, view)
		}
	}

	// The same repository without that checkout answers completely, so the
	// refusal is about the missing object and not about the repository.
	held := []WorktreeView{{Checkout: "present", Branch: "request/present", Head: present, State: "clean"}}
	if complete := f.w.Associations(f.ctx, f.snapshot(), held); !complete.Complete || held[0].Grade != GradeClaimed {
		t.Fatalf("the same repository without the missing tip did not answer: %+v %+v", complete, held[0])
	}
}
