// Package github observes GitHub issues into a workroom.
//
// The connector is not part of the core. It is a separate process holding its
// own key, submitting through the same public surface every other actor uses,
// and this package is the library it is built from rather than a framework the
// core knows about. Nothing here reaches into the kernel or the fold.
//
// Two operations, deliberately asymmetric. Observing is inbound and only ever
// appends: an observation is a new event, never a merge. Rendering is outbound
// and only ever overwrites a surface the connector owns and nothing reads back.
// Because inbound only appends and outbound only overwrites, the two never need
// to reconcile, and there is no conflict for an engine to resolve dishonestly.
package github

import (
	"fmt"
	"strings"
)

// Namespace is the connector's idempotency namespace. The dedup key the kernel
// computes is target, actor fingerprint, namespace and key together
// (internal/intent.Signed.DedupKey), so a namespace of our own keeps the
// connector's keys from colliding with any other actor's.
const Namespace = "connector/github@0"

// Marker opens every comment body the connector writes. It is how the
// connector recognizes its own writing on a surface it shares with people.
//
// This is ownership by structure, not suppression by tagging: the connector
// edits one comment in place and ingests only what it does not own. A tag that
// merely asks readers to ignore something is fragile, because anyone can write
// the tag; a surface the connector alone writes to cannot be spoofed into a
// loop by an ordinary participant.
const Marker = "<!-- gitseq-connector -->"

// Issue is the part of a GitHub issue an observation depends on. It is
// deliberately small: the connector attests to what it saw, and anything it did
// not see has no business in the body.
type Issue struct {
	Owner  string
	Repo   string
	Number int
	Title  string
	Body   string
	Author string // the GitHub login, carried as data, never as an identity
	URL    string
}

// Comment is an issue comment, with the author and body needed to decide
// whether the connector wrote it.
type Comment struct {
	Owner  string
	Repo   string
	Issue  int
	ID     int64
	Body   string
	Author string
}

// ExternalID names a foreign object in one stable string. It is the join
// between the two systems, and it is what makes the connector stateless: the
// correspondence between GitHub objects and durable events is a fold over acts
// carrying this field, not a private database that could disagree with the log.
func ExternalID(owner, repo string, number int) string {
	return fmt.Sprintf("%s/%s#%d", owner, repo, number)
}

// CommentExternalID names one comment. GitHub comment identifiers are unique
// within a repository, so the issue number is not needed for uniqueness — it is
// included because a reader of the log should be able to see what the comment
// was attached to without resolving anything.
func CommentExternalID(owner, repo string, issue int, id int64) string {
	return fmt.Sprintf("%s/%s#%d/comment/%d", owner, repo, issue, id)
}

// Observation is what the connector proposes to append for one foreign object.
// It carries no opinion about which kind it should become: that is the
// charter's decision, and it depends on who filed the issue.
type Observation struct {
	ExternalID     string
	IdempotencyKey string
	Text           string
	Body           map[string]string
}

// ObserveIssue builds the observation for one issue.
//
// The idempotency key is the external identifier and nothing else. That is what
// makes at-least-once delivery safe: a webhook redelivered three times, or a
// poll that overlaps a previous poll, produces the same key, and the kernel
// collapses the repeats into a replay rather than a second act. Deriving the key
// from anything that changes when the issue is edited — a timestamp, a body
// hash — would silently reopen the duplicate it is meant to close.
func ObserveIssue(issue Issue) Observation {
	external := ExternalID(issue.Owner, issue.Repo, issue.Number)
	return Observation{
		ExternalID:     external,
		IdempotencyKey: external,
		Text: fmt.Sprintf("Observed on GitHub: %s filed %s — %s",
			issue.Author, external, issue.Title),
		Body: map[string]string{
			"source":       "github",
			"external_id":  external,
			"on_behalf_of": issue.Author,
			"url":          issue.URL,
			"title":        issue.Title,
		},
	}
}

// ObserveComment builds the observation for one issue comment.
func ObserveComment(comment Comment) Observation {
	external := CommentExternalID(comment.Owner, comment.Repo, comment.Issue, comment.ID)
	return Observation{
		ExternalID:     external,
		IdempotencyKey: external,
		Text: fmt.Sprintf("Observed on GitHub: %s commented on %s",
			comment.Author, ExternalID(comment.Owner, comment.Repo, comment.Issue)),
		Body: map[string]string{
			"source":       "github",
			"external_id":  external,
			"on_behalf_of": comment.Author,
		},
	}
}

// Owned reports whether the connector wrote this comment. The connector must
// never ingest its own writing: a connector that reads back what it renders
// will observe its own observation and loop forever.
func Owned(comment Comment) bool {
	return strings.HasPrefix(strings.TrimSpace(comment.Body), Marker)
}

// Correspondence is the connector's memory, folded from durable acts rather
// than kept beside them. It maps each observed external identifier to the event
// that observed it.
type Correspondence map[string]string

// Fold builds the correspondence from durable statement bodies. The caller
// passes what the projection already holds; this package does no I/O.
//
// A connector that needs private state to be correct has a design problem. This
// fold is why one is not needed here: restart the connector with an empty disk
// and it reconstructs exactly what it knew, because what it knew was always in
// the log.
func Fold(statements []Statement) Correspondence {
	seen := make(Correspondence)
	for _, statement := range statements {
		if statement.Body["source"] != "github" {
			continue
		}
		external := statement.Body["external_id"]
		if external == "" {
			continue
		}
		// First writer wins. A later act naming the same external object is a
		// correction or a duplicate someone appended by hand; either way the
		// original observation is the one the correspondence points at.
		if _, exists := seen[external]; !exists {
			seen[external] = statement.Event
		}
	}
	return seen
}

// Statement is the shape Fold needs from a projected durable statement.
type Statement struct {
	Event string
	Body  map[string]string
}

// Unobserved returns the observations not already present in the
// correspondence, in the order given.
//
// This is belt to the kernel's braces. Idempotency already makes a repeated
// submission a replay, so skipping here is an optimization rather than the
// guarantee — but it also means a routine poll of a busy repository does not
// submit hundreds of acts that the kernel will only throw away.
func Unobserved(observations []Observation, seen Correspondence) []Observation {
	fresh := make([]Observation, 0, len(observations))
	for _, observation := range observations {
		if _, exists := seen[observation.ExternalID]; exists {
			continue
		}
		fresh = append(fresh, observation)
	}
	return fresh
}
