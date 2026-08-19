package statusview

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/generalbusiness-ai/gitseq/internal/app"
)

const (
	ArtifactPathMax     = 20
	ArtifactPageDefault = 20
	ArtifactPageMax     = 50
)

// ArtifactQuery selects live artifacts by exact path. Paths are wire keys:
// they are not cleaned, expanded, prefix-matched, or interpreted as globs.
type ArtifactQuery struct {
	Paths  []string `json:"paths"`
	Limit  int      `json:"limit,omitempty"`
	Cursor string   `json:"cursor,omitempty"`
}

type ArtifactRow struct {
	Event                    string `json:"event"`
	Path                     string `json:"path"`
	Commit                   string `json:"commit"`
	Stale                    bool   `json:"stale"`
	Retired                  bool   `json:"retired"`
	DescribesSupersededWorld bool   `json:"describes_superseded_world"`
}

type ArtifactPage struct {
	Frontier      Frontier      `json:"frontier"`
	Paths         []string      `json:"paths"`
	Artifacts     []ArtifactRow `json:"artifacts"`
	MatchingTotal int           `json:"matching_total"`
	Returned      int           `json:"returned"`
	Before        int           `json:"before"`
	Remaining     int           `json:"remaining"`
	NextCursor    string        `json:"next_cursor,omitempty"`
	Degraded      bool          `json:"degraded,omitempty"`
}

type artifactCursor struct {
	Version int    `json:"v"`
	Head    string `json:"head"`
	Offset  int    `json:"offset"`
	Filter  string `json:"filter"`
}

func normalizeArtifactQuery(input ArtifactQuery) (ArtifactQuery, string, error) {
	if len(input.Paths) == 0 {
		return ArtifactQuery{}, "", errors.New("at least one exact path is required")
	}
	if len(input.Paths) > ArtifactPathMax {
		return ArtifactQuery{}, "", fmt.Errorf("at most %d exact paths may be queried", ArtifactPathMax)
	}
	if input.Limit == 0 {
		input.Limit = ArtifactPageDefault
	}
	if input.Limit < 1 || input.Limit > ArtifactPageMax {
		return ArtifactQuery{}, "", fmt.Errorf("limit must be between 1 and %d", ArtifactPageMax)
	}
	seen := make(map[string]bool, len(input.Paths))
	paths := make([]string, 0, len(input.Paths))
	for _, path := range input.Paths {
		if path == "" {
			return ArtifactQuery{}, "", errors.New("artifact path must not be empty")
		}
		if !seen[path] {
			seen[path] = true
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	input.Paths = paths
	encoded, _ := json.Marshal(paths)
	sum := sha256.Sum256(encoded)
	return input, hex.EncodeToString(sum[:]), nil
}

func decodeArtifactCursor(raw, head, filter string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	encoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return 0, errors.New("invalid artifact cursor")
	}
	var cursor artifactCursor
	if err := json.Unmarshal(encoded, &cursor); err != nil || cursor.Version != 1 || cursor.Offset < 0 {
		return 0, errors.New("invalid artifact cursor")
	}
	if cursor.Head != head {
		return 0, fmt.Errorf("artifact cursor is for head %s; current head is %s", cursor.Head, head)
	}
	if cursor.Filter != filter {
		return 0, errors.New("artifact cursor does not match these paths")
	}
	return cursor.Offset, nil
}

func encodeArtifactCursor(head, filter string, offset int) string {
	encoded, _ := json.Marshal(artifactCursor{Version: 1, Head: head, Offset: offset, Filter: filter})
	return base64.RawURLEncoding.EncodeToString(encoded)
}

// BuildArtifactPage returns only non-retired artifacts. Pagination, exact-path
// matching, and the head-bound cursor keep the answer bounded without hiding
// that another page exists.
func BuildArtifactPage(durable app.Snapshot, input ArtifactQuery, degraded bool) (ArtifactPage, error) {
	query, filter, err := normalizeArtifactQuery(input)
	if err != nil {
		return ArtifactPage{}, err
	}
	offset, err := decodeArtifactCursor(query.Cursor, durable.Head, filter)
	if err != nil {
		return ArtifactPage{}, err
	}
	wanted := make(map[string]bool, len(query.Paths))
	for _, path := range query.Paths {
		wanted[path] = true
	}
	rows := make([]ArtifactRow, 0, query.Limit)
	matching := 0
	for index := len(durable.Projection.Artifacts) - 1; index >= 0; index-- {
		artifact := durable.Projection.Artifacts[index]
		if artifact.Retired || !wanted[artifact.Path] {
			continue
		}
		position := matching
		matching++
		if position < offset || len(rows) == query.Limit {
			continue
		}
		rows = append(rows, ArtifactRow{Event: artifact.Event, Path: artifact.Path, Commit: artifact.Commit,
			Stale: artifact.Stale, Retired: artifact.Retired, DescribesSupersededWorld: artifact.DescribesSupersededWorld})
	}
	if offset > matching {
		return ArtifactPage{}, errors.New("artifact cursor is beyond the matching result")
	}
	end := offset + len(rows)
	page := ArtifactPage{Frontier: Frontier{Genesis: durable.Genesis, Head: durable.Head, Depth: durable.Depth},
		Paths: append([]string(nil), query.Paths...), Artifacts: rows, MatchingTotal: matching, Returned: len(rows),
		Before: offset, Remaining: matching - end, Degraded: degraded}
	if end < matching {
		page.NextCursor = encodeArtifactCursor(durable.Head, filter, end)
	}
	return page, nil
}
