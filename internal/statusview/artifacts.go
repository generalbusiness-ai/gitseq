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

// ArtifactQuery selects artifacts by exact path and lifecycle state. Paths are
// wire keys: they are not cleaned, expanded, prefix-matched, or interpreted as
// globs.
type ArtifactQuery struct {
	Paths []string `json:"paths"`
	// State defaults to ArtifactStateLive, which is the behaviour every
	// caller had before this field, so an omitted state is not a widening.
	State ArtifactState `json:"state,omitempty"`
	// Reaches selects artifacts whose chain of artifact bases arrives at an
	// artifact recorded at this exact path, following provenance from artifact
	// to artifact for as many hops as the chain has. It is the anchor a
	// document follows to say which behaviour it describes, and it is the one
	// selector that reads more than a single row's own fields.
	//
	// An artifact recorded at the anchor path itself is excluded. It is the
	// anchor, not something anchored to it, and returning the anchors when
	// asked what still points at them answers a question nobody asked.
	Reaches string `json:"reaches,omitempty"`
	Limit   int    `json:"limit,omitempty"`
	Cursor  string `json:"cursor,omitempty"`
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
	Event  string `json:"event"`
	Path   string `json:"path"`
	Commit string `json:"commit"`
	Stale  bool   `json:"stale"`
	// Retired stays because it is in the shipped shape that the resident HTTP
	// and MCP surfaces already return. It answers a narrower question than
	// State: whether the pointer was withdrawn at all, not whether a successor
	// was named in its place.
	Retired bool `json:"retired"`
	// State is the lifecycle the selector matched on, carried so that a row
	// says which of the four states it is in rather than leaving the reader to
	// reconstruct it from Retired and be unable to.
	State                    ArtifactState `json:"state"`
	DescribesSupersededWorld bool          `json:"describes_superseded_world"`
}

// Lifecycle is the one place any reader asks a row what it is. A row decoded
// from a resident older than the State field carries an empty string; the
// narrower Retired bit still answers, and it answers "retired" because that
// older resident could not tell succeeded apart either. Every caller reads
// this rather than switching on the fields, so there is one lifecycle fact and
// one fallback rather than a copy of each per call site.
func (row ArtifactRow) Lifecycle() ArtifactState {
	if row.State != "" {
		return row.State
	}
	if row.Retired {
		return ArtifactStateRetired
	}
	return ArtifactStateLive
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
	// A path-less query is admitted only when an anchor selects the population
	// instead. Without that, an empty path list is the request for everything,
	// and refusing it is what has always kept an unbounded dump off this
	// surface; the refusal is left exactly where it was for every caller that
	// names no anchor.
	if len(input.Paths) == 0 && input.Reaches == "" {
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
	switch input.State {
	case "":
		input.State = ArtifactStateLive
	case ArtifactStateLive, ArtifactStateRetired, ArtifactStateSucceeded, ArtifactStateAll:
	default:
		return ArtifactQuery{}, "", fmt.Errorf("unknown artifact state %q", input.State)
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
	// Every selector goes into the fingerprint, not just the paths. A cursor
	// carries the fingerprint, so continuing one page's cursor into a query
	// with a different state or anchor is refused rather than silently
	// splicing two different result sets together.
	fingerprintInput := struct {
		Paths   []string      `json:"paths"`
		State   ArtifactState `json:"state"`
		Reaches string        `json:"reaches"`
	}{paths, input.State, input.Reaches}
	encoded, _ := json.Marshal(fingerprintInput)
	sum := sha256.Sum256(encoded)
	return input, hex.EncodeToString(sum[:]), nil
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
			Stale: artifact.Stale, Retired: artifact.Retired, State: lifecycleOf(artifact),
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
