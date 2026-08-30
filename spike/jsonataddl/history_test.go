package jsonataddl

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/generalbusiness-ai/gitseq/host"
)

// historyActs replays inserts, upserts, a delete, an uninterpreted foreign
// event, and an ineffective event, so every interpreted position has a
// distinct or deliberately repeated visible state. The verified log carries
// one leading host record before these acts, so act n lands at position n+1.
var historyActs = []testAct{
	{schema: "stock_received", payload: map[string]any{"id": "s1", "sku": "ink", "qty": 5}},        // position 2
	{schema: "reservation_requested", payload: map[string]any{"id": "r1", "sku": "ink", "qty": 2}}, // position 3
	{schema: "future/opaque@0", payload: map[string]any{"kept": true}},                             // position 4, uninterpreted
	{schema: "stock_received", payload: map[string]any{"id": "s2", "sku": "pen", "qty": 4}},        // position 5
	{schema: "reservation_requested", payload: map[string]any{"id": "r2", "sku": "pen", "qty": 1}}, // position 6
	{schema: "reservation_cancelled", payload: map[string]any{"id": "r1"}},                         // position 7, delete
	{schema: "reservation_requested", payload: map[string]any{"id": "r3", "sku": "ink", "qty": 9}}, // position 8, ineffective
	{schema: "stock_received", payload: map[string]any{"id": "s3", "sku": "ink", "qty": 1}},        // position 9, upsert over ink
}

var historyQueries = []string{
	`SELECT sku, available FROM stock ORDER BY sku`,
	`SELECT id, sku, qty FROM reservations ORDER BY id`,
	`SELECT sku, available, reserved FROM sku_summary ORDER BY sku`, // nested: view over view and table
}

func prefixLog(log host.Log, position int) host.Log {
	prefix := host.Log{Genesis: log.Genesis, Head: log.Head, Depth: position, Records: log.Records[:position]}
	if position < log.Depth {
		// The head hash of a shorter verified log differs; the projection
		// only records it, so any non-empty stand-in keeps Build honest.
		prefix.Head = fmt.Sprintf("prefix-head-%d", position)
	}
	return prefix
}

// TestHistoricalViewsMatchCleanPrefixProjections is the historical-view
// exercise: at every interpreted position, the unchanged application SQL run
// through the temporary-schema shadow must return exactly what a clean
// projection built from only that prefix returns through the ordinary
// current-table path — including the nested declared view.
func TestHistoricalViewsMatchCleanPrefixProjections(t *testing.T) {
	ctx := context.Background()
	profile, log := inventoryLog(t, ctx, historyActs...)
	projection := buildProjection(t, ctx, profile, log, filepath.Join(t.TempDir(), "history.sqlite"))
	defer projection.Close()

	for position := 0; position <= log.Depth; position++ {
		reference := buildProjection(t, ctx, profile, prefixLog(log, position),
			filepath.Join(t.TempDir(), fmt.Sprintf("prefix-%d.sqlite", position)))
		for _, query := range historyQueries {
			want, err := reference.Query(ctx, query)
			if err != nil {
				t.Fatalf("prefix %d reference query: %v", position, err)
			}
			got, err := projection.QueryAt(ctx, position, query)
			if err != nil {
				t.Fatalf("QueryAt(%d, %q): %v", position, query, err)
			}
			if !reflect.DeepEqual(got.Rows, want.Rows) || !reflect.DeepEqual(got.Columns, want.Columns) {
				t.Errorf("historical read at position %d diverges from the clean prefix projection\nquery: %s\ngot:  %#v\nwant: %#v",
					position, query, got.Rows, want.Rows)
			}
		}
		reference.Close()
	}

	// The current-table path still serves the same statements, and agrees
	// with the historical read at the frontier position.
	for _, query := range historyQueries {
		current, err := projection.Query(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		historical, err := projection.QueryAt(ctx, log.Depth, query)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(current.Rows, historical.Rows) {
			t.Errorf("frontier historical read diverges from current read\nquery: %s\ncurrent: %#v\nhistorical: %#v",
				query, current.Rows, historical.Rows)
		}
	}
}

// TestHistoricalConnectionRefusesEscapes proves the adversarial
// name-resolution facts the note asks about. Unqualified names — including
// names nested inside re-created views — resolve to the temporary shadow.
// Schema-qualified names escape the shadow by SQLite's own resolution rules;
// they must be refused, never silently answered with current rows.
func TestHistoricalConnectionRefusesEscapes(t *testing.T) {
	ctx := context.Background()
	profile, log := inventoryLog(t, ctx, historyActs...)
	projection := buildProjection(t, ctx, profile, log, filepath.Join(t.TempDir(), "escapes.sqlite"))
	defer projection.Close()

	for _, query := range []string{
		`SELECT available FROM main.stock WHERE sku = 'ink'`, // qualified escape to current rows
		`SELECT sku FROM main.sku_summary`,                   // qualified view whose body reads current rows
		`SELECT verified_head FROM gitseq_frontier`,          // platform relation
		`SELECT event_id FROM gitseq_decisions`,              // platform relation
		`UPDATE stock SET available = 0`,                     // write through the shadow
		`SELECT random()`,                                    // undeclared function
		`SELECT name FROM sqlite_schema`,                     // physical schema
	} {
		if _, err := projection.QueryAt(ctx, 1, query); err == nil {
			t.Errorf("historical QueryAt(%q) succeeded", query)
		}
	}

	// Design fact, not an aspiration: the historical authorizer must admit
	// reads of the version relations because the temporary shadow views are
	// defined over them, and SQLite presents both routes as the same
	// authorizer action. A direct read of a version relation is therefore
	// admitted on this connection (read-only, same rows the shadow serves);
	// hiding it would need statement vetting, not the authorizer.
	direct, err := projection.QueryAt(ctx, 1, `SELECT "__op" FROM "__gitseq_version_stock"`)
	if err != nil {
		t.Errorf("direct version-relation read was refused, contradicting the recorded design fact: %v", err)
	} else if direct.Truncated {
		t.Error("direct version-relation read was truncated")
	}

	if _, err := projection.QueryAt(ctx, log.Depth+1, historyQueries[0]); err == nil {
		t.Error("QueryAt beyond the interpreted frontier succeeded")
	}
	if _, err := projection.QueryAt(ctx, -1, historyQueries[0]); err == nil {
		t.Error("QueryAt at a negative position succeeded")
	}
}

// TestHistoricalReadsSeeDeletedRows pins the delete lifecycle through the
// version relation: reservation r1 is inserted at position 3 and deleted at
// position 7, so it is visible at positions 3..6 and at no other position.
func TestHistoricalReadsSeeDeletedRows(t *testing.T) {
	ctx := context.Background()
	profile, log := inventoryLog(t, ctx, historyActs...)
	projection := buildProjection(t, ctx, profile, log, filepath.Join(t.TempDir(), "deleted.sqlite"))
	defer projection.Close()

	visibleAt := func(position int) bool {
		result, err := projection.QueryAt(ctx, position, `SELECT id FROM reservations WHERE id = 'r1'`)
		if err != nil {
			t.Fatalf("QueryAt(%d): %v", position, err)
		}
		return len(result.Rows) == 1
	}
	for position := 0; position <= log.Depth; position++ {
		want := position >= 3 && position < 7
		if got := visibleAt(position); got != want {
			t.Errorf("r1 visible at position %d = %v, want %v", position, got, want)
		}
	}
}
