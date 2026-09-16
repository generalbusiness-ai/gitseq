package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/statusview"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// gs work --next answers the question the work page leaves to the reader:
// given this row, what is the next act, exactly as it would be typed?
//
// It is a formatter, not a second rule set. Every row comes from the same
// bounded query gs work already runs; this walks those rows and prints the one
// command each one owes, with a `#` comment naming the row it came from. A row
// that owes nothing says why, because "nothing printed" and "nothing owed"
// must not look the same.
//
// Where a fact is not in the projection — the path a head changed, the
// checkout a reviewer will read — the line carries an angle-bracketed
// placeholder. A command line with a hole in it is still the shape of the act,
// and it is honest about which part only the actor knows.
type nextWorld struct {
	projection  workroom.Projection
	fingerprint string
	actor       string
	statements  map[string]workroom.Statement
}

func workNext(ctx context.Context, workspace *app.Workspace, page statusview.WorkPage, fingerprint string) (string, error) {
	snapshot, err := snapshotWithProgress(ctx, workspace)
	if err != nil {
		return "", err
	}
	world := nextWorld{projection: snapshot.Projection, fingerprint: fingerprint,
		actor: page.Actor.Name, statements: map[string]workroom.Statement{}}
	for _, statement := range snapshot.Projection.Statements {
		world.statements[statement.Event] = statement
	}
	var out strings.Builder
	fmt.Fprintf(&out, "# Next acts for %s: %d rows on this page, %d matching, %d remaining.\n",
		page.Actor.Name, page.Returned, page.MatchingTotal, page.Remaining)
	if page.Returned == 0 {
		out.WriteString("# Nothing is owed on this page.\n")
	}
	for _, item := range page.Items {
		out.WriteString(world.row(item))
	}
	for _, line := range world.assertsOnPromises() {
		out.WriteString(line)
	}
	if page.NextCursor != "" {
		fmt.Fprintf(&out, "# More rows: gs work --next --cursor %s\n", page.NextCursor)
	}
	return out.String(), nil
}

// row prints one comment naming the row and one command line for the act it
// owes, or one comment saying why it owes none.
func (w nextWorld) row(item statusview.WorkItem) string {
	event := item.Request
	if event == "" {
		event = item.Event
	}
	var out strings.Builder
	fmt.Fprintf(&out, "\n# %s %s %s\n", item.Status, item.Lane, event)
	if item.Text != "" {
		fmt.Fprintf(&out, "#   %s\n", oneLine(item.Text, 110))
	}
	if item.Stale {
		successor := item.SuccessorRequest
		if successor == "" {
			successor = "its successor"
		}
		fmt.Fprintf(&out, "#   stale: re-anchor on %s or ask %s to refile\n", successor, item.Requester.Name)
	}
	for _, line := range w.acts(item, event) {
		out.WriteString(line + "\n")
	}
	return out.String()
}

func (w nextWorld) acts(item statusview.WorkItem, event string) []string {
	switch {
	case item.Lane == statusview.LaneAwaitingRatification:
		return []string{w.command("gs ratify %s", event)}
	case item.Lane == statusview.LaneAvailable:
		return []string{w.command("gs promise %s", event),
			"#   or decline: " + w.command("gs state --kind assert --rests-on %s --text '<why you decline>'", event) +
				", and ask " + item.Requester.Name + " to retire it"}
	case item.Status == "promised":
		return w.promised(item, event)
	case item.Status == "awaiting-review":
		return w.awaitingReview(item, event)
	case item.Status == "reported" && item.Requester.Fingerprint == w.fingerprint && item.Report != "":
		// The performer reported and the requester ratifies. This is the one
		// row whose next act belongs to the actor who asked for the work.
		return []string{w.command("gs ratify %s", item.Report)}
	case item.Status == "awaiting-landing":
		return []string{w.landCommand(item)}
	case item.Status == "awaiting-authorization":
		return []string{fmt.Sprintf("#   nothing for you: the landing is held, waiting on %s to file its release", waitingName(item))}
	case item.Lane == statusview.LaneYouAreWaitingOn:
		return []string{fmt.Sprintf("#   nothing for you: waiting on %s", waitingName(item))}
	default:
		return []string{fmt.Sprintf("#   nothing for you: this row is %s", item.Status)}
	}
}

// promised is the row you claimed and have not reported. A review lane reports
// with a verdict; an implementation lane reports with its artifacts.
func (w nextWorld) promised(item statusview.WorkItem, event string) []string {
	if artifact := w.statements[event].Body["artifact"]; artifact != "" {
		head := w.statements[event].Body["head"]
		return []string{w.command("gs review --checkout %s --artifact %s --promise %s --verdict approved --text-file <your review>",
			placeholder("checkout at "+short(head)), artifact, orPlaceholder(item.Promise, "promise"))}
	}
	return []string{w.command("gs artifact --head %s --promise %s %s",
		orPlaceholder(item.ReportedHead, "head"), orPlaceholder(item.Promise, "promise"), placeholder("path…"))}
}

// awaitingReview is the row whose artifacts stand at a head. What it owes
// depends on whether a verdict has been filed, and on whether that verdict has
// been ratified by the actor who asked for it.
func (w nextWorld) awaitingReview(item statusview.WorkItem, event string) []string {
	head := orPlaceholder(item.ReportedHead, "head")
	review := item.LatestReview
	switch {
	case review == nil && w.liveReviewRequest(item):
		return []string{"#   nothing for you: a review request is live and no verdict has been filed yet"}
	case review == nil:
		return []string{w.command("gs review-request --head %s --to %s", head, placeholder("reviewer"))}
	case review.Verdict != "approved":
		return []string{
			fmt.Sprintf("#   %s: correct the head, then publish a fresh artifact and ask for review again", review.Verdict),
			w.command("gs artifact --head %s --promise %s %s", placeholder("corrected head"), orPlaceholder(item.Promise, "promise"), placeholder("path…")),
		}
	case !review.Ratified:
		lines := []string{}
		if w.requestedReview(review.Report) {
			lines = append(lines, w.command("gs ratify %s", review.Report))
		} else {
			lines = append(lines, fmt.Sprintf("#   the approval is unratified; only its review requester may ratify it: gs ratify %s", short(review.Report)))
		}
		return append(lines, w.landLine(review.Report, item.TargetRef))
	default:
		return []string{w.landLine(review.Report, item.TargetRef)}
	}
}

func (w nextWorld) landCommand(item statusview.WorkItem) string {
	approval := item.Approval
	if approval == "" && item.LatestReview != nil {
		approval = item.LatestReview.Report
	}
	return w.landLine(approval, item.TargetRef)
}

func (w nextWorld) landLine(approval, targetRef string) string {
	where := "target checkout"
	if targetRef != "" {
		where = "checkout on " + targetRef
	}
	return w.command("gs land --approval %s --checkout %s --text %s",
		orPlaceholder(approval, "approval"), placeholder(where), placeholder("what landed and why it matters"))
}

// liveReviewRequest reports whether this actor already has an unclosed review
// request resting on the head's reporting artifact, which is the difference
// between "ask for a review" and "wait for the one you asked for".
func (w nextWorld) liveReviewRequest(item statusview.WorkItem) bool {
	if item.Report == "" {
		return false
	}
	artifact := item.Report
	open := map[string]bool{"open": true, "promised": true, "reported": true}
	for _, commitment := range w.projection.Commitments {
		if commitment.Requester != w.fingerprint || !open[commitment.Status] {
			continue
		}
		for _, basis := range w.projection.Provenance[commitment.Request] {
			if basis == artifact {
				return true
			}
		}
	}
	return false
}

// requestedReview reports whether this actor asked for the review whose
// verdict this is: the fold admits a ratification of a report only from the
// requester of the commitment it answers.
func (w nextWorld) requestedReview(report string) bool {
	for _, commitment := range w.projection.Commitments {
		if commitment.Report == report {
			return commitment.Requester == w.fingerprint
		}
	}
	return false
}

// assertsOnPromises surfaces the breakdowns filed against this actor's
// promises. They are not commitments and never appear as work rows, so
// without this they are found only by somebody who thought to look.
func (w nextWorld) assertsOnPromises() []string {
	mine := map[string]bool{}
	for _, statement := range w.projection.Statements {
		if statement.Kind == workroom.KindPromise && statement.Actor == w.fingerprint && !statement.Retired {
			mine[statement.Event] = true
		}
	}
	type note struct {
		sequence int
		line     string
	}
	var notes []note
	for _, statement := range w.projection.Statements {
		if statement.Kind != workroom.KindAssert || statement.Retired {
			continue
		}
		for _, basis := range w.projection.Provenance[statement.Event] {
			if !mine[basis] {
				continue
			}
			notes = append(notes, note{sequence: statement.Sequence,
				line: fmt.Sprintf("\n# assert on your promise %s by %s\n#   %s\n%s\n",
					short(basis), statusview.ActorName(w.projection, statement.Actor), oneLine(statement.Text, 110),
					w.command("gs inspect %s", statement.Event))})
			break
		}
	}
	sort.Slice(notes, func(i, j int) bool { return notes[i].sequence > notes[j].sequence })
	if len(notes) > 10 {
		notes = notes[:10]
	}
	lines := make([]string, 0, len(notes))
	for _, note := range notes {
		lines = append(lines, note.line)
	}
	return lines
}

// command prints one line an actor can copy. The identity is named because a
// line that signs as whoever the environment happens to hold is not exact.
func (w nextWorld) command(format string, arguments ...any) string {
	command := fmt.Sprintf(format, arguments...)
	parts := strings.SplitN(command, " ", 3)
	if len(parts) < 2 {
		return command
	}
	line := parts[0] + " " + parts[1] + " --as " + w.actor
	if len(parts) == 3 {
		line += " " + parts[2]
	}
	return line
}

func placeholder(what string) string { return "<" + what + ">" }

func orPlaceholder(value, what string) string {
	if value == "" {
		return placeholder(what)
	}
	return value
}

// waitingName reads who the row is waiting on, falling back to the addressee:
// an open request nobody has claimed waits on the actor it was addressed to,
// and the fold leaves WaitingOn empty until a promise names a performer.
func waitingName(item statusview.WorkItem) string {
	switch {
	case item.WaitingOn != nil:
		return item.WaitingOn.Name
	case item.AddressedTo != nil:
		return item.AddressedTo.Name
	case item.Performer != nil:
		return item.Performer.Name
	default:
		return "somebody else"
	}
}
