package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"

	"github.com/generalbusiness-ai/gitseq/internal/eventref"
	"github.com/generalbusiness-ai/gitseq/internal/gitstore"
	"github.com/generalbusiness-ai/gitseq/internal/reviewguard"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// The reverse association answers one question and grants nothing: which
// durable record does the work on this branch or in this checkout claim, and
// is that claim corroborated by anything anybody signed.
//
// Two different trailers wear the name `Rests-On:`. A kernel event commit
// carries one in a signed envelope that the sequencer compares byte for byte
// with the signed intent, so that one is unforgeable. An implementing source
// commit carries one as ordinary message text that nobody signs and nothing
// verifies. The commit hash seals the bytes of such a claim, never its truth:
// anyone who can write a commit can name any event, and can copy a real
// performer's author name and email onto it while they are there, because the
// author ident is committer-controlled like every other field of the message.
//
// So a source trailer is graded, not believed. Nothing here widens the
// deletable set, authorises a signature, or moves a merge.
const (
	// GradeClaimed is a resolvable event named by an unsigned source trailer.
	// It is a claim and nothing more.
	GradeClaimed = "claimed"
	// GradeCorroborated is a claim that authenticated durable evidence backs: a
	// standing artifact statement naming this exact commit, whose signing
	// actor the review guard's own owned edge ties to the claimed governing
	// record. Promotion needs that actor's key, which is what a forger has
	// not got.
	GradeCorroborated = "corroborated"
	// GradeUnresolved is a trailer naming no event of this workroom, or more
	// than one. The typed refusal and its candidates are carried verbatim.
	GradeUnresolved = "unresolved"
	// GradeForeign is a canonical identifier of another workroom. It is carried
	// as typed and never resolved against this room's events.
	GradeForeign = "foreign"
	// GradeUnknown is what every row says when the pass could not finish.
	// Unknown never proves a negative.
	GradeUnknown = "unknown"
)

// associationClaimLimit bounds how many claims one row reports. A row that
// reaches it says so with Truncated rather than dropping the rest in silence.
const associationClaimLimit = worktreeRowLimit

// AssociationClaim is one implementing commit's `Rests-On:` trailer, as read
// and as graded. Selector is the trailer exactly as the commit carries it;
// Governing is what it resolved to here, and is empty unless it resolved.
type AssociationClaim struct {
	Commit    string `json:"commit"`
	Selector  string `json:"selector"`
	Governing string `json:"governing,omitempty"`
	Grade     string `json:"grade"`
	// Artifact, Actor, Request, Promise and Decision are the corroborating
	// evidence, present only on a corroborated claim. Actor is the actor an
	// event signature names. No Git ident is read, matched or mapped here.
	Artifact string `json:"artifact,omitempty"`
	Actor    string `json:"actor,omitempty"`
	Request  string `json:"request,omitempty"`
	Promise  string `json:"promise,omitempty"`
	Decision string `json:"decision,omitempty"`
	// Reason and Candidates carry the resolver's own typed refusal for an
	// unresolved selector, including the ambiguity candidate list.
	Reason     string   `json:"reason,omitempty"`
	Candidates []string `json:"candidates,omitempty"`
	// CandidatesOmitted counts the ambiguity candidates the refusal did not
	// name. The resolver caps its list at eight.
	CandidatesOmitted int `json:"candidates_omitted,omitempty"`
}

// AssociationRow is one branch tip or one detached checkout head, and what the
// implementing commits on its first-parent lineage claim.
//
// Grade is the tip's own grade, not the branch's best. Corroboration attaches
// to a commit and never to a branch: a tip whose lineage holds a corroborated
// commit is corroborated at that commit and claimed beyond it, because the
// later commits are not the head anyone signed for. CorroboratedAt names that
// commit when the tip itself is not the one signed for.
type AssociationRow struct {
	Branch         string             `json:"branch,omitempty"`
	Checkouts      []string           `json:"checkouts,omitempty"`
	Head           string             `json:"head,omitempty"`
	Grade          string             `json:"grade"`
	Governing      string             `json:"governing,omitempty"`
	CorroboratedAt string             `json:"corroborated_at,omitempty"`
	Claims         []AssociationClaim `json:"claims,omitempty"`
	Truncated      bool               `json:"truncated,omitempty"`
}

// AssociationTable is the whole read-only reverse association. Complete is
// false when a bound, a missing object or a cancelled read stopped the pass;
// the rows are then unknown, because a partial association would read as an
// absence of work rather than as an unfinished look for it.
type AssociationTable struct {
	Rows []AssociationRow `json:"rows"`
	// Attempts is every checkout attempt ref in the captured inventory, with
	// the durable record its name claims. This is the direction the ref
	// answers, and the reason it exists: a lane is findable here after its
	// checkout has been removed and its branch deleted, because a ref in the
	// common directory outlives both. It is the only direction. Given a tip,
	// which record governs it stays the trailer plus durable corroboration,
	// because a ref can be written by anyone with repository write access and
	// can point anywhere.
	Attempts []AssociationAttempt `json:"attempts,omitempty"`
	// Records is what each captured checkout's own private record claims. A
	// record is a claim like any other: a value the actor can write is a value
	// the actor can unset, and reading one back never promotes anything.
	Records  []AssociationRecord `json:"records,omitempty"`
	Complete bool                `json:"complete"`
	Reason   string              `json:"reason,omitempty"`
}

// AssociationAttempt is one `refs/gitseq/lanes/<governing event hash>/<n>`
// ref, read back.
//
// Selector is the event hash the ref name carries, resolved here the same way
// a trailer's is: through the verified event set, never by pasting the room's
// prefix onto it. Grade is at most `claimed`, and is `corroborated` only when
// the ordinary rule promotes it — a standing artifact statement naming that
// exact tip, whose signing actor the owned edge ties to the same record.
type AssociationAttempt struct {
	Ref       string `json:"ref"`
	Attempt   int    `json:"attempt,omitempty"`
	Tip       string `json:"tip"`
	Selector  string `json:"selector"`
	Governing string `json:"governing,omitempty"`
	Grade     string `json:"grade"`
	Reason    string `json:"reason,omitempty"`
	// Checkouts names the captured checkouts sitting at this tip, and is
	// empty when the lane's checkout is gone — which is the case this whole
	// field exists to make visible.
	Checkouts []string `json:"checkouts,omitempty"`
}

// AssociationRecord is one checkout's private record, read back and graded.
type AssociationRecord struct {
	Checkout  string `json:"checkout"`
	Selector  string `json:"selector"`
	Governing string `json:"governing,omitempty"`
	Grade     string `json:"grade"`
	Reason    string `json:"reason,omitempty"`
}

// associationCacheKey names every input the table is derived from: the durable
// frontier, the observed ref inventory, and the captured checkout inventory.
//
// Each is there because dropping it makes the cache answer about a world that
// has moved. The frontier alone is not enough: a branch can move, be renamed
// or be deleted while nothing is appended to the log. The refs are not enough
// either: a checkout can be added, removed or renamed, and a detached checkout
// can move to another commit, without any ref or any durable record changing,
// and the rows carry checkout labels and rows built for those detached heads.
// A cache keyed on less than its inputs does not go stale after a while; it
// stays wrong until something unrelated happens to move. The captured records
// are an input for the same reason: a checkout stamped, restamped or copied
// into changes what this table says while no ref and no listing entry moves.
type associationCacheKey struct {
	frontier  string
	refs      string
	checkouts string
	records   string
}

// Associations derives the reverse association from Git and the projection
// and writes nothing, anywhere. Every branch tip in the bounded ref inventory
// gets a row, and so does every checkout whose head is on no branch.
//
// The caller's views are annotated in place with the grade and governing
// record of the row that covers them, so a reader of the checkout list sees
// the same answer as a reader of the table.
func (w *Workspace) Associations(ctx context.Context, snapshot Snapshot, views []WorktreeView) AssociationTable {
	read := w.captureAround(ctx, views)
	defer read.Close()
	return w.AssociateCheckouts(read, snapshot)
}

// AssociateCheckouts is that derivation over an already captured read. It
// answers about the ref inventory and checkout listing that read captured, and
// spends that read's budget, so a request that also classifies its checkouts
// gets one world and one bound rather than two of each.
//
// It reads no classification. Whether a checkout may be removed is a different
// question from what its commits claim, and a claim is not evidence about the
// first.
func (w *Workspace) AssociateCheckouts(read *CheckoutRead, snapshot Snapshot) AssociationTable {
	views := read.Worktrees
	// The shared budget is charged before anything is answered, including from
	// the cache. A judgment that finds the read already spent has to say so:
	// the bound belongs to the request, not to whichever judgment reached it
	// first.
	if !read.budget.take(1) {
		return unknownAssociationTable(views, "worktree inspection limit or cancellation")
	}
	if !read.refsKnown {
		return unknownAssociationTable(views, "ref inventory unavailable or above its bound")
	}
	key := associationCacheKey{
		frontier:  snapshot.Head + ":" + strconv.Itoa(snapshot.Depth),
		refs:      associationRefDigest(read.refs),
		checkouts: associationCheckoutDigest(views),
		records:   checkoutRecordDigest(read.records),
	}
	if table, hit := w.cachedAssociations(key); hit {
		applyAssociationRows(views, table)
		return table
	}
	table := w.associate(read, snapshot.Projection)
	// Completion is decided once, here, and an incomplete result leaves every
	// view unknown. Nothing after this point may annotate a view from an
	// incomplete table: an early unknown that the ordinary annotation path
	// overwrites reads as "looked and found nothing" rather than "did not
	// finish looking", which is exactly the negative this layer must not
	// prove. Only a complete table is cached.
	if !table.Complete {
		return unknownAssociationTable(views, table.Reason)
	}
	w.rememberAssociations(key, table)
	applyAssociationRows(views, table)
	return table
}

func (w *Workspace) cachedAssociations(key associationCacheKey) (AssociationTable, bool) {
	w.associationsMu.Lock()
	defer w.associationsMu.Unlock()
	if w.associationsKey != key || w.associationsKey == (associationCacheKey{}) {
		return AssociationTable{}, false
	}
	return copyAssociationTable(w.associationsCached), true
}

func (w *Workspace) rememberAssociations(key associationCacheKey, table AssociationTable) {
	w.associationsMu.Lock()
	defer w.associationsMu.Unlock()
	w.associationsKey, w.associationsCached = key, copyAssociationTable(table)
}

// copyAssociationTable hands every caller its own rows. The cached table
// outlives one request, and a reader that sorted or trimmed a shared slice
// would change what the next reader is told.
func copyAssociationTable(table AssociationTable) AssociationTable {
	rows := make([]AssociationRow, len(table.Rows))
	for i, row := range table.Rows {
		row.Checkouts = append([]string(nil), row.Checkouts...)
		claims := append([]AssociationClaim(nil), row.Claims...)
		// The claims are values, but each carries its own candidate list, and
		// a copy that stopped at the outer slice would hand every caller the
		// same underlying candidates.
		for j := range claims {
			claims[j].Candidates = append([]string(nil), claims[j].Candidates...)
		}
		row.Claims = claims
		rows[i] = row
	}
	table.Rows = rows
	table.Attempts = append([]AssociationAttempt(nil), table.Attempts...)
	for i := range table.Attempts {
		table.Attempts[i].Checkouts = append([]string(nil), table.Attempts[i].Checkouts...)
	}
	table.Records = append([]AssociationRecord(nil), table.Records...)
	return table
}

// associationRefDigest is the observed ref inventory, exactly as this pass saw
// it. Names and object ids both take part: a rename at an unchanged tip and a
// move at an unchanged name are each a different world to associate.
func associationRefDigest(refs map[string]string) string {
	names := make([]string, 0, len(refs))
	for name := range refs {
		names = append(names, name)
	}
	sort.Strings(names)
	sum := sha256.New()
	for _, name := range names {
		sum.Write([]byte(name))
		sum.Write([]byte{0})
		sum.Write([]byte(refs[name]))
		sum.Write([]byte{0})
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// associationCheckoutDigest is the captured checkout inventory exactly as this
// pass used it: for each checkout, in the order given, the label the rows
// carry, the branch that decides which row it joins, the head a row is built
// for when no branch covers it, and whether it is detached.
//
// Only inputs take part. Grade and Governing are what this pass writes back
// onto the views, so including them would key the cache on its own output.
func associationCheckoutDigest(views []WorktreeView) string {
	sum := sha256.New()
	for _, view := range views {
		for _, field := range []string{view.Checkout, view.Branch, view.Head} {
			sum.Write([]byte(field))
			sum.Write([]byte{0})
		}
		detached := byte(0)
		if view.Detached {
			detached = 1
		}
		sum.Write([]byte{detached})
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// incompleteAssociation is what every bounded stop returns. The rows are empty
// on purpose: a partial association reads as an absence of work rather than as
// an unfinished look for it.
func incompleteAssociation(reason string) AssociationTable {
	return AssociationTable{Rows: []AssociationRow{}, Complete: false, Reason: reason}
}

func unknownAssociationTable(views []WorktreeView, reason string) AssociationTable {
	for i := range views {
		views[i].Grade, views[i].Governing = GradeUnknown, ""
	}
	return incompleteAssociation(reason)
}

// applyAssociationRows copies each checkout's own row onto its view. A
// checkout on a branch takes the branch row; a detached one takes the row
// built for its own head.
func applyAssociationRows(views []WorktreeView, table AssociationTable) {
	// An incomplete table has already marked every view unknown, and its rows
	// are empty. Annotating from them would clear that unknown to blank, which
	// is the one thing an unfinished read must never say.
	if !table.Complete {
		return
	}
	byBranch := map[string]int{}
	byHead := map[string]int{}
	for i, row := range table.Rows {
		if row.Branch != "" {
			byBranch[row.Branch] = i
		}
		if _, seen := byHead[row.Head]; !seen && row.Head != "" {
			byHead[row.Head] = i
		}
	}
	for i := range views {
		views[i].Grade, views[i].Governing = "", ""
		index, found := -1, false
		if views[i].Branch != "" && !views[i].Detached {
			index, found = byBranch[views[i].Branch]
		}
		if !found {
			index, found = byHead[views[i].Head]
		}
		if !found {
			continue
		}
		views[i].Grade, views[i].Governing = table.Rows[index].Grade, table.Rows[index].Governing
	}
}

// associationTips is the ordered, deduplicated set of tips one pass reads:
// every branch in the bounded ref inventory, then every checkout head no
// branch covers. Remote-tracking refs are deliberately absent: this table is
// about work in this repository, and a tracking ref is a copy of somewhere
// else.
// The tip limit is a bound on work, not a licence to answer as though the tips
// past it were not there. Excess is detected before anything is dropped, and
// the caller discards the batch, because a table of the first 256 branches
// marked complete is a wrong answer about the other one rather than a partial
// answer about all of them.
func associationTips(refs map[string]string, views []WorktreeView) (rows []AssociationRow, tips []string, exhausted bool) {
	rows = []AssociationRow{}
	index := map[string]int{}
	names := make([]string, 0, len(refs))
	for name := range refs {
		if strings.HasPrefix(name, "refs/heads/") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if len(rows) == gitstore.LineageTipLimit {
			return nil, nil, true
		}
		branch := strings.TrimPrefix(name, "refs/heads/")
		index[branch] = len(rows)
		rows = append(rows, AssociationRow{Branch: branch, Head: refs[name], Grade: GradeUnknown})
	}
	for _, view := range views {
		// The checkout listing is cached for eight seconds, so an attached
		// checkout's own head can be behind its branch. The ref is the current
		// fact and the row is built from it.
		if position, found := index[view.Branch]; found && view.Branch != "" && !view.Detached {
			rows[position].Checkouts = append(rows[position].Checkouts, view.Checkout)
			continue
		}
		if view.Head == "" || !exactObjectID(view.Head) {
			continue
		}
		if position, found := index["\x00"+view.Head]; found {
			rows[position].Checkouts = append(rows[position].Checkouts, view.Checkout)
			continue
		}
		if len(rows) == gitstore.LineageTipLimit {
			return nil, nil, true
		}
		index["\x00"+view.Head] = len(rows)
		rows = append(rows, AssociationRow{Head: view.Head, Checkouts: []string{view.Checkout}, Grade: GradeUnknown})
	}
	tips = make([]string, 0, len(rows))
	for _, row := range rows {
		tips = append(tips, row.Head)
	}
	return rows, tips, false
}

// associationBoundary is where a lineage walk stops: the mainline this
// repository publishes, resolved from the same ref inventory the rows came
// from. Work claims itself against the branch it will land on, so reading past
// that point re-reads the shared history of every branch for no new answer.
func associationBoundary(refs map[string]string) string {
	for _, ref := range MainlineRefs {
		if oid := refs[ref]; oid != "" {
			return oid
		}
	}
	return ""
}

// associate builds the table or says why it could not. Every bound it meets
// ends the pass: the caller decides what an incomplete result does to the
// views, so nothing here annotates one.
func (w *Workspace) associate(read *CheckoutRead, p workroom.Projection) AssociationTable {
	const exhausted = "worktree inspection limit or cancellation"
	ctx, budget, views := read.ctx, read.budget, read.Worktrees
	rows, tips, overflow := associationTips(read.refs, views)
	if overflow {
		return incompleteAssociation("more branches and unbranched checkout heads than the " +
			strconv.Itoa(gitstore.LineageTipLimit) + "-tip limit reads")
	}
	if !budget.take(len(rows)) {
		return incompleteAssociation(exhausted)
	}
	lineage, err := w.Store.Lineage(ctx, tips, associationBoundary(read.refs), gitstore.LineageCommitLimit)
	if err != nil {
		return incompleteAssociation("commit lineage unavailable")
	}
	// A cut-off lineage and a tip this repository does not hold are both
	// answers this pass has not got. Reporting the rows it did reach as
	// complete would say that the commits it never read carry no claim.
	if len(lineage.Unavailable) > 0 {
		return incompleteAssociation("a captured tip is not an object this repository holds")
	}
	if len(lineage.Truncated) > 0 {
		return incompleteAssociation("a tip's first-parent lineage is longer than the " +
			strconv.Itoa(gitstore.LineageCommitLimit) + "-commit per-tip limit reads")
	}
	byHash := make(map[string]gitstore.GraphCommit, len(lineage.Commits))
	for _, commit := range lineage.Commits {
		if !budget.take(1) {
			return incompleteAssociation(exhausted)
		}
		byHash[commit.Hash] = commit
	}
	room := eventref.Room{Genesis: w.config.Genesis, ObjectFormat: w.config.ObjectFormat}
	grader := newAssociationGrader(room, p, byHash)
	for i := range rows {
		if !w.gradeAssociationRow(&rows[i], byHash, grader, budget) {
			return incompleteAssociation(exhausted)
		}
	}
	attempts, ok := gradeCheckoutAttempts(read, views, grader, budget)
	if !ok {
		return incompleteAssociation(exhausted)
	}
	records, ok := gradeCheckoutRecords(read, views, room, grader, budget)
	if !ok {
		return incompleteAssociation(exhausted)
	}
	if !budget.take(0) {
		return incompleteAssociation(exhausted)
	}
	return AssociationTable{Rows: rows, Attempts: attempts, Records: records, Complete: true}
}

// gradeCheckoutAttempts reads every attempt ref in the captured inventory back
// to the durable record its name claims.
//
// The event hash in the ref name is resolved through the same verified event
// set every other selector goes through. It is deliberately not turned into an
// identifier by pasting this room's prefix onto it: a hash that names no event
// here, or more than one, is `unresolved` and says so, and a builder that
// pasted a prefix would have produced a confident identifier for both cases.
//
// A tip no branch reaches is the case this exists for, and it is not a
// problem: the ref names the record, the record is claimed, and nothing is
// promoted. Corroboration still needs a signed artifact naming that exact
// commit, so an attempt ref left behind by a deleted branch grades `claimed`
// unless somebody signed for its tip.
func gradeCheckoutAttempts(read *CheckoutRead, views []WorktreeView, grader *associationGrader, budget *worktreeInspectionBudget) ([]AssociationAttempt, bool) {
	names := make([]string, 0, len(read.refs))
	for name := range read.refs {
		if strings.HasPrefix(name, checkoutAttemptNamespace) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if !budget.take(len(names)) {
		return nil, false
	}
	atTip := map[string][]string{}
	for _, view := range views {
		if view.Head != "" {
			atTip[view.Head] = append(atTip[view.Head], view.Checkout)
		}
	}
	attempts := make([]AssociationAttempt, 0, len(names))
	for _, name := range names {
		tip := read.refs[name]
		hash, number := attemptRefParts(name)
		entry := AssociationAttempt{Ref: name, Attempt: number, Tip: tip, Selector: hash, Checkouts: atTip[tip]}
		if hash == "" {
			entry.Grade, entry.Reason = GradeUnresolved, "the ref name carries no event hash"
			attempts = append(attempts, entry)
			continue
		}
		if !budget.take(1) {
			return nil, false
		}
		claim := grader.grade(tip, hash)
		entry.Grade, entry.Governing = claim.Grade, claim.Governing
		entry.Reason = claim.Reason
		attempts = append(attempts, entry)
	}
	return attempts, true
}

// attemptRefParts splits refs/gitseq/lanes/<hash>/<attempt> into its two
// halves. A name that is not that shape yields no hash, which is reported
// rather than guessed at: this namespace belongs to one writer and a ref in it
// that this version does not recognise is a fact, not a parse to force.
func attemptRefParts(name string) (hash string, attempt int) {
	rest := strings.TrimPrefix(name, checkoutAttemptNamespace)
	hash, number, found := strings.Cut(rest, "/")
	if !found || strings.Contains(number, "/") || !exactObjectID(hash) {
		return "", 0
	}
	value, err := strconv.Atoi(number)
	if err != nil || value < 1 {
		return "", 0
	}
	return hash, value
}

// gradeCheckoutRecords reads each captured checkout's private record back.
//
// A record naming another workroom is carried as typed and never resolved
// here, which is what every foreign citation in this repository gets. The
// genesis written inside the record is compared with the identifier it
// carries as well: a record whose two halves disagree is not a record this
// room may read as its own, whatever the identifier says.
func gradeCheckoutRecords(read *CheckoutRead, views []WorktreeView, room eventref.Room, grader *associationGrader, budget *worktreeInspectionBudget) ([]AssociationRecord, bool) {
	records := make([]AssociationRecord, 0, len(read.records))
	for i, captured := range read.records {
		if i >= len(views) {
			break
		}
		if !captured.present && captured.reason == "" {
			continue
		}
		if !budget.take(1) {
			return nil, false
		}
		entry := AssociationRecord{Checkout: views[i].Checkout}
		if !captured.present {
			entry.Grade, entry.Reason = GradeUnknown, captured.reason
			records = append(records, entry)
			continue
		}
		entry.Selector = captured.record.Governing
		if captured.record.Genesis != room.Genesis || captured.record.ObjectFormat != room.ObjectFormat {
			entry.Grade = GradeForeign
			entry.Reason = "the record names workroom git:" + captured.record.ObjectFormat + ":" + captured.record.Genesis
			records = append(records, entry)
			continue
		}
		claim := grader.grade(views[i].Head, captured.record.Governing)
		entry.Grade, entry.Governing, entry.Reason = claim.Grade, claim.Governing, claim.Reason
		records = append(records, entry)
	}
	return records, true
}

// gradeAssociationRow walks one tip's first-parent lineage, newest first,
// grading every `Rests-On:` trailer it meets. The walk uses the parents the
// verified commit objects carry, so it starts no second Git process per row.
func (w *Workspace) gradeAssociationRow(row *AssociationRow, byHash map[string]gitstore.GraphCommit, grader *associationGrader, budget *worktreeInspectionBudget) bool {
	row.Grade = ""
	tip := true
	for hash, steps := row.Head, 0; hash != "" && steps < gitstore.LineageCommitLimit; steps++ {
		if !budget.take(1) {
			return false
		}
		commit, known := byHash[hash]
		if !known {
			break
		}
		for _, selector := range commit.RestsOn {
			if !budget.take(1) {
				return false
			}
			claim := grader.grade(hash, selector)
			if len(row.Claims) < associationClaimLimit {
				row.Claims = append(row.Claims, claim)
			} else {
				row.Truncated = true
			}
			if row.Governing == "" && claim.Governing != "" {
				row.Governing = claim.Governing
			}
			if row.Grade == "" || row.Grade == GradeUnresolved && claim.Grade != GradeUnresolved {
				row.Grade = claim.Grade
			}
			if claim.Grade == GradeCorroborated {
				if tip {
					row.Grade = GradeCorroborated
				}
				if row.CorroboratedAt == "" {
					row.CorroboratedAt = hash
				}
			}
		}
		if len(commit.Parents) == 0 {
			break
		}
		hash, tip = commit.Parents[0], false
	}
	if row.Grade == "" {
		row.Grade = GradeUnknown
	}
	// A tip that is not itself the commit anyone signed for is claimed, even
	// when a corroborated commit sits behind it on the same lineage.
	if row.Grade == GradeCorroborated && row.CorroboratedAt != row.Head {
		row.Grade = GradeClaimed
	}
	return true
}

// checkoutEvidence is one standing artifact statement naming one exact commit,
// and the governing record the review guard's owned edge ties its signing
// actor to. This is the only thing that promotes a claim.
type checkoutEvidence struct {
	artifact string
	actor    string
	request  string
	promise  string
	decision string
}

// associationGrader resolves and grades selectors against one verified event
// set. It resolves each distinct selector once and looks up corroboration only
// for the commits the lineage reached, so an unread artifact costs nothing.
type associationGrader struct {
	room       eventref.Room
	set        eventref.Set
	projection workroom.Projection
	// artifactsByCommit is every artifact statement's own commit field. The
	// expensive provenance walk runs only for a commit a lineage reached.
	artifactsByCommit map[string][]string
	statements        map[string]workroom.Statement
	effective         map[string]bool
	graded            map[string]AssociationClaim
	evidence          map[string][]checkoutEvidence
}

func newAssociationGrader(room eventref.Room, p workroom.Projection, byHash map[string]gitstore.GraphCommit) *associationGrader {
	g := &associationGrader{
		room:              room,
		set:               eventref.FromProjection(room, p),
		projection:        p,
		artifactsByCommit: map[string][]string{},
		statements:        make(map[string]workroom.Statement, len(p.Statements)),
		effective:         make(map[string]bool, len(p.Decisions)),
		graded:            map[string]AssociationClaim{},
		evidence:          map[string][]checkoutEvidence{},
	}
	for _, statement := range p.Statements {
		g.statements[statement.Event] = statement
	}
	for _, decision := range p.Decisions {
		g.effective[decision.Event] = decision.Verdict == workroom.Effective
	}
	for _, artifact := range p.Artifacts {
		// Only a commit some lineage actually reached can corroborate
		// anything, and the owned-edge walk behind that lookup is the
		// expensive part. Indexing the rest would buy nothing.
		if artifact.Commit != "" && byHash[artifact.Commit].Hash != "" {
			g.artifactsByCommit[artifact.Commit] = append(g.artifactsByCommit[artifact.Commit], artifact.Event)
		}
	}
	return g
}

// grade turns one commit and one trailer value into a graded claim.
func (g *associationGrader) grade(commit, selector string) AssociationClaim {
	claim, cached := g.graded[commit+"\x00"+selector]
	if cached {
		return claim
	}
	claim = AssociationClaim{Commit: commit, Selector: selector}
	switch g.room.Form(selector) {
	case eventref.FormExternal:
		// Another genesis, or this genesis under another object format. It
		// names an event this room cannot verify, so it is carried as typed
		// and never resolved against anything local.
		claim.Grade, claim.Reason = GradeForeign, "names an event of another workroom"
	default:
		resolved, err := g.set.Resolve(selector)
		switch {
		case err != nil:
			claim.Grade = GradeUnresolved
			var refusal *eventref.Refusal
			if refusal, _ = err.(*eventref.Refusal); refusal != nil {
				claim.Reason = refusal.Detail
				for _, candidate := range refusal.Candidates {
					claim.Candidates = append(claim.Candidates, candidate.ID)
				}
				claim.CandidatesOmitted = refusal.Omitted
			} else {
				claim.Reason = err.Error()
			}
		case !g.set.Resolvable(resolved):
			claim.Grade = GradeUnresolved
			claim.Reason = "names no durable record of this workroom"
		default:
			claim.Grade, claim.Governing = GradeClaimed, resolved
			g.corroborate(&claim)
		}
	}
	g.graded[commit+"\x00"+selector] = claim
	return claim
}

// corroborate promotes a claim only on authenticated durable evidence: a
// standing artifact statement naming this exact commit, whose signing actor
// the owned edge ties to this very governing record. A commit author ident is
// committer-controlled and is never read here.
func (g *associationGrader) corroborate(claim *AssociationClaim) {
	for _, evidence := range g.commitEvidence(claim.Commit) {
		if claim.Governing != evidence.request && claim.Governing != evidence.promise && claim.Governing != evidence.decision {
			continue
		}
		claim.Grade = GradeCorroborated
		claim.Artifact, claim.Actor = evidence.artifact, evidence.actor
		claim.Request, claim.Promise, claim.Decision = evidence.request, evidence.promise, evidence.decision
		return
	}
}

func (g *associationGrader) commitEvidence(commit string) []checkoutEvidence {
	if found, cached := g.evidence[commit]; cached {
		return found
	}
	found := []checkoutEvidence{}
	for _, event := range g.artifactsByCommit[commit] {
		statement, standing := g.statements[event]
		if !standing || statement.Retired || statement.Kind != workroom.KindArtifact || !g.effective[event] {
			continue
		}
		request, promise, owned := reviewguard.OwnedEdge(g.projection, statement)
		if owned {
			found = append(found, checkoutEvidence{artifact: event, actor: statement.Actor, request: request, promise: promise})
			continue
		}
		// Self-initiated work files no request, so the record its commits name
		// is the adopted decision the artifact rests on directly.
		for _, basis := range g.projection.Provenance[event] {
			if reviewguard.AdoptedDecision(g.projection, basis) == nil {
				found = append(found, checkoutEvidence{artifact: event, actor: statement.Actor, decision: basis})
			}
		}
	}
	g.evidence[commit] = found
	return found
}
