package jsonataddl

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/generalbusiness-ai/gitseq/host"
	"github.com/ncruces/go-sqlite3"
)

func TestInventoryReplayIsDeterministicAndQueryable(t *testing.T) {
	ctx := context.Background()
	profile, log := inventoryLog(t, ctx,
		testAct{schema: "stock_received", payload: map[string]any{"id": "s1", "sku": "ink", "qty": 5}},
		testAct{schema: "future/opaque@0", payload: map[string]any{"kept": true}},
		testAct{schema: "reservation_requested", payload: map[string]any{"id": "r1", "sku": "ink", "qty": 2}},
		testAct{schema: "reservation_requested", payload: map[string]any{"id": "r2", "sku": "ink", "qty": 4}},
	)

	first := buildProjection(t, ctx, profile, log, filepath.Join(t.TempDir(), "first.sqlite"))
	defer first.Close()
	second := buildProjection(t, ctx, profile, log, filepath.Join(t.TempDir(), "second.sqlite"))
	defer second.Close()

	query := `SELECT d.position, d.event_type, d.decision, f.kind,
             (SELECT available FROM stock WHERE sku = 'ink') AS available
             FROM gitseq_decisions d LEFT JOIN gitseq_facts f USING (event_id)
             ORDER BY d.position`
	gotFirst, err := first.Query(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	gotSecond, err := second.Query(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotFirst, gotSecond) {
		t.Fatalf("replays differ:\nfirst: %#v\nsecond: %#v", gotFirst, gotSecond)
	}
	if !gotFirst.Frontier.Complete || gotFirst.Frontier.VerifiedHead != log.Head || gotFirst.Frontier.VerifiedDepth != log.Depth || gotFirst.Frontier.InterpretedPosition != log.Depth {
		t.Fatalf("wrong exact frontier: %#v, log %#v", gotFirst.Frontier, log)
	}
	wantRows := [][]any{
		{int64(2), "stock_received", "effective", nil, int64(3)},
		{int64(4), "reservation_requested", "effective", nil, int64(3)},
		{int64(5), "reservation_requested", "ineffective", "insufficient-stock", int64(3)},
	}
	if !reflect.DeepEqual(gotFirst.Rows, wantRows) {
		t.Fatalf("rows = %#v, want %#v", gotFirst.Rows, wantRows)
	}
	reservations, err := first.Query(ctx, `SELECT id, sku, qty FROM reservations ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	if want := [][]any{{"r1", "ink", int64(2)}}; !reflect.DeepEqual(reservations.Rows, want) {
		t.Fatalf("reservations = %#v, want %#v", reservations.Rows, want)
	}
}

func TestFoldFailureRollsBackAndRecordsGap(t *testing.T) {
	ctx := context.Background()
	profile, log := inventoryLog(t, ctx,
		testAct{schema: "stock_received", payload: map[string]any{"id": "s1", "sku": "ink", "qty": 10}},
		testAct{schema: "reservation_requested", payload: map[string]any{"id": "r1", "sku": "ink", "qty": 2}},
		testAct{schema: "reservation_requested", payload: map[string]any{"id": "r1", "sku": "ink", "qty": 2}},
	)
	projection, err := Build(ctx, profile, log, filepath.Join(t.TempDir(), "gap.sqlite"))
	if projection == nil {
		t.Fatal("failure did not return the readable prefix")
	}
	defer projection.Close()
	var gap *GapError
	if !errors.As(err, &gap) || gap.Position != log.Depth {
		t.Fatalf("Build error = %v, want final-position GapError", err)
	}
	frontier := projection.Frontier()
	if frontier.Complete || frontier.InterpretedPosition != log.Depth-1 || frontier.GapEvent != log.Records[len(log.Records)-1].ID {
		t.Fatalf("wrong gap frontier: %#v", frontier)
	}
	stock, queryErr := projection.Query(ctx, `SELECT available FROM stock WHERE sku = 'ink'`)
	if queryErr != nil {
		t.Fatal(queryErr)
	}
	if want := [][]any{{int64(8)}}; !reflect.DeepEqual(stock.Rows, want) {
		t.Fatalf("failed event leaked a row change: %#v", stock.Rows)
	}
}

func TestProjectionQueriesAreReadOnly(t *testing.T) {
	ctx := context.Background()
	profile, log := inventoryLog(t, ctx)
	projection := buildProjection(t, ctx, profile, log, filepath.Join(t.TempDir(), "readonly.sqlite"))
	defer projection.Close()
	for _, query := range []string{
		`UPDATE stock SET available = 0`,
		`PRAGMA table_info(stock)`,
		`SELECT random()`,
		`SELECT 1; SELECT 2`,
	} {
		if _, err := projection.Query(ctx, query); err == nil {
			t.Errorf("Query(%q) succeeded", query)
		}
	}
}

func TestProfileRejectsAmbientJSONata(t *testing.T) {
	files := fstest.MapFS{
		"app/application.sql": {Data: []byte("CREATE EVENT ping (id TEXT NOT NULL); CREATE TABLE pings (id TEXT PRIMARY KEY); CREATE FOLD ping ON ping READ old OPTIONAL ONE AS SELECT id FROM pings WHERE id = :event.id USING 'fold.jsonata' WRITES pings;")},
		"app/fold.jsonata":    {Data: []byte(`{"decision":"effective","facts":[],"tables":{},"time":$now()}`)},
	}
	_, err := Load(files, "app", host.Application{Name: "test", FoldVersion: "test@0"})
	if err == nil || !strings.Contains(err.Error(), "ambient") {
		t.Fatalf("Load error = %v, want ambient-function refusal", err)
	}
}

func TestFoldReadSurfaceIsClosed(t *testing.T) {
	valid, err := compileRead(`SELECT sku, available FROM stock WHERE sku = :event.sku`)
	if err != nil || valid.table != "stock" || valid.whereColumn != "sku" {
		t.Fatalf("inventory read did not compile: %#v, %v", valid, err)
	}
	for _, query := range []string{
		`SELECT changes() FROM stock WHERE sku = :event.sku`,
		`SELECT total_changes() FROM stock WHERE sku = :event.sku`,
		`SELECT last_insert_rowid() FROM stock WHERE sku = :event.sku`,
		`SELECT random() FROM stock WHERE sku = :event.sku`,
		`SELECT * FROM stock WHERE sku = :event.sku`,
		`SELECT sku FROM stock`,
	} {
		if _, err := compileRead(query); err == nil {
			t.Errorf("closed fold-read grammar admitted %q", query)
		}
	}

	tables := map[string]tableDefinition{
		"stock": {columns: []tableColumn{{name: "sku", pk: 1}, {name: "available"}}, primary: []string{"sku"}},
	}
	authorize := foldReadAuthorizer(tables)
	for _, check := range []struct {
		action sqlite3.AuthorizerActionCode
		name3  string
		name4  string
		schema string
		label  string
	}{
		{sqlite3.AUTH_FUNCTION, "", "changes", "", "changes()"},
		{sqlite3.AUTH_FUNCTION, "", "total_changes", "", "total_changes()"},
		{sqlite3.AUTH_FUNCTION, "", "last_insert_rowid", "", "last_insert_rowid()"},
		{sqlite3.AUTH_FUNCTION, "", "random", "", "random()"},
		{sqlite3.AUTH_PRAGMA, "page_count", "", "", "PRAGMA state"},
		{sqlite3.AUTH_READ, "sqlite_schema", "name", "main", "physical schema"},
		{sqlite3.AUTH_READ, "gitseq_frontier", "verified_head", "main", "platform state"},
		{sqlite3.AUTH_RECURSIVE, "", "", "", "recursive work"},
	} {
		if got := authorize(check.action, check.name3, check.name4, check.schema, ""); got != sqlite3.AUTH_DENY {
			t.Errorf("fold-read authority admitted %s", check.label)
		}
	}
}

func TestEvaluationInputByteLimit(t *testing.T) {
	input := func(pad string) map[string]any {
		return map[string]any{
			"meta":  map[string]any{"position": 1, "event_id": "event", "actor": "actor", "event_type": "stock_received"},
			"event": map[string]any{"id": "s1", "sku": "ink", "qty": 1},
			"rows":  map[string]any{"stock_row": map[string]any{"sku": "ink", "available": 0, "padding": pad}},
		}
	}
	empty, err := json.Marshal(input(""))
	if err != nil {
		t.Fatal(err)
	}
	exactInput := input(strings.Repeat("x", maxEvaluationInput-len(empty)))
	exact, err := encodeEvaluationInput(exactInput)
	if err != nil {
		t.Fatalf("exactly-at-cap input refused: %v", err)
	}
	if len(exact) != maxEvaluationInput {
		t.Fatalf("encoded exact input = %d bytes, want %d", len(exact), maxEvaluationInput)
	}
	overInput := input(strings.Repeat("x", maxEvaluationInput-len(empty)+1))
	if encoded, err := encodeEvaluationInput(overInput); err == nil {
		t.Fatalf("one-byte-over input was admitted at %d bytes", len(encoded))
	}
}

func TestGapFailuresDoNotReturnUnstoredFrontier(t *testing.T) {
	ctx := context.Background()
	profile, log := inventoryLog(t, ctx,
		testAct{schema: "stock_received", payload: map[string]any{"id": "s1", "sku": "ink", "qty": 10}},
		testAct{schema: "reservation_requested", payload: map[string]any{"id": "r1", "sku": "ink", "qty": 2}},
		testAct{schema: "reservation_requested", payload: map[string]any{"id": "r1", "sku": "ink", "qty": 2}},
	)

	t.Run("persistence", func(t *testing.T) {
		injected := errors.New("injected gap persistence failure")
		options := newBuildOptions()
		options.persistGap = func(context.Context, *sql.DB, Frontier) error { return injected }
		projection, err := build(ctx, profile, log, filepath.Join(t.TempDir(), "persist.sqlite"), options)
		if projection != nil {
			t.Fatal("gap persistence failure returned a projection with an unstored frontier")
		}
		if !errors.Is(err, injected) {
			t.Fatalf("Build error = %v, want injected persistence failure", err)
		}
	})

	t.Run("writer close", func(t *testing.T) {
		injected := errors.New("injected writer close failure")
		options := newBuildOptions()
		options.closeWriter = func(writer *sql.DB) error {
			return errors.Join(writer.Close(), injected)
		}
		projection, err := build(ctx, profile, log, filepath.Join(t.TempDir(), "close.sqlite"), options)
		if projection != nil {
			t.Fatal("writer close failure returned a projection")
		}
		if !errors.Is(err, injected) {
			t.Fatalf("Build error = %v, want injected writer close failure", err)
		}
	})
}

type testAct struct {
	schema  string
	payload map[string]any
}

func inventoryLog(t *testing.T, ctx context.Context, acts ...testAct) (*Profile, host.Log) {
	t.Helper()
	profile, err := LoadInventory()
	if err != nil {
		t.Fatal(err)
	}
	repository := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.CommandContext(ctx, "git", "init", "-q", repository).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	_, signer, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := host.Init(ctx, repository, profile.Application, signer, host.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for index, act := range acts {
		payload, err := json.Marshal(act.payload)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := workspace.Append(ctx, signer, host.Act{Schema: act.schema, Payload: payload, IdempotencyKey: act.schema + "-" + string(rune('a'+index))}); err != nil {
			t.Fatal(err)
		}
	}
	// Reopen before reading: the projection consumes only records recovered
	// from and cryptographically verified against the Git object database.
	reopened, err := host.Open(ctx, repository, profile.Application)
	if err != nil {
		t.Fatal(err)
	}
	log, err := reopened.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return profile, log
}

func buildProjection(t *testing.T, ctx context.Context, profile *Profile, log host.Log, path string) *Projection {
	t.Helper()
	projection, err := Build(ctx, profile, log, path)
	if err != nil {
		t.Fatal(err)
	}
	return projection
}
