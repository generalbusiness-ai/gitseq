// Package github observes GitHub issues into a workroom.
//
// The connector is not part of the core. It is a separate process holding its
// own key, submitting through the same public surface every other actor uses,
// and this package is the library it is built from rather than a framework the
// core knows about. Nothing here reaches into the kernel or the fold.
//
// Two operations, deliberately asymmetric. Observing is inbound and only ever
// appends: an observation is a new event, never a merge. Rendering is outbound
// and writes to the forge — currently by opening a pull request, which is not
// idempotent and cannot be overwritten, so it is invoked deliberately rather
// than reconciled toward.
//
// The asymmetry that matters is not overwrite-versus-append but that neither
// direction reads the other's writing. Inbound skips pull requests entirely, so
// what this connector opens can never come back as something it observed. That
// exclusion is structural: a pull request is a pull request whatever its body
// says. Because the two never read each other, there is no divergence for an
// engine to reconcile, and no conflict it could resolve dishonestly.
package github

import "fmt"

// Namespace prefixes every idempotency key this connector issues.
//
// It is a prefix rather than a kernel idempotency namespace, because the
// namespace in the dedup key (internal/intent.Signed.DedupKey) is set per
// workroom and the connector submits through the same public surface as every
// other actor rather than choosing its own. Prefixing the key gets the property
// that matters — the connector's observation of owner/repo#1 cannot collide
// with some later connector operation that happens to name the same object —
// without widening the core API to let a caller pick its own namespace.
const Namespace = "connector/github@0"

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
	State  string // "open" or "closed", as GitHub reports it

	// Labels are carried on the issue rather than passed alongside it. A
	// criteria clause cannot decide whether an issue matches without them, and
	// a separate parameter is something a caller can forget to supply — which
	// is exactly how a label criterion silently stopped admitting anything.
	Labels []string
}

// ExternalID names a foreign object in one stable string. It is the join
// between the two systems, and it is what makes the connector stateless: the
// correspondence between GitHub objects and durable events is a fold over acts
// carrying this field, not a private database that could disagree with the log.
func ExternalID(owner, repo string, number int) string {
	return fmt.Sprintf("%s/%s#%d", owner, repo, number)
}

// Observation is what the connector proposes to append for one foreign object.
// It carries no opinion about which kind it should become: that is the
// charter's decision, and it depends on who filed the issue.
type Observation struct {
	ExternalID     string
	IdempotencyKey string
	Text           string
	Body           map[string]string

	// AdmittedBy is the clause event that let this observation in. The act
	// rests on it, so retiring the clause flares what it admitted.
	AdmittedBy string
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
		IdempotencyKey: Namespace + ":" + external,
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

// Comments are deliberately absent. The connector fetches no comments and
// observes none, so the read-back defence that belongs with them — recognizing
// the connector's own writing and refusing to ingest it — would be a claim with
// no executable path behind it. It belongs to the outbound half, which is
// chartered separately, and it should arrive there with the fetch that makes it
// reachable rather than sit here looking like protection nothing exercises.

// Correspondence is the connector's memory, folded from durable acts rather
// than kept beside them. It maps each observed external identifier to the event
// that observed it.
type Correspondence map[string]string

// Fold builds the correspondence from statements authored by connectorActor.
// The caller passes what the projection already holds; this package does no
// I/O, and body fields alone never confer connector authority.
//
// A connector that needs private state to be correct has a design problem. This
// fold is why one is not needed here: restart the connector with an empty disk
// and it reconstructs exactly what it knew, because what it knew was always in
// the log.
func Fold(statements []Statement, connectorActor string) Correspondence {
	seen := make(Correspondence)
	for _, statement := range statements {
		if statement.Actor != connectorActor {
			continue
		}
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
	Actor string
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
