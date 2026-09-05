package statusview

import (
	"fmt"
	"sort"
	"strings"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// LandingText renders a warning only from the receipt witness, never from
// missing legacy fields.
func LandingText(row app.LandingDetails) string {
	if row.TargetRef == "" {
		return ""
	}
	var out strings.Builder
	fmt.Fprintf(&out, "target %s", Text(row.TargetRef))
	if row.Legacy {
		out.WriteString(" (legacy request)")
	}
	if row.LandingReceipt != "" {
		fmt.Fprintf(&out, "; receipt %s", Text(row.LandingReceipt))
		if row.ReceiptLegacy {
			out.WriteString(" (legacy target)")
		}
	}
	if row.MergeHoldWarning {
		out.WriteString("; WARNING: sealed landing used the unreleased-hold compatibility window")
	}
	if row.Git != nil {
		fmt.Fprintf(&out, "; local %s", Text(row.Git.State))
		publication := "unknown"
		if row.Git.RemoteContains != nil {
			if *row.Git.RemoteContains {
				publication = "contains landing"
			} else {
				publication = "does not contain landing"
			}
		}
		fmt.Fprintf(&out, "; remote tracking %s: %s", Text(row.Git.Remote), publication)
	}
	return out.String()
}

// LandingDetails carries the fold's delivery facts unchanged across the
// bounded status and work surfaces. Git measurements never change these facts.
type LandingDetails = app.LandingDetails

func landingWarning(rows []*app.LandingDetails) string {
	receipts := map[string]bool{}
	for _, row := range rows {
		if row.MergeHoldWarning && row.LandingReceipt != "" {
			receipts[row.LandingReceipt] = true
		}
	}
	if len(receipts) == 0 {
		return ""
	}
	return fmt.Sprintf("; warning: %d shown sealed receipts used the unreleased-hold compatibility window", len(receipts))
}

func landingDetails(c workroom.Commitment) LandingDetails {
	return app.LandingDetailsFor(c)
}

func approvedLandingFor(c workroom.Commitment, actor string) bool {
	return c.ApprovedNotLanded && (c.Performer == actor || c.HoldOwner == actor)
}

// landingTargets selects the newest validated receipt per target, then caps
// the rendered observations. The receipt itself remains the provenance.
func landingTargets(projection workroom.Projection) ([]app.LandingDetails, int) {
	wanted := make(map[string]workroom.Commitment)
	for _, c := range projection.Commitments {
		if c.LandingReceipt != "" {
			wanted[c.LandingReceipt] = c
		}
	}
	seen := make(map[string]bool)
	var rows []app.LandingDetails
	omitted := 0
	for i := len(projection.Statements) - 1; i >= 0; i-- {
		statement := projection.Statements[i]
		c, ok := wanted[statement.Event]
		if !ok {
			continue
		}
		key := c.TargetRepo + "\x00" + c.TargetRef
		if seen[key] {
			continue
		}
		seen[key] = true
		if len(rows) == ListCap {
			omitted++
			continue
		}
		rows = append(rows, app.LandingDetailsFor(c))
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].TargetRepo != rows[j].TargetRepo {
			return rows[i].TargetRepo < rows[j].TargetRepo
		}
		return rows[i].TargetRef < rows[j].TargetRef
	})
	var pointers []*app.LandingDetails
	for i := range rows {
		pointers = append(pointers, &rows[i])
	}
	app.FillLandingEvidence(projection, pointers)
	return rows, omitted
}

func (p *WorkPage) LandingRows() []*app.LandingDetails {
	var rows []*app.LandingDetails
	for i := range p.Items {
		rows = append(rows, &p.Items[i].LandingDetails)
	}
	return rows
}

func (p *ItemInspection) LandingRows() []*app.LandingDetails {
	if p.Landing == nil {
		return nil
	}
	return []*app.LandingDetails{p.Landing}
}

func (p *ActorStatus) LandingRows() []*app.LandingDetails {
	var rows []*app.LandingDetails
	for i := range p.LandingTargets {
		rows = append(rows, &p.LandingTargets[i])
	}
	for _, group := range [][]CommitmentView{p.AvailableToYou, p.WaitingOnYou, p.YouAreWaiting, p.NotActionable} {
		for i := range group {
			rows = append(rows, &group[i].LandingDetails)
		}
	}
	return rows
}

func (p *Summary) LandingRows() []*app.LandingDetails {
	var rows []*app.LandingDetails
	for i := range p.LandingTargets {
		rows = append(rows, &p.LandingTargets[i])
	}
	for _, group := range [][]Commitment{p.Actionable, p.Attention} {
		for i := range group {
			rows = append(rows, &group[i].LandingDetails)
		}
	}
	return rows
}
