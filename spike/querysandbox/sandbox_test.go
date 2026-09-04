package querysandbox

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ncruces/go-sqlite3"
	sqlitedriver "github.com/ncruces/go-sqlite3/driver"
)

func TestApplicationQueryRefusals(t *testing.T) {
	sandbox, _, _ := newSemanticFixture(t)

	tests := []struct {
		class string
		sql   string
	}{
		{"write/insert", `INSERT INTO inventory VALUES ('eraser', 1, 'pink')`},
		{"write/update", `UPDATE inventory SET quantity = 0`},
		{"write/delete", `DELETE FROM inventory`},
		{"DDL/create-table", `CREATE TABLE leaked (value TEXT)`},
		{"DDL/create-index", `CREATE INDEX leaked_index ON inventory(quantity)`},
		{"DDL/create-view", `CREATE VIEW leaked_view AS SELECT * FROM inventory`},
		{"DDL/alter-table", `ALTER TABLE inventory ADD COLUMN leaked TEXT`},
		{"DDL/drop-table", `DROP TABLE inventory`},
		{"PRAGMA/read", `PRAGMA table_info(inventory)`},
		{"PRAGMA/write", `PRAGMA query_only=OFF`},
		{"attachment/attach", `ATTACH DATABASE ':memory:' AS attached`},
		{"attachment/detach", `DETACH DATABASE main`},
		{"extension/load", `SELECT load_extension('not-present')`},
		{"unsafe-function/random", `SELECT random()`},
		{"unsafe-function/connection-state", `SELECT changes()`},
		{"unsafe-function/clock", `SELECT datetime('now')`},
	}
	for _, test := range tests {
		t.Run(test.class, func(t *testing.T) {
			_, err := sandbox.Query(context.Background(), test.sql)
			if err == nil {
				t.Fatalf("refusal assertion failed: %s was admitted", test.class)
			}
			if errors.Is(err, errQueryCancelled) {
				t.Fatalf("refusal assertion failed: %s was cancelled instead of refused", test.class)
			}
		})
	}

	got, err := sandbox.Query(context.Background(), `SELECT sku, quantity FROM inventory_summary WHERE sku <> 'oversize' ORDER BY sku`)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]any{{"ink", int64(3)}, {"paper", int64(8)}}
	if !reflect.DeepEqual(got.Rows, want) {
		t.Fatalf("safe query rows = %#v, want %#v", got.Rows, want)
	}
}

var errPRAGMAAdmitted = errors.New("named PRAGMA refusal assertion failed: PRAGMA was admitted")

func TestAuthorizerMutationProof(t *testing.T) {
	_, path, _ := newSemanticFixture(t)
	assertPRAGMARefused := func(sandbox *Sandbox) error {
		_, err := sandbox.Query(context.Background(), `PRAGMA table_info(inventory)`)
		switch {
		case errors.Is(err, sqlite3.AUTH):
			return nil
		case err == nil:
			return errPRAGMAAdmitted
		default:
			return fmt.Errorf("named PRAGMA refusal assertion got %v, want authorization denied", err)
		}
	}

	guardedOptions := semanticTestOptions()
	guarded, err := open(path, guardedOptions)
	if err != nil {
		t.Fatal(err)
	}
	if err := assertPRAGMARefused(guarded); err != nil {
		t.Fatal(err)
	}
	guarded.Close()

	mutantOptions := guardedOptions
	mutantOptions.installAuthorizer = false
	mutant, err := open(path, mutantOptions)
	if err != nil {
		t.Fatal(err)
	}
	if err := assertPRAGMARefused(mutant); !errors.Is(err, errPRAGMAAdmitted) {
		t.Fatalf("disabled-authorizer PRAGMA error = %v, want admitted query", err)
	}
	mutant.Close()

	restored, err := open(path, guardedOptions)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if err := assertPRAGMARefused(restored); err != nil {
		t.Fatalf("restored authorizer: %v", err)
	}
}

func TestExpensiveQueryIsCancelled(t *testing.T) {
	sandbox, _, _ := newFixture(t)
	assertCancelled(t, sandbox)
}

func TestCancellationMutationProof(t *testing.T) {
	_, path, _ := newFixture(t)
	assertionFailure := func(sandbox *Sandbox) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := sandbox.Query(ctx, expensiveQuery)
		if !errors.Is(err, errQueryCancelled) {
			return fmt.Errorf("named expensive-query cancellation assertion failed: %v", err)
		}
		return nil
	}

	guardedOptions := defaultOptions()
	guardedOptions.deadline = time.Millisecond
	guarded, err := open(path, guardedOptions)
	if err != nil {
		t.Fatal(err)
	}
	if err := assertionFailure(guarded); err != nil {
		t.Fatal(err)
	}
	guarded.Close()

	mutantOptions := guardedOptions
	mutantOptions.enforceDeadline = false
	mutant, err := open(path, mutantOptions)
	if err != nil {
		t.Fatal(err)
	}
	if err := assertionFailure(mutant); err == nil || !strings.Contains(err.Error(), "named expensive-query cancellation assertion failed") {
		t.Fatalf("disabling the cancellation decision did not fail the named assertion: %v", err)
	}
	mutant.Close()

	restored, err := open(path, guardedOptions)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if err := assertionFailure(restored); err != nil {
		t.Fatalf("restored cancellation decision: %v", err)
	}
}

func TestResultRowsAndBytesAreBounded(t *testing.T) {
	sandbox, _, _ := newSemanticFixture(t)

	rows, err := sandbox.Query(context.Background(), `WITH RECURSIVE n(value) AS (VALUES(1) UNION ALL SELECT value + 1 FROM n WHERE value < 100) SELECT value FROM n`)
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Truncated || len(rows.Rows) != MaxRows {
		t.Fatalf("row bound = %d rows, truncated %v; want %d and true", len(rows.Rows), rows.Truncated, MaxRows)
	}

	bytes, err := sandbox.Query(context.Background(), `SELECT description FROM inventory WHERE sku = 'oversize'`)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Truncated || len(bytes.Rows) != 0 {
		t.Fatalf("byte bound returned %#v", bytes)
	}
}

func TestSemanticProofsIgnoreTheInternalDeadline(t *testing.T) {
	options := semanticTestOptions()
	options.deadline = time.Nanosecond

	t.Run("authorization", func(t *testing.T) {
		sandbox, _, _ := newFixtureWithOptions(t, options)
		for iteration := range 64 {
			if _, err := sandbox.Query(context.Background(), `PRAGMA table_info(inventory)`); !errors.Is(err, sqlite3.AUTH) {
				t.Fatalf("iteration %d PRAGMA error = %v, want authorization denied", iteration, err)
			}
		}
	})

	t.Run("result bounds", func(t *testing.T) {
		sandbox, _, _ := newFixtureWithOptions(t, options)
		for iteration := range 64 {
			rows, err := sandbox.Query(context.Background(), `WITH RECURSIVE n(value) AS (VALUES(1) UNION ALL SELECT value + 1 FROM n WHERE value < 100) SELECT value FROM n`)
			if err != nil {
				t.Fatalf("iteration %d row-bound query: %v", iteration, err)
			}
			if !rows.Truncated || len(rows.Rows) != MaxRows {
				t.Fatalf("iteration %d row bound = %d rows, truncated %v; want %d and true", iteration, len(rows.Rows), rows.Truncated, MaxRows)
			}
			bytes, err := sandbox.Query(context.Background(), `SELECT description FROM inventory WHERE sku = 'oversize'`)
			if err != nil {
				t.Fatalf("iteration %d byte-bound query: %v", iteration, err)
			}
			if !bytes.Truncated || len(bytes.Rows) != 0 {
				t.Fatalf("iteration %d byte bound returned %#v", iteration, bytes)
			}
		}
	})
}

func TestColumnValueRejectsUnexpectedSQLiteDatatype(t *testing.T) {
	_, _, err := columnValueOfType(nil, 0, sqlite3.Datatype(0))
	if err == nil || !strings.Contains(err.Error(), "unexpected SQLite datatype 0") {
		t.Fatalf("unexpected datatype error = %v", err)
	}
}

func TestReaderProceedsWhileImmediateFoldTransactionIsOpen(t *testing.T) {
	sandbox, _, writer := newSemanticFixture(t)
	tx, err := writer.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE inventory SET quantity = 99 WHERE sku = 'ink'`); err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	before, err := sandbox.Query(context.Background(), `SELECT quantity FROM inventory WHERE sku = 'ink'`)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("reader waited %v for an open writer transaction", elapsed)
	}
	if want := [][]any{{int64(3)}}; !reflect.DeepEqual(before.Rows, want) {
		t.Fatalf("reader saw uncommitted fold state: %#v", before.Rows)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	after, err := sandbox.Query(context.Background(), `SELECT quantity FROM inventory WHERE sku = 'ink'`)
	if err != nil {
		t.Fatal(err)
	}
	if want := [][]any{{int64(99)}}; !reflect.DeepEqual(after.Rows, want) {
		t.Fatalf("reader did not see committed fold state: %#v", after.Rows)
	}
}

func TestAuthorizerIsDefaultDeny(t *testing.T) {
	for action := sqlite3.AuthorizerActionCode(1); action <= sqlite3.AUTH_RECURSIVE; action++ {
		got := applicationAuthorizer(action, "other", "other", "main", "")
		allowedControlFlow := action == sqlite3.AUTH_SELECT || action == sqlite3.AUTH_RECURSIVE
		if allowedControlFlow && got != sqlite3.AUTH_OK {
			t.Errorf("control-flow action %d denied", action)
		}
		if !allowedControlFlow && got != sqlite3.AUTH_DENY {
			t.Errorf("action %d escaped the default deny", action)
		}
	}
	if got := applicationAuthorizer(sqlite3.AUTH_READ, "inventory", "sku", "main", ""); got != sqlite3.AUTH_OK {
		t.Error("public application-table read denied")
	}
	if got := applicationAuthorizer(sqlite3.AUTH_FUNCTION, "", "count", "", ""); got != sqlite3.AUTH_OK {
		t.Error("allowlisted aggregate denied")
	}
	if got := applicationAuthorizer(sqlite3.AuthorizerActionCode(10_000), "", "", "", ""); got != sqlite3.AUTH_DENY {
		t.Error("unknown future action did not fail closed")
	}
}

const expensiveQuery = `WITH RECURSIVE n(value) AS (VALUES(1) UNION ALL SELECT value + 1 FROM n WHERE value < 100000) SELECT sum(value) FROM n`

func assertCancelled(t *testing.T, sandbox *Sandbox) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	started := time.Now()
	_, err := sandbox.Query(ctx, expensiveQuery)
	if !errors.Is(err, errQueryCancelled) {
		t.Fatalf("expensive query error = %v, want cancellation", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("expensive query cancellation took %v", elapsed)
	}
}

func newFixture(t *testing.T) (*Sandbox, string, *sql.DB) {
	return newFixtureWithOptions(t, defaultOptions())
}

func newSemanticFixture(t *testing.T) (*Sandbox, string, *sql.DB) {
	return newFixtureWithOptions(t, semanticTestOptions())
}

func semanticTestOptions() options {
	options := defaultOptions()
	options.enforceDeadline = false
	return options
}

func newFixtureWithOptions(t *testing.T, options options) (*Sandbox, string, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "projection.sqlite")
	location := &url.URL{Scheme: "file", Path: path}
	values := location.Query()
	values.Add("_pragma", "journal_mode(wal)")
	values.Set("_txlock", "immediate")
	location.RawQuery = values.Encode()
	writer, err := sqlitedriver.Open(location.String(), func(connection *sqlite3.Conn) error {
		if _, err := connection.Config(sqlite3.DBCONFIG_DEFENSIVE, true); err != nil {
			return err
		}
		if _, err := connection.Config(sqlite3.DBCONFIG_TRUSTED_SCHEMA, false); err != nil {
			return err
		}
		_, err := connection.Config(sqlite3.DBCONFIG_ENABLE_LOAD_EXTENSION, false)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	writer.SetMaxOpenConns(1)
	t.Cleanup(func() { writer.Close() })
	if _, err := writer.Exec(`CREATE TABLE inventory (sku TEXT PRIMARY KEY, quantity INTEGER NOT NULL, description TEXT NOT NULL) STRICT`); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Exec(`CREATE VIEW inventory_summary AS SELECT sku, quantity FROM inventory`); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Exec(`CREATE TABLE gitseq_private (secret TEXT NOT NULL) STRICT`); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Exec(`INSERT INTO inventory VALUES ('ink', 3, 'black'), ('paper', 8, 'white'), ('oversize', 1, ?)`, strings.Repeat("x", MaxResultBytes+1)); err != nil {
		t.Fatal(err)
	}

	sandbox, err := open(path, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sandbox.Close() })
	return sandbox, path, writer
}
