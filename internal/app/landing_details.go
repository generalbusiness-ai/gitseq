package app

import (
	"context"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// LandingDetails carries one common shape on status, work, inspect and local
// worktree rows. Durable facts and local Git observations remain separate.
type LandingDetails struct {
	TargetRepo        string              `json:"target_repo,omitempty"`
	TargetRef         string              `json:"target_ref,omitempty"`
	Legacy            bool                `json:"legacy,omitempty"`
	HoldOwner         string              `json:"hold_owner,omitempty"`
	Release           string              `json:"release,omitempty"`
	Approval          string              `json:"approval,omitempty"`
	Candidate         string              `json:"candidate,omitempty"`
	LatestResolution  string              `json:"latest_resolution,omitempty"`
	Terminal          string              `json:"terminal,omitempty"`
	ApprovedNotLanded bool                `json:"approved_not_landed,omitempty"`
	LandingReceipt    string              `json:"landing_receipt,omitempty"`
	MergeHead         string              `json:"merge_head,omitempty"`
	ReceiptLegacy     bool                `json:"receipt_legacy,omitempty"`
	MergeHoldWarning  bool                `json:"merge_hold_warning,omitempty"`
	Git               *LandingMeasurement `json:"git,omitempty"`
}

func LandingDetailsFor(c workroom.Commitment) LandingDetails {
	return LandingDetails{TargetRepo: c.TargetRepo, TargetRef: c.TargetRef, Legacy: c.Legacy,
		HoldOwner: c.HoldOwner, Release: c.Release, Approval: c.Approval, Candidate: c.Candidate,
		LatestResolution: c.LatestResolution, Terminal: c.Terminal, ApprovedNotLanded: c.ApprovedNotLanded,
		LandingReceipt: c.LandingReceipt}
}

// FillLandingEvidence joins only receipt IDs already selected by the fold's
// validated merge index. Looking for an assertion containing merge_head would
// silently manufacture a second, weaker receipt validator.
func FillLandingEvidence(projection workroom.Projection, rows []*LandingDetails) {
	wanted := map[string][]*LandingDetails{}
	for _, row := range rows {
		if row.LandingReceipt != "" {
			wanted[row.LandingReceipt] = append(wanted[row.LandingReceipt], row)
		}
	}
	if len(wanted) == 0 {
		return
	}
	for _, statement := range projection.Statements {
		for _, row := range wanted[statement.Event] {
			fillLandingReceipt(row, statement)
		}
	}
}

func fillLandingReceipt(row *LandingDetails, statement workroom.Statement) {
	if statement.Retired {
		return
	}
	row.MergeHead = statement.Body["merge_head"]
	row.MergeHoldWarning = statement.Body["merge_hold_warning"] == "true"
	row.ReceiptLegacy = statement.Body["merge_target_repo"] == "" && statement.Body["merge_target_ref"] == ""
}

func (w *Workspace) MeasureLandingDetails(ctx context.Context, rows []*LandingDetails) {
	var inputs []LandingInput
	var selected []*LandingDetails
	for _, row := range rows {
		if row.TargetRef == "" {
			continue
		}
		inputs = append(inputs, LandingInput{TargetRepo: row.TargetRepo, TargetRef: row.TargetRef, MergeHead: row.MergeHead})
		selected = append(selected, row)
	}
	measurements := w.MeasureLandings(ctx, inputs)
	for i, row := range selected {
		row.Git = &measurements[i]
		if row.Terminal == "landed" && row.MergeHead == "" {
			row.Git.State, row.Git.Reason = "unknown", "validated receipt details unavailable"
		}
	}
}
