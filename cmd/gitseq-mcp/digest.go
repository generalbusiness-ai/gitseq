package main

// MCP, the resident summary endpoint, and gs share the statusview package.
// This file only adapts service wire types to the actor-oriented view used by
// the MCP surface; the caps, actor naming, totals, and verdict distinctions
// live at the shared boundary.

import (
	"encoding/json"
	"errors"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/service"
	"github.com/generalbusiness-ai/gitseq/internal/statusview"
)

const (
	deltaCap = statusview.DeltaCap
	textCap  = statusview.TextCap
	listCap  = statusview.ListCap
)

type actorView = statusview.ActorView
type commitmentView = statusview.CommitmentView
type eventView = statusview.EventView
type totals = statusview.ActorTotals
type liveView = statusview.LiveView
type actorStatus = statusview.ActorStatus
type waitDelta = statusview.WaitDelta

func capList[T any](items []T, limit int) ([]T, int) { return statusview.Cap(items, limit) }
func capRoles(roles []string, limit int) ([]string, int) {
	return statusview.CapRoles(roles, limit)
}
func truncate(text string) string { return statusview.Text(text) }

func digestStatus(status service.Status, fingerprint, actorName string, degraded bool) actorStatus {
	return statusview.BuildActorStatus(status.Durable, status.Live, status.Cursor, status.Inbox, fingerprint, actorName, degraded)
}

func digestWait(response service.WaitResponse, requested service.Cursor, fingerprint, actorName string, degraded bool) waitDelta {
	return statusview.BuildWait(response.Status.Durable, response.Status.Cursor, response.LiveChanges, response.Reset, requested, response.Status.Inbox, fingerprint, actorName, degraded)
}

// summarize writes the one text block a client is guaranteed to read. A result
// carrying a warning says so there as well as in the structured payload, so an
// agent that reads only the summary still learns that the kind it wrote means
// nothing here.
func summarize(tool string, value any) string {
	if result, ok := value.(map[string]any); ok {
		if warning, held := result["warning"].(string); held && warning != "" {
			return statusview.Summarize(tool, value) + "; warning: " + warning
		}
	}
	return statusview.Summarize(tool, value)
}

func shown(listed, skipped int) string { return statusview.Shown(listed, skipped) }
func liveLabel(live liveView) string   { return statusview.LiveLabel(live) }

// fingerprint resolves this process's configured actor to the identity the
// projection speaks in, within the workroom the call is acting in. The same
// actor name can carry a different fingerprint in a different repository, so
// the room asked has to be the room answered about. An unconfigured actor
// yields the empty string with no error, which simply matches nothing rather
// than matching everyone — that is the ErrUnknownActor meaning here and only
// that. Every other failure, chiefly the custody I/O error a fresh re-read
// can hit, is returned as itself, so a digest can never dress a broken
// custody read up as an unknown actor or the reverse.
func (s *mcpServer) fingerprint(current *room, identities ...*selectedIdentity) (string, error) {
	actor, err := s.resolvedActor(current, identities...)
	if err != nil {
		if errors.Is(err, app.ErrUnknownActor) {
			return "", nil
		}
		return "", err
	}
	return actor.Fingerprint, nil
}

func (s *mcpServer) digest(current *room, status service.Status, degraded bool, identities ...*selectedIdentity) (actorStatus, error) {
	fingerprint, err := s.fingerprint(current, identities...)
	if err != nil {
		return actorStatus{}, err
	}
	return digestStatus(status, fingerprint, current.actor, degraded), nil
}

func remarshal(value any, target any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, target)
}

func requestedCursor(arguments map[string]any) service.Cursor {
	var cursor service.Cursor
	raw, ok := arguments["cursor"]
	if !ok {
		return cursor
	}
	if err := remarshal(raw, &cursor); err != nil {
		return service.Cursor{}
	}
	return cursor
}
