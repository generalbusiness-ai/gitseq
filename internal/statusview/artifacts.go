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
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

const (
	ArtifactPathMax     = 20
	ArtifactPageDefault = 20
	ArtifactPageMax     = 50
)

// ArtifactState names which part of an artifact's lifecycle a query selects.
// It is a closed set of four names, not a predicate language, and the names
// are the ones `gs status` already prints on an artifact row, so a reader of
// that page and the author of a query use one vocabulary.
type ArtifactState string

const (
	// ArtifactStateLive is what a query naming no state receives, and it is
	// exactly what every caller received before the field existed. Live means
	// not retired. Stale is a different fact and does not answer this
	// question: a stale artifact still occupies its path and is still the
	// predecessor a successor has to retire.
	ArtifactStateLive ArtifactState = "live"
	// ArtifactStateRetired is the withdrawn pointer with no successor named.
	// It is kept apart from succeeded because the two mean opposite things to
	// whoever was standing on the artifact: one says where the behaviour went,
	// the other says go and look.
	ArtifactStateRetired   ArtifactState = "retired"
	ArtifactStateSucceeded ArtifactState = "succeeded"
	ArtifactStateAll       ArtifactState = "all"
)

// ArtifactQuery is the resident HTTP and MCP request contract. It selects live
// artifacts at exact path strings. Keep CLI-only selectors out of this type:
// both remote surfaces decode it directly, so sharing a wider request type
// silently widens their protocol even when the published schemas stay put.
type ArtifactQuery struct {
	Paths  []string `json:"paths"`
	Limit  int      `json:"limit,omitempty"`
	Cursor string   `json:"cursor,omitempty"`
}

// ArtifactSelection is the CLI-only extension over ArtifactQuery. The CLI may
// ask the extra runbook questions without turning an implementation detail
// into a resident or MCP compatibility promise.
type ArtifactSelection struct {
	Paths []string
	State ArtifactState
	// Reaches selects artifacts whose chain of artifact bases arrives at an
	// artifact recorded at this exact path, following provenance from artifact
	// to artifact for as many hops as the chain has. It is the anchor a
	// document follows to say which behaviour it describes, and it is the one
	// selector that reads more than a single row's own fields.
	//
	// An artifact recorded at the anchor path itself is excluded. It is the
	// anchor, not something anchored to it, and returning the anchors when
	// asked what still points at them answers a question nobody asked.
	Reaches string
	Limit   int
	Cursor  string
}

// lifecycleOf names which of the three lifecycle states an artifact is in.
// Retirement and succession are two independent bits in the fold; the states
// are what those bits mean together, and deriving that meaning in more than
// one place is exactly how the filter here and the output in cmd/gs came to
// disagree — a succeeded artifact was selected correctly and then reported as
// merely retired, so four documented states arrived as three.
func lifecycleOf(artifact workroom.Artifact) ArtifactState {
	switch {
	case artifact.Retired && artifact.Succeeded:
		return ArtifactStateSucceeded
	case artifact.Retired:
		return ArtifactStateRetired
	default:
		return ArtifactStateLive
	}
}

// admits reports whether one artifact is in the selected state. It reads the
// same lifecycle every row carries, so a selector and the row it returns can
// no longer disagree about what the artifact is.
func (state ArtifactState) admits(artifact workroom.Artifact) bool {
	if state == ArtifactStateAll {
		return true
	}
	if state == "" {
		return lifecycleOf(artifact) == ArtifactStateLive
	}
	return lifecycleOf(artifact) == state
}

type ArtifactRow struct {
	Event   string `json:"event"`
	Path    string `json:"path"`
	Commit  string `json:"commit"`
	Stale   bool   `json:"stale"`
	Retired bool   `json:"retired"`
	// Succeeded is omitted on the remote surface's live-only rows, preserving
	// its response bytes. CLI selections that include retired artifacts need
	// the bit: Retired alone cannot distinguish replacement from withdrawal.
	Succeeded                bool `json:"succeeded,omitempty"`
	DescribesSupersededWorld bool `json:"describes_superseded_world"`
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
	paths, limit, err := normalizeArtifactPaths(input.Paths, input.Limit)
	if err != nil {
		return ArtifactQuery{}, "", err
	}
	input.Paths, input.Limit = paths, limit
	// This is the exact pre-CLI-extension cursor fingerprint. Existing HTTP
	// and MCP cursors remain valid and cannot be spliced across path changes.
	encoded, _ := json.Marshal(paths)
	sum := sha256.Sum256(encoded)
	return input, hex.EncodeToString(sum[:]), nil
}

func normalizeArtifactSelection(input ArtifactSelection) (ArtifactSelection, string, error) {
	// A path-less query is admitted only when an anchor selects the population
	// instead. Without that, an empty path list is the request for everything,
	// and refusing it is what has always kept an unbounded dump off this
	// surface; the refusal is left exactly where it was for every caller that
	// names no anchor.
	if len(input.Paths) == 0 && input.Reaches == "" {
		return ArtifactSelection{}, "", errors.New("at least one exact path is required")
	}
	paths, limit, err := normalizeArtifactPaths(input.Paths, input.Limit)
	if err != nil {
		return ArtifactSelection{}, "", err
	}
	input.Paths, input.Limit = paths, limit
	switch input.State {
	case "":
		input.State = ArtifactStateLive
	case ArtifactStateLive, ArtifactStateRetired, ArtifactStateSucceeded, ArtifactStateAll:
	default:
		return ArtifactSelection{}, "", fmt.Errorf("unknown artifact state %q", input.State)
	}
	fingerprintInput := struct {
		Paths   []string      `json:"paths"`
		State   ArtifactState `json:"state"`
		Reaches string        `json:"reaches"`
	}{input.Paths, input.State, input.Reaches}
	encoded, _ := json.Marshal(fingerprintInput)
	sum := sha256.Sum256(encoded)
	return input, hex.EncodeToString(sum[:]), nil
}

func normalizeArtifactPaths(input []string, limit int) ([]string, int, error) {
	if len(input) > ArtifactPathMax {
		return nil, 0, fmt.Errorf("at most %d exact paths may be queried", ArtifactPathMax)
	}
	if limit == 0 {
		limit = ArtifactPageDefault
	}
	if limit < 1 || limit > ArtifactPageMax {
		return nil, 0, fmt.Errorf("limit must be between 1 and %d", ArtifactPageMax)
	}
	seen := make(map[string]bool, len(input))
	paths := make([]string, 0, len(input))
	for _, path := range input {
		if path == "" {
			return nil, 0, errors.New("artifact path must not be empty")
		}
		if !seen[path] {
			seen[path] = true
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	return paths, limit, nil
}

// artifactsAnchoredAt returns every artifact that reaches an artifact at the
// exact path along artifact-to-artifact provenance. It walks forward from the
// artifacts at that path to everything standing on them, transitively, rather
// than stopping after one hop: a page resting on an artifact that itself rests
// on the anchor is anchored there just as surely as a direct dependent, and a
// one-hop answer would report the migration finished while that page still
// described the old world.
//
// Retired artifacts stay on the walk. Retirement withdraws a pointer; it does
// not erase the anchor that whatever was stated on top of the artifact still
// follows. Only the caller's state filter decides which rows are returned.
func artifactsAnchoredAt(projection workroom.Projection, path string) map[string]bool {
	isArtifact := make(map[string]bool, len(projection.Artifacts))
	for _, artifact := range projection.Artifacts {
		isArtifact[artifact.Event] = true
	}
	dependents := make(map[string][]string, len(projection.Artifacts))
	for _, artifact := range projection.Artifacts {
		for _, basis := range projection.Provenance[artifact.Event] {
			if isArtifact[basis] {
				dependents[basis] = append(dependents[basis], artifact.Event)
			}
		}
	}
	anchored := make(map[string]bool)
	frontier := make([]string, 0, len(projection.Artifacts))
	for _, artifact := range projection.Artifacts {
		if artifact.Path == path && !anchored[artifact.Event] {
			anchored[artifact.Event] = true
			frontier = append(frontier, artifact.Event)
		}
	}
	// The seen set is what makes this terminate. Provenance is a graph, not a
	// tree, and an artifact reached by two chains must not be walked twice.
	for len(frontier) > 0 {
		current := frontier[len(frontier)-1]
		frontier = frontier[:len(frontier)-1]
		for _, dependent := range dependents[current] {
			if !anchored[dependent] {
				anchored[dependent] = true
				frontier = append(frontier, dependent)
			}
		}
	}
	return anchored
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

// BuildArtifactPage returns the artifacts in the selected state at the
// selected exact paths. Pagination, exact-path matching, and the head-bound
// cursor keep the answer bounded without hiding that another page exists —
// including for a selector that matches everything, which pages like any
// other rather than dumping the projection.
func BuildArtifactPage(durable app.Snapshot, input ArtifactQuery, degraded bool) (ArtifactPage, error) {
	query, filter, err := normalizeArtifactQuery(input)
	if err != nil {
		return ArtifactPage{}, err
	}
	return buildArtifactPage(durable, ArtifactSelection{Paths: query.Paths, State: ArtifactStateLive, Limit: query.Limit, Cursor: query.Cursor}, filter, degraded)
}

// BuildArtifactSelectionPage exposes the extra finite CLI selectors while
// sharing the exact filtering, row, pagination, and cursor implementation with
// the remote live-path query above.
func BuildArtifactSelectionPage(durable app.Snapshot, input ArtifactSelection, degraded bool) (ArtifactPage, error) {
	query, filter, err := normalizeArtifactSelection(input)
	if err != nil {
		return ArtifactPage{}, err
	}
	return buildArtifactPage(durable, query, filter, degraded)
}

func buildArtifactPage(durable app.Snapshot, query ArtifactSelection, filter string, degraded bool) (ArtifactPage, error) {
	offset, err := decodeArtifactCursor(query.Cursor, durable.Head, filter)
	if err != nil {
		return ArtifactPage{}, err
	}
	wanted := make(map[string]bool, len(query.Paths))
	for _, path := range query.Paths {
		wanted[path] = true
	}
	var anchored map[string]bool
	if query.Reaches != "" {
		anchored = artifactsAnchoredAt(durable.Projection, query.Reaches)
	}
	rows := make([]ArtifactRow, 0, query.Limit)
	matching := 0
	for index := len(durable.Projection.Artifacts) - 1; index >= 0; index-- {
		artifact := durable.Projection.Artifacts[index]
		if !query.State.admits(artifact) {
			continue
		}
		if len(wanted) > 0 && !wanted[artifact.Path] {
			continue
		}
		if anchored != nil && (!anchored[artifact.Event] || artifact.Path == query.Reaches) {
			continue
		}
		position := matching
		matching++
		if position < offset || len(rows) == query.Limit {
			continue
		}
		rows = append(rows, ArtifactRow{Event: artifact.Event, Path: artifact.Path, Commit: artifact.Commit,
			Stale: artifact.Stale, Retired: artifact.Retired, Succeeded: artifact.Succeeded,
			DescribesSupersededWorld: artifact.DescribesSupersededWorld})
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
