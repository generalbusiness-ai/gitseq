package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/residentclient"
	"github.com/generalbusiness-ai/gitseq/internal/statusview"
)

// The shared machinery behind the bounded query commands in main.go: the
// resident round trip they all take, the three-way Git answer about a branch,
// and the human renderers. Nothing here re-implements a filter, a cursor, or a
// cap. A second implementation of a bounded selection is a second set of bounds
// to get wrong, and the two would disagree at exactly the moment somebody was
// comparing them.

const (
	// queryResponseLimit and queryTimeout bound one resident answer. The row
	// caps in statusview already make these responses independent of workroom
	// depth; this is the ceiling for a resident that answers with something
	// else entirely.
	queryResponseLimit = fullResponseLimit
	queryTimeout       = 10 * time.Second
)

// askResident posts one bounded query and reports whether its answer may be
// used. The resident's frontier must still be the frontier this checkout holds
// and must not have moved while the answer was read, so a resident that is
// behind is refused rather than quietly preferred. Any failure is named on
// standard error and reported as not taken, which is what keeps a local
// fallback from ever being presented as a resident answer.
func askResident(ctx context.Context, workspace *app.Workspace, serverURL, route string, input any, target any, frontier func() statusview.Frontier) bool {
	before, err := workspace.Store.Head(ctx, kernel.Ref(workspace.Config.Genesis))
	if err == nil {
		err = residentclient.New(queryTimeout).PostJSON(ctx, serverURL, route, input, queryResponseLimit, target)
	}
	if err == nil {
		answered := frontier()
		err = validateRemoteFrontierAt(ctx, workspace, before, answered.Genesis, answered.Head)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "gs: resident %s unavailable (%v); performing verified local fallback\n", route, err)
		return false
	}
	return true
}

// openForQuery opens the workspace and validates a resident URL before any
// read, so a malformed --server is refused rather than silently falling back
// to a local answer the caller did not ask for.
func openForQuery(ctx context.Context, repo, serverURL string) (*app.Workspace, error) {
	if serverURL != "" {
		if err := validateLoopbackServer(serverURL); err != nil {
			return nil, err
		}
	}
	return app.Open(ctx, repo)
}

// headLanding separates the three answers Git can give about one commit, which
// is not the same as the two answers a shell `||` sees. A non-zero status from
// `git merge-base --is-ancestor` means either "not an ancestor" or "the check
// never ran", and reading the second as the first asserts a fact nobody
// measured. The runbook counts a commit this clone does not have as out of the
// branch, and so does the refusal here — but it is named apart, because the
// repair is to fetch it, not to merge it.
type headLanding struct {
	in      []string
	out     []string
	unknown []string
}

func (landing headLanding) refusal(gate statusview.ReviewGate, branch string) error {
	var reasons []string
	if !gate.Quiet() {
		reasons = append(reasons, fmt.Sprintf("%d review requests await a first verdict", gate.Awaiting))
	}
	if len(landing.out) > 0 {
		reasons = append(reasons, fmt.Sprintf("%d approved heads are not in %s", len(landing.out), branch))
	}
	if len(landing.unknown) > 0 {
		reasons = append(reasons, fmt.Sprintf("%d approved heads are unknown to this clone", len(landing.unknown)))
	}
	if len(reasons) == 0 {
		return nil
	}
	return errors.New("the review queue is not quiet: " + strings.Join(reasons, "; "))
}

func classifyHeads(ctx context.Context, checkout, branch string, heads []string) (headLanding, error) {
	var landing headLanding
	if len(heads) == 0 {
		return landing, nil
	}
	// The branch itself has to resolve before any answer about it means
	// anything. Asking about ancestry of a name Git cannot resolve would
	// report every head as out of the branch, which is a confident wrong
	// answer rather than a refusal.
	if _, err := git(ctx, checkout, "rev-parse", "--verify", "--end-of-options", branch+"^{commit}"); err != nil {
		return headLanding{}, err
	}
	// Two Git processes, whatever the number of heads. The obvious loop runs
	// `merge-base --is-ancestor` once per head, so the work a caller does grows
	// with the signed log — an actor filing approvals can make this command
	// arbitrarily expensive without anything in the repository changing. The
	// answer is bounded by asking the two questions in bulk instead of capping
	// the list: a gate that omits a head while reporting quiet is the one
	// failure an irreversible step cannot have, so the list stays complete and
	// the work stops being proportional to it.
	//
	// Reachability first. Every commit reachable from the branch is an ancestor
	// of it, which is the same question `--is-ancestor` answers, and one
	// rev-list answers it for all heads at once. Its cost is the repository's
	// history, not the workroom's length, which is the amplification being
	// removed.
	reachable, err := git(ctx, checkout, "rev-list", "--end-of-options", branch)
	if err != nil {
		return headLanding{}, err
	}
	ancestors := make(map[string]bool)
	for _, line := range strings.Fields(reachable) {
		ancestors[line] = true
	}
	// Existence second, and separately, because the two answers are not the
	// same news. A head this clone does not have is not out of the branch; it
	// is unmeasured, and the repair is to fetch it rather than to merge it.
	// cat-file --batch-check answers for every head in one process.
	var probe bytes.Buffer
	for _, head := range heads {
		probe.WriteString(head)
		probe.WriteString("\n")
	}
	command := exec.CommandContext(ctx, "git", "-C", checkout, "cat-file", "--batch-check=%(objectname) %(objecttype)")
	command.Stdin = &probe
	presence, err := command.Output()
	if err != nil {
		return headLanding{}, err
	}
	present := make(map[string]bool)
	for index, line := range strings.Split(strings.TrimSpace(string(presence)), "\n") {
		if index >= len(heads) {
			break
		}
		if fields := strings.Fields(line); len(fields) == 2 && fields[1] == "commit" {
			present[heads[index]] = true
		}
	}
	for _, head := range heads {
		switch {
		case ancestors[head]:
			landing.in = append(landing.in, head)
		case present[head]:
			landing.out = append(landing.out, head)
		default:
			landing.unknown = append(landing.unknown, head)
		}
	}
	return landing, nil
}

// querySource names where an answer came from. There are three states, not
// two: a resident answer, an ordinary verified local read, and a local read
// taken after a resident was asked and could not answer. Collapsing the first
// and the last would present a fallback as a resident answer, which is the one
// thing the fallback path exists to avoid.
func querySource(asked, answered bool) string {
	switch {
	case answered:
		return "resident"
	case asked:
		return "verified local fallback"
	default:
		return "verified local"
	}
}

func renderFrontier(frontier statusview.Frontier, source string) string {
	return fmt.Sprintf("head %s depth %d (%s)\n", short(frontier.Head), frontier.Depth, source)
}

func renderWorkPage(page statusview.WorkPage, source string) string {
	var out strings.Builder
	out.WriteString(renderFrontier(page.Frontier, source))
	fmt.Fprintf(&out, "Work for %s: %d matching, %d returned, %d before, %d remaining", page.Actor.Name, page.MatchingTotal, page.Returned, page.Before, page.Remaining)
	if page.ClosedStaleOmitted > 0 {
		fmt.Fprintf(&out, ", %d closed and stale omitted", page.ClosedStaleOmitted)
	}
	out.WriteString(".\n")
	for _, item := range page.Items {
		fmt.Fprintf(&out, "%-10s %-18s %s\n", item.Status, item.Lane, short(item.Request))
		if item.Text != "" {
			fmt.Fprintf(&out, "    %s\n", item.Text)
		}
		if item.WaitingOn != nil {
			fmt.Fprintf(&out, "    waiting on %s\n", item.WaitingOn.Name)
		}
		if item.LatestReview != nil {
			fmt.Fprintf(&out, "    latest review %s (%s)\n", item.LatestReview.Verdict, reviewQualifiers(*item.LatestReview))
		}
	}
	if page.NextCursor != "" {
		fmt.Fprintf(&out, "next cursor: %s\n", page.NextCursor)
	}
	return out.String()
}

func reviewQualifiers(review statusview.WorkReview) string {
	qualifiers := make([]string, 0, 3)
	if review.Ratified {
		qualifiers = append(qualifiers, "ratified")
	}
	if review.Retired {
		qualifiers = append(qualifiers, "retired")
	}
	if review.Stale {
		qualifiers = append(qualifiers, "stale")
	}
	if len(qualifiers) == 0 {
		return "unratified"
	}
	return strings.Join(qualifiers, ", ")
}

func renderArtifactPage(page statusview.ArtifactPage, source string) string {
	var out strings.Builder
	out.WriteString(renderFrontier(page.Frontier, source))
	fmt.Fprintf(&out, "Artifacts: %d matching, %d returned, %d before, %d remaining.\n", page.MatchingTotal, page.Returned, page.Before, page.Remaining)
	for _, row := range page.Artifacts {
		fmt.Fprintf(&out, "%-10s %-40s %s %s\n", artifactRowState(row), row.Path, short(row.Event), short(row.Commit))
	}
	if page.NextCursor != "" {
		fmt.Fprintf(&out, "next cursor: %s\n", page.NextCursor)
	}
	return out.String()
}

// artifactRowState names the row the way the status page names it, so a reader
// moving between the two does not have to translate. The lifecycle is read
// from the row rather than reconstructed here: a succeeded artifact and a
// retired one are opposite news to whoever was standing on it, and reporting
// both as "retired" was the defect that made --state succeeded indistinguishable
// from --state retired. Lifecycle is reported before staleness because a
// withdrawn pointer is the louder fact.
func artifactRowState(row statusview.ArtifactRow) string {
	switch lifecycle := row.Lifecycle(); lifecycle {
	case statusview.ArtifactStateRetired, statusview.ArtifactStateSucceeded:
		return string(lifecycle)
	default:
		if row.Stale {
			return "stale"
		}
		return "current"
	}
}

func renderInspection(inspection statusview.ItemInspection, source string) string {
	var out strings.Builder
	out.WriteString(renderFrontier(inspection.Frontier, source))
	fmt.Fprintf(&out, "Event %s\n", inspection.Event)
	if inspection.Statement != nil {
		fmt.Fprintf(&out, "  statement #%d %s by %s: %s\n", inspection.Statement.Sequence, inspection.Statement.Kind, short(inspection.Statement.Actor), statusview.Text(inspection.Statement.Text))
	}
	if inspection.Act != nil {
		fmt.Fprintf(&out, "  act %s\n", inspection.Act.Type)
	}
	if inspection.Decision != nil {
		fmt.Fprintf(&out, "  decision %s: %s\n", inspection.Decision.Verdict, inspection.Decision.Reason)
	}
	if inspection.Commitment != nil {
		fmt.Fprintf(&out, "  commitment %s on request %s\n", inspection.Commitment.Status, short(inspection.Commitment.Request))
	}
	fmt.Fprintf(&out, "  rests on %d bases", len(inspection.ProvenanceBases))
	if inspection.ProvenanceBasesOmitted > 0 {
		fmt.Fprintf(&out, " (%d older omitted)", inspection.ProvenanceBasesOmitted)
	}
	out.WriteString("\n")
	for _, basis := range inspection.ProvenanceBases {
		fmt.Fprintf(&out, "    %s\n", short(basis))
	}
	for _, artifact := range inspection.RelatedArtifacts {
		fmt.Fprintf(&out, "  artifact %s at %s\n", short(artifact.Event), artifact.Path)
	}
	for _, review := range inspection.RelatedReviews {
		fmt.Fprintf(&out, "  review %s of head %s\n", review.Verdict, short(review.Head))
	}
	return out.String()
}

func renderReviewGate(gate statusview.ReviewGate, branch, source string, landing headLanding) string {
	var out strings.Builder
	out.WriteString(renderFrontier(gate.Frontier, source))
	fmt.Fprintf(&out, "Review requests: %d named, %d awaiting a first verdict, %d with an artifact reference that resolves to no live artifact.\n",
		gate.Named, gate.Awaiting, gate.Unresolved)
	renderEvents(&out, "awaiting a first verdict", gate.AwaitingRequests, gate.AwaitingOmitted)
	renderEvents(&out, "unresolved artifact reference", gate.UnresolvedReferences, gate.UnresolvedOmitted)
	fmt.Fprintf(&out, "Approved heads: %d in %s, %d not in %s, %d unknown to this clone.\n",
		len(landing.in), branch, len(landing.out), branch, len(landing.unknown))
	for _, head := range landing.out {
		fmt.Fprintf(&out, "  not in %s: %s\n", branch, head)
	}
	for _, head := range landing.unknown {
		fmt.Fprintf(&out, "  unknown here, fetch before trusting the silence: %s\n", head)
	}
	return out.String()
}

func renderEvents(out *strings.Builder, label string, events []string, omitted int) {
	for _, event := range events {
		fmt.Fprintf(out, "  %s: %s\n", label, event)
	}
	if omitted > 0 {
		fmt.Fprintf(out, "  %d older %s omitted; raise --limit to see them.\n", omitted, label)
	}
}

// short elides the middle of a long identifier so the output stays readable.
// It is visibly incomplete, which is right for a value a reader can still
// resolve and wrong for one that has to round-trip; --json carries the whole
// identifier for anything that does.
func short(value string) string {
	if len(value) <= 16 {
		return value
	}
	return value[:8] + "…" + value[len(value)-7:]
}
