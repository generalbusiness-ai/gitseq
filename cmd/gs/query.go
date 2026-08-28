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
	before, err := workspace.Store.Head(ctx, kernel.Ref(workspace.View().Genesis))
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

// headLanding separates the three answers Git can give about one commit, which
// is not the same as the two answers a shell `||` sees. A non-zero status from
// `git merge-base --is-ancestor` means either "not an ancestor" or "the check
// never ran", and reading the second as the first asserts a fact nobody
// measured. The runbook counts a commit this clone does not have as out of the
// branch, and so does the refusal here — but it is named apart, because the
// repair is to fetch it, not to merge it.
type headLanding struct {
	in, out, unknown                []string
	inTotal, outTotal, unknownTotal int
	outOmitted, unknownOmitted      int
}

func (landing headLanding) refusal(gate statusview.ReviewGate, branch string) error {
	var reasons []string
	if gate.Awaiting > 0 {
		reasons = append(reasons, fmt.Sprintf("%d review requests await a first verdict", gate.Awaiting))
	}
	if landing.outTotal > 0 {
		reasons = append(reasons, fmt.Sprintf("%d approved heads are not in %s", landing.outTotal, branch))
	}
	if landing.unknownTotal > 0 {
		reasons = append(reasons, fmt.Sprintf("%d approved heads are unknown to this clone", landing.unknownTotal))
	}
	if len(reasons) == 0 {
		return nil
	}
	return errors.New("the review queue is not quiet: " + strings.Join(reasons, "; "))
}

func classifyHeads(ctx context.Context, checkout, branch string, heads []string, displayLimit int) (headLanding, error) {
	var landing headLanding
	if len(heads) == 0 {
		return landing, nil
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
	// rev-list also proves the branch resolves. A missing branch fails here
	// before any head is classified, so the separate rev-parse process would
	// add no fact.
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
			landing.inTotal++
			if len(landing.in) < displayLimit {
				landing.in = append(landing.in, head)
			}
		case present[head]:
			landing.outTotal++
			if len(landing.out) < displayLimit {
				landing.out = append(landing.out, head)
			}
		default:
			landing.unknownTotal++
			if len(landing.unknown) < displayLimit {
				landing.unknown = append(landing.unknown, head)
			}
		}
	}
	landing.outOmitted = landing.outTotal - len(landing.out)
	landing.unknownOmitted = landing.unknownTotal - len(landing.unknown)
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
		fmt.Fprintf(&out, "%-10s %-18s %s\n", item.Status, item.Lane, item.Request)
		if item.Text != "" {
			fmt.Fprintf(&out, "    %s\n", item.Text)
		}
		if item.WaitingOn != nil {
			fmt.Fprintf(&out, "    waiting on %s\n", item.WaitingOn.Name)
		}
		if item.SuccessorRequest != "" {
			fmt.Fprintf(&out, "    successor request %s\n", item.SuccessorRequest)
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

func buildSupersessionPlan(page statusview.ArtifactPage, message, prefix string) ([]batchAct, error) {
	if page.Remaining != 0 || page.Returned != page.MatchingTotal {
		return nil, fmt.Errorf("%d live artifacts match but the bounded plan holds %d; no partial plan was written", page.MatchingTotal, page.Returned)
	}
	plan := make([]batchAct, 0, len(page.Artifacts))
	for _, artifact := range page.Artifacts {
		plan = append(plan, batchAct{Verb: app.VerbSupersede, Target: artifact.Event, Text: message, IdempotencyKey: prefix + artifact.Event})
	}
	return plan, nil
}

func renderSupersessionPlan(frontier statusview.Frontier, path string, plan []batchAct) string {
	var out strings.Builder
	out.WriteString(renderFrontier(frontier, "verified local"))
	fmt.Fprintf(&out, "Supersession plan: %d live artifacts at exact path %s.\n", len(plan), path)
	for _, act := range plan {
		fmt.Fprintf(&out, "  supersede %s\n", act.Target)
	}
	return out.String()
}

func renderStalenessWave(wave statusview.StalenessWave, source string) string {
	var out strings.Builder
	out.WriteString(renderFrontier(wave.Frontier, source))
	fmt.Fprintf(&out, "Staleness wave from %s: %d of %d records reached; %d of %d live artifacts away from that path reach it.\n",
		wave.Path, wave.Reached, wave.Records, wave.Reaching, wave.LiveArtifacts)
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
	if lifecycle := row.Lifecycle(); lifecycle != statusview.ArtifactStateLive {
		return string(lifecycle)
	}
	if row.Stale {
		return "stale"
	}
	return "current"
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
		reason := explainLifecycleRefusal(errors.New(inspection.Decision.Reason)).Error()
		fmt.Fprintf(&out, "  decision %s: %s\n", inspection.Decision.Verdict, statusview.Text(reason))
	}
	if inspection.Commitment != nil {
		fmt.Fprintf(&out, "  commitment %s on request %s\n", inspection.Commitment.Status, inspection.Commitment.Request)
	}
	fmt.Fprintf(&out, "  rests on %d bases", len(inspection.ProvenanceBases))
	if inspection.ProvenanceBasesOmitted > 0 {
		fmt.Fprintf(&out, " (%d older omitted)", inspection.ProvenanceBasesOmitted)
	}
	out.WriteString("\n")
	for _, basis := range inspection.ProvenanceBases {
		fmt.Fprintf(&out, "    %s\n", basis)
	}
	for _, artifact := range inspection.RelatedArtifacts {
		fmt.Fprintf(&out, "  artifact %s at %s\n", artifact.Event, artifact.Path)
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
		landing.inTotal, branch, landing.outTotal, branch, landing.unknownTotal)
	for _, head := range landing.out {
		fmt.Fprintf(&out, "  not in %s: %s\n", branch, head)
	}
	for _, head := range landing.unknown {
		fmt.Fprintf(&out, "  unknown here, fetch before trusting the silence: %s\n", head)
	}
	if landing.outOmitted > 0 {
		fmt.Fprintf(&out, "  %d additional out-of-branch heads omitted from this bounded display.\n", landing.outOmitted)
	}
	if landing.unknownOmitted > 0 {
		fmt.Fprintf(&out, "  %d additional unknown heads omitted from this bounded display.\n", landing.unknownOmitted)
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
