package jsonataddl

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/generalbusiness-ai/gitseq/host"
	"github.com/ncruces/go-sqlite3"
	sqlitedriver "github.com/ncruces/go-sqlite3/driver"
)

type tableColumn struct {
	name         string
	declaredType string
	pk           int
}

type tableDefinition struct {
	columns []tableColumn
	primary []string
}

// Frontier names both the verified log and the prefix this projection has
// interpreted. Complete is false when a fold or constraint failure left a
// deliberate gap.
type Frontier struct {
	Genesis             string `json:"genesis"`
	VerifiedHead        string `json:"verified_head"`
	VerifiedDepth       int    `json:"verified_depth"`
	InterpretedEvent    string `json:"interpreted_event,omitempty"`
	InterpretedPosition int    `json:"interpreted_position"`
	Complete            bool   `json:"complete"`
	GapEvent            string `json:"gap_event,omitempty"`
	GapReason           string `json:"gap_reason,omitempty"`
}

// Projection is a disposable SQLite view rebuilt from one verified host log.
type Projection struct {
	db       *sql.DB
	path     string
	frontier Frontier
	tables   map[string]tableDefinition
	views    []string
}

// QueryResult carries the exact frontier alongside bounded typed SQL rows.
type QueryResult struct {
	Frontier  Frontier `json:"frontier"`
	Columns   []string `json:"columns"`
	Rows      [][]any  `json:"rows"`
	Truncated bool     `json:"truncated"`
}

// GapError reports the first verified event the application could not
// interpret. Row changes for that event are rolled back atomically.
type GapError struct {
	Event    string
	Position int
	Err      error
}

func (e *GapError) Error() string {
	return fmt.Sprintf("interpret event %s at position %d: %v", e.Event, e.Position, e.Err)
}

func (e *GapError) Unwrap() error { return e.Err }

type buildOptions struct {
	persistGap  func(context.Context, *sql.DB, Frontier) error
	closeWriter func(*sql.DB) error
	// writerVFS names a registered SQLite VFS for the projection writer. The
	// crash tests use it to record every durable mutation; production reuse
	// is not implied.
	writerVFS string
	// afterInitialize and afterEvent are observation seams for the crash
	// tests: they mark the schema-initialization commit and each event-commit
	// boundary, and let a test force a mid-replay checkpoint.
	afterInitialize func(context.Context, *sql.DB) error
	afterEvent      func(context.Context, *sql.DB, int) error
}

func newBuildOptions() buildOptions {
	return buildOptions{
		persistGap: func(ctx context.Context, writer *sql.DB, frontier Frontier) error {
			result, err := writer.ExecContext(ctx, `UPDATE gitseq_frontier SET gap_event = ?, gap_reason = ? WHERE singleton = 1`, frontier.GapEvent, frontier.GapReason)
			if err != nil {
				return err
			}
			rows, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if rows != 1 {
				return fmt.Errorf("gap metadata updated %d frontier rows, want 1", rows)
			}
			return nil
		},
		closeWriter: func(writer *sql.DB) error { return writer.Close() },
	}
}

// Build replays a verified log into a new file-backed WAL database. It refuses
// to overwrite an existing path; cache reuse and replacement are intentionally
// outside this spike.
func Build(ctx context.Context, profile *Profile, log host.Log, databasePath string) (*Projection, error) {
	return build(ctx, profile, log, databasePath, newBuildOptions())
}

func build(ctx context.Context, profile *Profile, log host.Log, databasePath string, options buildOptions) (*Projection, error) {
	if profile == nil {
		return nil, errors.New("profile is required")
	}
	if log.Genesis == "" || log.Head == "" || log.Depth != len(log.Records) {
		return nil, errors.New("verified log frontier is incomplete")
	}
	if databasePath == "" {
		return nil, errors.New("database path is required")
	}
	absolute, err := filepath.Abs(databasePath)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(absolute); err == nil {
		return nil, errors.New("projection database already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return nil, err
	}
	writer, err := openWriter(ctx, absolute, options.writerVFS)
	if err != nil {
		return nil, err
	}
	frontier := Frontier{Genesis: log.Genesis, VerifiedHead: log.Head, VerifiedDepth: log.Depth}
	tables, err := initialize(ctx, writer, profile, frontier)
	if err != nil {
		return nil, errors.Join(err, options.closeWriter(writer))
	}
	if options.afterInitialize != nil {
		if err := options.afterInitialize(ctx, writer); err != nil {
			return nil, errors.Join(err, options.closeWriter(writer))
		}
	}
	foldReader, err := openFoldReader(ctx, absolute, tables)
	if err != nil {
		return nil, errors.Join(err, options.closeWriter(writer))
	}
	for index, record := range log.Records {
		position := index + 1
		if err := foldRecord(ctx, writer, foldReader, profile, tables, record, position); err != nil {
			gap := &GapError{Event: record.ID, Position: position, Err: err}
			frontier.GapEvent, frontier.GapReason = record.ID, gap.Error()
			persistErr := options.persistGap(ctx, writer, frontier)
			foldCloseErr := foldReader.Close()
			writerCloseErr := options.closeWriter(writer)
			if persistErr != nil || foldCloseErr != nil || writerCloseErr != nil {
				return nil, errors.Join(gap, wrapError("persist gap metadata", persistErr), wrapError("close fold reader", foldCloseErr), wrapError("close projection writer", writerCloseErr))
			}
			reader, openErr := openReader(ctx, absolute)
			if openErr != nil {
				return nil, errors.Join(gap, openErr)
			}
			loaded, loadErr := readFrontier(ctx, reader, frontier)
			if loadErr != nil {
				reader.Close()
				return nil, errors.Join(gap, loadErr)
			}
			return &Projection{db: reader, path: absolute, frontier: loaded, tables: tables, views: declaredViews(profile)}, gap
		}
		frontier.InterpretedEvent, frontier.InterpretedPosition = record.ID, position
		if options.afterEvent != nil {
			if err := options.afterEvent(ctx, writer, position); err != nil {
				return nil, errors.Join(err, foldReader.Close(), options.closeWriter(writer))
			}
		}
	}
	frontier.Complete = true
	if _, err := writer.ExecContext(ctx, `UPDATE gitseq_frontier SET complete = 1 WHERE singleton = 1`); err != nil {
		return nil, errors.Join(err, foldReader.Close(), options.closeWriter(writer))
	}
	if err := errors.Join(foldReader.Close(), options.closeWriter(writer)); err != nil {
		return nil, err
	}
	reader, err := openReader(ctx, absolute)
	if err != nil {
		return nil, err
	}
	return &Projection{db: reader, path: absolute, frontier: frontier, tables: tables, views: declaredViews(profile)}, nil
}

func wrapError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func openWriter(ctx context.Context, path, vfsName string) (*sql.DB, error) {
	dsn := sqliteDSN(path, false)
	if vfsName != "" {
		dsn += "&vfs=" + url.QueryEscape(vfsName)
	}
	database, err := sqlitedriver.Open(dsn, func(connection *sqlite3.Conn) error {
		if _, err := connection.Config(sqlite3.DBCONFIG_DEFENSIVE, true); err != nil {
			return err
		}
		if _, err := connection.Config(sqlite3.DBCONFIG_TRUSTED_SCHEMA, false); err != nil {
			return err
		}
		if _, err := connection.Config(sqlite3.DBCONFIG_ENABLE_LOAD_EXTENSION, false); err != nil {
			return err
		}
		return connection.Exec(`PRAGMA foreign_keys=ON`)
	})
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(1)
	if err := database.PingContext(ctx); err != nil {
		database.Close()
		return nil, err
	}
	var journal string
	if err := database.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&journal); err != nil || strings.ToLower(journal) != "wal" {
		database.Close()
		if err != nil {
			return nil, fmt.Errorf("read SQLite journal mode: %w", err)
		}
		return nil, fmt.Errorf("SQLite journal mode is %q, want wal", journal)
	}
	return database, nil
}

func openReader(ctx context.Context, path string) (*sql.DB, error) {
	database, err := sqlitedriver.Open(sqliteDSN(path, true), func(connection *sqlite3.Conn) error {
		if _, err := connection.Config(sqlite3.DBCONFIG_DEFENSIVE, true); err != nil {
			return err
		}
		if _, err := connection.Config(sqlite3.DBCONFIG_TRUSTED_SCHEMA, false); err != nil {
			return err
		}
		if _, err := connection.Config(sqlite3.DBCONFIG_ENABLE_LOAD_EXTENSION, false); err != nil {
			return err
		}
		if err := connection.Exec(`PRAGMA query_only=ON`); err != nil {
			return err
		}
		return connection.SetAuthorizer(queryAuthorizer)
	})
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(4)
	return database, nil
}

// openFoldReader creates the only SQL surface visible to fold programs. It is
// a separate read-only connection opened after schema initialization, so a
// fold observes the committed n-1 projection even while the writer holds its
// immediate transaction for event n.
func openFoldReader(ctx context.Context, path string, tables map[string]tableDefinition) (*sql.DB, error) {
	database, err := sqlitedriver.Open(sqliteDSN(path, true), func(connection *sqlite3.Conn) error {
		if _, err := connection.Config(sqlite3.DBCONFIG_DEFENSIVE, true); err != nil {
			return err
		}
		if _, err := connection.Config(sqlite3.DBCONFIG_TRUSTED_SCHEMA, false); err != nil {
			return err
		}
		if _, err := connection.Config(sqlite3.DBCONFIG_ENABLE_LOAD_EXTENSION, false); err != nil {
			return err
		}
		connection.Limit(sqlite3.LIMIT_LENGTH, maxFoldSQLiteValue)
		connection.Limit(sqlite3.LIMIT_SQL_LENGTH, maxFoldReadSQL)
		connection.Limit(sqlite3.LIMIT_COLUMN, 32)
		connection.Limit(sqlite3.LIMIT_EXPR_DEPTH, 8)
		connection.Limit(sqlite3.LIMIT_COMPOUND_SELECT, 1)
		connection.Limit(sqlite3.LIMIT_VDBE_OP, maxFoldReadVDBE)
		connection.Limit(sqlite3.LIMIT_FUNCTION_ARG, 8)
		connection.Limit(sqlite3.LIMIT_ATTACHED, 0)
		connection.Limit(sqlite3.LIMIT_VARIABLE_NUMBER, 1)
		connection.Limit(sqlite3.LIMIT_WORKER_THREADS, 0)
		if err := connection.Exec(`PRAGMA query_only=ON`); err != nil {
			return err
		}
		return connection.SetAuthorizer(foldReadAuthorizer(tables))
	})
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(1)
	if err := database.PingContext(ctx); err != nil {
		database.Close()
		return nil, err
	}
	return database, nil
}

func foldReadAuthorizer(tables map[string]tableDefinition) func(sqlite3.AuthorizerActionCode, string, string, string, string) sqlite3.AuthorizerReturnCode {
	return func(action sqlite3.AuthorizerActionCode, name3rd, name4th, schema, inner string) sqlite3.AuthorizerReturnCode {
		switch action {
		case sqlite3.AUTH_SELECT:
			return sqlite3.AUTH_OK
		case sqlite3.AUTH_READ:
			definition, exists := tables[name3rd]
			if schema != "main" || !exists || !tableHasColumn(definition, name4th) || inner != "" {
				return sqlite3.AUTH_DENY
			}
			return sqlite3.AUTH_OK
		case sqlite3.AUTH_PRAGMA:
			// database/sql reasserts this connection invariant after the
			// authorizer is installed. No application PRAGMA is admitted.
			if strings.EqualFold(name3rd, "query_only") && name4th == "" {
				return sqlite3.AUTH_OK
			}
			return sqlite3.AUTH_DENY
		default:
			// Functions (including connection-state functions), recursion,
			// physical schema access, writes and connection changes all deny.
			return sqlite3.AUTH_DENY
		}
	}
}

func tableHasColumn(table tableDefinition, wanted string) bool {
	for _, column := range table.columns {
		if column.name == wanted {
			return true
		}
	}
	return false
}

func sqliteDSN(path string, readOnly bool) string {
	location := &url.URL{Scheme: "file", Path: path}
	values := url.Values{"_pragma": {"busy_timeout(5000)"}}
	if readOnly {
		values.Set("mode", "ro")
	} else {
		values.Add("_pragma", "journal_mode(wal)")
		values.Set("_txlock", "immediate")
	}
	location.RawQuery = values.Encode()
	return location.String()
}

func queryAuthorizer(action sqlite3.AuthorizerActionCode, name3rd, name4th, schema, inner string) sqlite3.AuthorizerReturnCode {
	switch action {
	case sqlite3.AUTH_SELECT, sqlite3.AUTH_RECURSIVE:
		return sqlite3.AUTH_OK
	case sqlite3.AUTH_READ:
		if (schema != "" && schema != "main") || strings.HasPrefix(name3rd, "sqlite_") || strings.HasPrefix(name3rd, "__gitseq_") {
			return sqlite3.AUTH_DENY
		}
		return sqlite3.AUTH_OK
	case sqlite3.AUTH_FUNCTION:
		switch strings.ToLower(name4th) {
		case "avg", "coalesce", "count", "max", "min", "sum", "total":
			return sqlite3.AUTH_OK
		default:
			return sqlite3.AUTH_DENY
		}
	case sqlite3.AUTH_PRAGMA:
		// database/sql reasserts query_only when it checks out a connection,
		// after the authorizer has been installed. This harmless connection
		// invariant must remain admissible; application PRAGMAs stay denied.
		if strings.EqualFold(name3rd, "query_only") && name4th == "" {
			return sqlite3.AUTH_OK
		}
		return sqlite3.AUTH_DENY
	default:
		return sqlite3.AUTH_DENY
	}
}

// initialize creates the complete physical schema and the identity and
// frontier records in one transaction. A crash during initialization
// therefore leaves either an empty database (no frontier relation: an
// unambiguous discard-and-rebuild signal for a disposable projection) or the
// complete initialized schema, never a partial schema carrying a plausible
// frontier row.
func initialize(ctx context.Context, database *sql.DB, profile *Profile, frontier Frontier) (map[string]tableDefinition, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	platform := []string{
		`CREATE TABLE gitseq_projection_identity (singleton INTEGER PRIMARY KEY CHECK (singleton = 1), genesis TEXT NOT NULL, application TEXT NOT NULL, fold_version TEXT NOT NULL, schema_digest TEXT NOT NULL) STRICT`,
		`CREATE TABLE gitseq_frontier (singleton INTEGER PRIMARY KEY CHECK (singleton = 1), verified_head TEXT NOT NULL, verified_depth INTEGER NOT NULL, interpreted_event TEXT NOT NULL DEFAULT '', interpreted_position INTEGER NOT NULL DEFAULT 0, complete INTEGER NOT NULL DEFAULT 0, gap_event TEXT NOT NULL DEFAULT '', gap_reason TEXT NOT NULL DEFAULT '') STRICT`,
		`CREATE TABLE gitseq_decisions (position INTEGER PRIMARY KEY, event_id TEXT NOT NULL UNIQUE, event_type TEXT NOT NULL, decision TEXT NOT NULL CHECK (decision IN ('effective', 'ineffective'))) STRICT`,
		`CREATE TABLE gitseq_facts (position INTEGER NOT NULL, event_id TEXT NOT NULL, ordinal INTEGER NOT NULL, kind TEXT NOT NULL, fact_json TEXT NOT NULL CHECK (json_valid(fact_json)), PRIMARY KEY (event_id, ordinal)) STRICT`,
	}
	for _, statement := range platform {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO gitseq_projection_identity VALUES (1, ?, ?, ?, ?)`, frontier.Genesis, profile.Application.Name, profile.Application.FoldVersion, profile.SchemaDigest); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO gitseq_frontier (singleton, verified_head, verified_depth) VALUES (1, ?, ?)`, frontier.VerifiedHead, frontier.VerifiedDepth); err != nil {
		return nil, err
	}
	for _, statement := range profile.ddl {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return nil, fmt.Errorf("application DDL: %w", err)
		}
	}
	for _, event := range profile.events {
		statement := `CREATE TABLE ` + quoteIdentifier("__gitseq_event_"+event.name) + ` (` + event.columnsSQL + `) STRICT`
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return nil, fmt.Errorf("event %q schema: %w", event.name, err)
		}
	}
	tables := make(map[string]tableDefinition)
	for _, statement := range profile.ddl {
		match := tableRE.FindStringSubmatch(statement)
		if match == nil {
			continue
		}
		definition, err := inspectTable(ctx, tx, match[1])
		if err != nil {
			return nil, err
		}
		if len(definition.primary) == 0 {
			return nil, fmt.Errorf("writable application table %q has no primary key", match[1])
		}
		tables[match[1]] = definition
		for _, versionStatement := range versionTableDDL(match[1], definition) {
			if _, err := tx.ExecContext(ctx, versionStatement); err != nil {
				return nil, fmt.Errorf("version relation for %q: %w", match[1], err)
			}
		}
	}
	for _, fold := range profile.folds {
		definition, exists := tables[fold.read.table]
		if !exists {
			return nil, fmt.Errorf("fold %q reads undeclared application table %q", fold.name, fold.read.table)
		}
		if len(definition.primary) != 1 || definition.primary[0] != fold.read.whereColumn {
			return nil, fmt.Errorf("fold %q read must constrain the table's complete primary key", fold.name)
		}
		for _, column := range fold.read.columns {
			if !tableHasColumn(definition, column) {
				return nil, fmt.Errorf("fold %q reads undeclared column %q", fold.name, column)
			}
		}
		statement, err := tx.PrepareContext(ctx, fold.read.query)
		if err != nil {
			return nil, fmt.Errorf("prepare fold %q read: %w", fold.name, err)
		}
		statement.Close()
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return tables, nil
}

func inspectTable(ctx context.Context, database *sql.Tx, table string) (tableDefinition, error) {
	rows, err := database.QueryContext(ctx, `PRAGMA table_xinfo(`+quoteIdentifier(table)+`)`)
	if err != nil {
		return tableDefinition{}, err
	}
	defer rows.Close()
	var definition tableDefinition
	for rows.Next() {
		var cid, notNull, pk, hidden int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &pk, &hidden); err != nil {
			return tableDefinition{}, err
		}
		if hidden != 0 {
			continue
		}
		definition.columns = append(definition.columns, tableColumn{name: name, declaredType: kind, pk: pk})
	}
	if err := rows.Err(); err != nil {
		return tableDefinition{}, err
	}
	primary := append([]tableColumn(nil), definition.columns...)
	sort.Slice(primary, func(i, j int) bool { return primary[i].pk < primary[j].pk })
	for _, column := range primary {
		if column.pk > 0 {
			definition.primary = append(definition.primary, column.name)
		}
	}
	return definition, nil
}

func foldRecord(ctx context.Context, database, foldReader *sql.DB, profile *Profile, tables map[string]tableDefinition, record host.Record, position int) error {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	fold, interpreted := profile.folds[record.Schema]
	if !interpreted {
		if err := advanceFrontier(ctx, tx, record.ID, position); err != nil {
			return err
		}
		return tx.Commit()
	}
	event, err := decodeEvent(profile.events[record.Schema], record.Payload)
	if err != nil {
		return err
	}
	if err := validateEvent(ctx, tx, profile.events[record.Schema], event); err != nil {
		return err
	}
	read, err := runRead(ctx, foldReader, fold.read, event)
	if err != nil {
		return err
	}
	input := map[string]any{
		"meta":  map[string]any{"position": position, "event_id": record.ID, "actor": record.Actor, "event_type": record.Schema},
		"event": event,
		"rows":  map[string]any{fold.read.name: read},
	}
	encoded, err := encodeEvaluationInput(input)
	if err != nil {
		return err
	}
	result, err := fold.program.Evaluate(encoded, nil)
	if err != nil {
		return fmt.Errorf("evaluate fold %q: %w", fold.name, err)
	}
	if len(result) == 0 || len(result) > maxOutputBytes {
		return errors.New("fold output is empty or too large")
	}
	output, err := decodeOutput(result)
	if err != nil {
		return err
	}
	if err := applyOutput(ctx, tx, fold, tables, output, record.ID, position); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO gitseq_decisions VALUES (?, ?, ?, ?)`, position, record.ID, record.Schema, output.Decision); err != nil {
		return err
	}
	for ordinal, fact := range output.Facts {
		kind, ok := fact["kind"].(string)
		if !ok || kind == "" {
			return errors.New("fold fact needs a non-empty kind")
		}
		encoded, err := json.Marshal(fact)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO gitseq_facts VALUES (?, ?, ?, ?, ?)`, position, record.ID, ordinal, kind, string(encoded)); err != nil {
			return err
		}
	}
	if err := advanceFrontier(ctx, tx, record.ID, position); err != nil {
		return err
	}
	return tx.Commit()
}

func encodeEvaluationInput(input any) ([]byte, error) {
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	if len(encoded) > maxEvaluationInput {
		return nil, fmt.Errorf("encoded JSONata input is %d bytes, limit is %d", len(encoded), maxEvaluationInput)
	}
	return encoded, nil
}

func advanceFrontier(ctx context.Context, tx *sql.Tx, event string, position int) error {
	_, err := tx.ExecContext(ctx, `UPDATE gitseq_frontier SET interpreted_event = ?, interpreted_position = ? WHERE singleton = 1`, event, position)
	return err
}

func decodeEvent(declaration eventDefinition, payload []byte) (map[string]any, error) {
	if len(payload) == 0 || len(payload) > maxPayloadBytes {
		return nil, errors.New("event payload is empty or too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var event map[string]any
	if err := decoder.Decode(&event); err != nil || event == nil {
		return nil, errors.New("event payload is not a JSON object")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("event payload has trailing data")
	}
	canonical, err := json.Marshal(event)
	if err != nil || !bytes.Equal(canonical, payload) {
		return nil, errors.New("event payload is not canonically encoded")
	}
	if len(event) != len(declaration.columns) {
		return nil, errors.New("event payload does not have exactly the declared fields")
	}
	for _, column := range declaration.columns {
		if _, exists := event[column]; !exists {
			return nil, fmt.Errorf("event payload is missing %q", column)
		}
	}
	return event, nil
}

func validateEvent(ctx context.Context, tx *sql.Tx, declaration eventDefinition, event map[string]any) error {
	table := quoteIdentifier("__gitseq_event_" + declaration.name)
	if _, err := tx.ExecContext(ctx, `DELETE FROM `+table); err != nil {
		return err
	}
	columns := make([]string, len(declaration.columns))
	values := make([]any, len(declaration.columns))
	marks := make([]string, len(declaration.columns))
	for index, column := range declaration.columns {
		columns[index], marks[index] = quoteIdentifier(column), "?"
		value, err := sqlValue(event[column])
		if err != nil {
			return fmt.Errorf("event field %q: %w", column, err)
		}
		values[index] = value
	}
	statement := `INSERT INTO ` + table + ` (` + strings.Join(columns, ",") + `) VALUES (` + strings.Join(marks, ",") + `)`
	if _, err := tx.ExecContext(ctx, statement, values...); err != nil {
		return fmt.Errorf("event payload violates its declaration: %w", err)
	}
	return nil
}

func runRead(ctx context.Context, database *sql.DB, read readDefinition, event map[string]any) (any, error) {
	argument, err := sqlValue(event[read.parameter])
	if err != nil {
		return nil, err
	}
	rows, err := database.QueryContext(ctx, read.query, argument)
	if err != nil {
		return nil, fmt.Errorf("fold read %q: %w", read.name, err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var found []map[string]any
	for rows.Next() {
		if len(found) == 2 {
			return nil, fmt.Errorf("fold read %q returned more than one row", read.name)
		}
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for index := range values {
			pointers[index] = &values[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(columns))
		for index, column := range columns {
			if _, duplicate := row[column]; duplicate {
				return nil, fmt.Errorf("fold read %q returns duplicate column %q", read.name, column)
			}
			row[column] = jsonValue(values[index])
		}
		found = append(found, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	switch read.cardinality {
	case "ONE":
		if len(found) != 1 {
			return nil, fmt.Errorf("fold read %q expected one row, got %d", read.name, len(found))
		}
		return found[0], nil
	case "OPTIONAL ONE":
		if len(found) == 0 {
			return nil, nil
		}
		if len(found) != 1 {
			return nil, fmt.Errorf("fold read %q expected at most one row, got %d", read.name, len(found))
		}
		return found[0], nil
	default:
		return nil, fmt.Errorf("unsupported cardinality %q", read.cardinality)
	}
}

type foldOutput struct {
	Decision string                  `json:"decision"`
	Facts    []map[string]any        `json:"facts"`
	Tables   map[string]tableChanges `json:"tables"`
}

type tableChanges struct {
	Insert []map[string]any `json:"insert"`
	Upsert []map[string]any `json:"upsert"`
	Delete []map[string]any `json:"delete"`
}

func decodeOutput(payload []byte) (foldOutput, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	var output foldOutput
	if err := decoder.Decode(&output); err != nil {
		return foldOutput{}, fmt.Errorf("fold output has the wrong shape: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return foldOutput{}, errors.New("fold output has trailing data")
	}
	if output.Decision != "effective" && output.Decision != "ineffective" {
		return foldOutput{}, errors.New("fold decision must be effective or ineffective")
	}
	if len(output.Facts) > maxFacts {
		return foldOutput{}, errors.New("fold produced too many facts")
	}
	changes := 0
	for _, table := range output.Tables {
		changes += len(table.Insert) + len(table.Upsert) + len(table.Delete)
	}
	if changes > maxRowChanges {
		return foldOutput{}, errors.New("fold produced too many row changes")
	}
	if output.Decision == "ineffective" && changes != 0 {
		return foldOutput{}, errors.New("ineffective fold output may not change tables")
	}
	return output, nil
}

func applyOutput(ctx context.Context, tx *sql.Tx, fold foldDefinition, tables map[string]tableDefinition, output foldOutput, event string, position int) error {
	names := make([]string, 0, len(output.Tables))
	for table := range output.Tables {
		names = append(names, table)
	}
	sort.Strings(names)
	for _, table := range names {
		if !fold.writes[table] {
			return fmt.Errorf("fold %q wrote undeclared table %q", fold.name, table)
		}
		definition, exists := tables[table]
		if !exists {
			return fmt.Errorf("fold %q wrote unknown table %q", fold.name, table)
		}
		changes := output.Tables[table]
		for _, row := range changes.Insert {
			if err := insertRow(ctx, tx, table, definition, row, false, event, position); err != nil {
				return err
			}
		}
		for _, row := range changes.Upsert {
			if err := insertRow(ctx, tx, table, definition, row, true, event, position); err != nil {
				return err
			}
		}
		for _, row := range changes.Delete {
			if err := deleteRow(ctx, tx, table, definition, row, position); err != nil {
				return err
			}
		}
	}
	return nil
}

func insertRow(ctx context.Context, tx *sql.Tx, table string, definition tableDefinition, row map[string]any, upsert bool, event string, position int) error {
	if len(row) != len(definition.columns) {
		return fmt.Errorf("%s row is not complete: got fields %v", table, sortedKeys(row))
	}
	columns := make([]string, len(definition.columns))
	marks := make([]string, len(definition.columns))
	values := make([]any, len(definition.columns))
	for index, column := range definition.columns {
		value, exists := row[column.name]
		if !exists {
			return fmt.Errorf("%s row is missing %q", table, column.name)
		}
		encoded, err := sqlValue(value)
		if err != nil {
			return fmt.Errorf("%s.%s: %w", table, column.name, err)
		}
		columns[index], marks[index], values[index] = quoteIdentifier(column.name), "?", encoded
	}
	statement := `INSERT INTO ` + quoteIdentifier(table) + ` (` + strings.Join(columns, ",") + `) VALUES (` + strings.Join(marks, ",") + `)`
	if upsert {
		updates := make([]string, 0, len(definition.columns))
		for _, column := range definition.columns {
			if !contains(definition.primary, column.name) {
				quoted := quoteIdentifier(column.name)
				updates = append(updates, quoted+` = excluded.`+quoted)
			}
		}
		statement += ` ON CONFLICT (` + quotedList(definition.primary) + `) `
		if len(updates) == 0 {
			statement += `DO NOTHING`
		} else {
			statement += `DO UPDATE SET ` + strings.Join(updates, ",")
		}
	}
	if _, err := tx.ExecContext(ctx, statement, values...); err != nil {
		return fmt.Errorf("apply %s row: %w", table, err)
	}
	operation := "insert"
	if upsert {
		operation = "upsert"
	}
	return recordVersionChange(ctx, tx, table, definition, values, event, operation, position)
}

func sortedKeys(row map[string]any) []string {
	keys := make([]string, 0, len(row))
	for key := range row {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func deleteRow(ctx context.Context, tx *sql.Tx, table string, definition tableDefinition, row map[string]any, position int) error {
	if len(row) != len(definition.primary) {
		return fmt.Errorf("%s delete must carry the complete primary key", table)
	}
	clauses := make([]string, len(definition.primary))
	values := make([]any, len(definition.primary))
	for index, column := range definition.primary {
		value, exists := row[column]
		if !exists {
			return fmt.Errorf("%s delete is missing primary key %q", table, column)
		}
		encoded, err := sqlValue(value)
		if err != nil {
			return err
		}
		clauses[index], values[index] = quoteIdentifier(column)+` = ?`, encoded
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM `+quoteIdentifier(table)+` WHERE `+strings.Join(clauses, ` AND `), values...); err != nil {
		return fmt.Errorf("delete %s row: %w", table, err)
	}
	return closeOpenVersion(ctx, tx, table, definition, values, position)
}

func sqlValue(value any) (any, error) {
	switch value := value.(type) {
	case nil, string, bool, int64, float64, []byte:
		if number, ok := value.(float64); ok && (math.IsInf(number, 0) || math.IsNaN(number)) {
			return nil, errors.New("number must be finite")
		}
		return value, nil
	case json.Number:
		if integer, err := value.Int64(); err == nil {
			if integer > 1<<53-1 || integer < -(1<<53-1) {
				return nil, errors.New("integer is outside JSON's safe range")
			}
			return integer, nil
		}
		number, err := value.Float64()
		if err != nil || math.IsInf(number, 0) || math.IsNaN(number) {
			return nil, errors.New("number must be finite")
		}
		return number, nil
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, errors.New("value is not JSON")
		}
		return string(encoded), nil
	}
}

func jsonValue(value any) any {
	switch value := value.(type) {
	case []byte:
		return string(value)
	default:
		return value
	}
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func quotedList(values []string) string {
	quoted := make([]string, len(values))
	for index, value := range values {
		quoted[index] = quoteIdentifier(value)
	}
	return strings.Join(quoted, ",")
}

func readFrontier(ctx context.Context, database *sql.DB, base Frontier) (Frontier, error) {
	var complete int
	if err := database.QueryRowContext(ctx, `SELECT interpreted_event, interpreted_position, complete, gap_event, gap_reason FROM gitseq_frontier WHERE singleton = 1`).Scan(
		&base.InterpretedEvent, &base.InterpretedPosition, &complete, &base.GapEvent, &base.GapReason,
	); err != nil {
		return Frontier{}, err
	}
	base.Complete = complete == 1
	return base, nil
}

// Frontier returns a copy of the projection's exact verified/interpreted
// boundary.
func (p *Projection) Frontier() Frontier { return p.frontier }

// Close releases the read pool. The database remains a disposable file.
func (p *Projection) Close() error {
	if p == nil || p.db == nil {
		return nil
	}
	return p.db.Close()
}

// Query executes one read-only SQL statement under byte and row bounds.
func (p *Projection) Query(ctx context.Context, query string, arguments ...any) (QueryResult, error) {
	if p == nil || p.db == nil {
		return QueryResult{}, errors.New("projection is closed")
	}
	return collectRows(ctx, p.db, p.frontier, query, arguments)
}

func collectRows(ctx context.Context, database *sql.DB, frontier Frontier, query string, arguments []any) (QueryResult, error) {
	if len(query) == 0 || len(query) > maxQueryBytes {
		return QueryResult{}, errors.New("query is empty or too large")
	}
	readOnly, err := statementReadOnly(ctx, database, query)
	if err != nil {
		return QueryResult{}, err
	}
	if !readOnly {
		return QueryResult{}, errors.New("query is not read-only")
	}
	rows, err := database.QueryContext(ctx, query, arguments...)
	if err != nil {
		return QueryResult{}, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return QueryResult{}, err
	}
	result := QueryResult{Frontier: frontier, Columns: columns}
	bytesUsed := 0
	for rows.Next() {
		if len(result.Rows) == maxQueryRows {
			result.Truncated = true
			break
		}
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for index := range values {
			pointers[index] = &values[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			return QueryResult{}, err
		}
		for index := range values {
			values[index] = jsonValue(values[index])
		}
		encoded, err := json.Marshal(values)
		if err != nil {
			return QueryResult{}, err
		}
		bytesUsed += len(encoded)
		if bytesUsed > maxQueryResult {
			result.Truncated = true
			break
		}
		result.Rows = append(result.Rows, values)
	}
	if err := rows.Err(); err != nil {
		return QueryResult{}, err
	}
	return result, nil
}

func statementReadOnly(ctx context.Context, database *sql.DB, query string) (bool, error) {
	connection, err := database.Conn(ctx)
	if err != nil {
		return false, err
	}
	defer connection.Close()
	readOnly := false
	err = connection.Raw(func(raw any) error {
		driverConnection, ok := raw.(sqlitedriver.Conn)
		if !ok {
			return errors.New("unexpected SQLite driver connection")
		}
		statement, tail, err := driverConnection.Raw().Prepare(query)
		if err != nil {
			return err
		}
		if statement == nil {
			return errors.New("query contains no statement")
		}
		defer statement.Close()
		if strings.TrimSpace(tail) != "" {
			return errors.New("query contains more than one statement")
		}
		readOnly = statement.ReadOnly()
		return nil
	})
	return readOnly, err
}
