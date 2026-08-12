package github

import (
	"fmt"
	"strings"
)

// Marker opens every body the connector writes to a surface it shares with
// people. It is how the connector recognizes its own writing.
//
// This is ownership by structure rather than suppression by tagging. A tag that
// merely asks readers to ignore something is fragile, because anyone can write
// the tag; what makes this sound is that the connector ingests only what it
// does not own, and never treats its own rendering as an observation. Without
// that rule the inbound half would observe the outbound half's writing and the
// two would feed each other for ever.
//
// The marker is not a security boundary and should not be read as one. Any
// GitHub user can type these characters.
//
// It is deliberately not consulted by the inbound half, and the reason is worth
// keeping: filtering observations on a string anyone can write would let any
// author hide an issue from the log by pasting the marker into it. That turns a
// label into a denial vector, which is worse than not filtering at all. What
// actually stops the connector reading its own writing is that the inbound half
// skips pull requests, and a pull request is one whatever its body contains.
//
// So this exists to let a human reading GitHub see which text a machine wrote,
// and to be available if the connector ever writes somewhere pull-request
// exclusion does not reach — a comment on an issue, say. It would need the
// spoofing question answered again before being trusted there.
const Marker = "<!-- gitseq-connector -->"

// Owned reports whether the connector wrote this body.
func Owned(body string) bool {
	return strings.HasPrefix(strings.TrimSpace(body), Marker)
}

// PullRequest is what the connector asks GitHub to open.
type PullRequest struct {
	Owner string
	Repo  string
	Head  string // the branch carrying the work
	Base  string // the branch it should merge into
	Title string
	Body  string
}

// Delivery is what came back, so a report can carry it as evidence.
type Delivery struct {
	Number int
	URL    string
}

// Proposal is the durable state a pull request renders.
type Proposal struct {
	Issue    Issue  // the observed issue this fixes
	Request  string // the gitseq request governing the work
	Artifact string // the artifact statement naming the exact head
	Commit   string // the exact head under review
	Branch   string // the branch carrying that head
	Base     string
	Title    string
}

// Render composes the pull request for one proposal.
//
// The body carries the durable record's identifiers rather than a summary of
// it, because a reader on GitHub needs to be able to find the record, and a
// summary would be a second copy that can disagree with the first. What GitHub
// gets is a pointer and a plain statement of where authority lives.
//
// It opens with the marker, so the inbound half can tell this from something a
// person wrote.
func Render(proposal Proposal) PullRequest {
	external := ExternalID(proposal.Issue.Owner, proposal.Issue.Repo, proposal.Issue.Number)

	var body strings.Builder
	body.WriteString(Marker + "\n\n")
	fmt.Fprintf(&body, "Fixes %s.\n\n", external)
	body.WriteString("This pull request is a rendering of a gitseq work loop. ")
	body.WriteString("Approval and merge happen there, not here: the review is signed against the exact head below, ")
	body.WriteString("and `gs merge` refuses anything that is not that commit.\n\n")
	fmt.Fprintf(&body, "- Request: `%s`\n", proposal.Request)
	fmt.Fprintf(&body, "- Artifact: `%s`\n", proposal.Artifact)
	fmt.Fprintf(&body, "- Head under review: `%s`\n", proposal.Commit)

	return PullRequest{
		Owner: proposal.Issue.Owner,
		Repo:  proposal.Issue.Repo,
		Head:  proposal.Branch,
		Base:  proposal.Base,
		Title: proposal.Title,
		Body:  body.String(),
	}
}
