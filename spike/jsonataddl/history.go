package jsonataddl

// Historical reads follow the note's "Historical reads without a SQL dialect"
// design: every writable application table has one hidden typed version
// relation maintained in the same transaction as the current-table change,
// and a historical read runs the application's unchanged SQL on a dedicated
// read-only connection whose temporary schema shadows each application table
// and view with the row versions visible at one selected position.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/ncruces/go-sqlite3"
	sqlitedriver "github.com/ncruces/go-sqlite3/driver"
)

const versionTablePrefix = "__gitseq_version_"

var viewDDLRE = regexp.MustCompile(`(?is)^CREATE\s+VIEW\s+(.+)$`)

// declaredViews returns the application's CREATE VIEW statements in
// declaration order, for re-creation inside a historical temporary schema.
func declaredViews(profile *Profile) []string {
	var views []string
	for _, statement := range profile.ddl {
		if viewDDLRE.MatchString(statement) {
			views = append(views, statement)
		}
	}
	return views
}

// versionTableDDL declares the hidden version relation for one writable
// application table: the table's typed columns plus the opening position, the
// optional closing position, and the source event and operation that opened
// the version. Declared types are re-emitted from SQLite's own parsed schema
// (table_xinfo), not from application text. The relation carries no
// uniqueness constraint: one event may replace the same row more than once,
// leaving zero-length versions that no position can observe.
func versionTableDDL(table string, definition tableDefinition) []string {
	columns := make([]string, 0, len(definition.columns)+4)
	for _, column := range definition.columns {
		declaration := quoteIdentifier(column.name)
		if column.declaredType != "" {
			declaration += " " + column.declaredType
		}
		if column.pk > 0 {
			declaration += " NOT NULL"
		}
		columns = append(columns, declaration)
	}
	columns = append(columns,
		`"__open" INTEGER NOT NULL`,
		`"__close" INTEGER`,
		`"__event" TEXT NOT NULL`,
		`"__op" TEXT NOT NULL CHECK ("__op" IN ('insert', 'upsert'))`,
	)
	keyed := make([]string, 0, len(definition.primary)+1)
	for _, name := range definition.primary {
		keyed = append(keyed, quoteIdentifier(name))
	}
	name := quoteIdentifier(versionTablePrefix + table)
	index := quoteIdentifier(versionTablePrefix + table + "__key")
	return []string{
		`CREATE TABLE ` + name + ` (` + strings.Join(columns, ", ") + `)`,
		`CREATE INDEX ` + index + ` ON ` + name + ` (` + strings.Join(append(keyed, `"__open"`), ",") + `)`,
	}
}

// primaryKeyValues extracts the primary-key values from a complete row value
// slice aligned with definition.columns.
func primaryKeyValues(definition tableDefinition, values []any) []any {
	keys := make([]any, 0, len(definition.primary))
	for _, name := range definition.primary {
		for index, column := range definition.columns {
			if column.name == name {
				keys = append(keys, values[index])
			}
		}
	}
	return keys
}

// closeOpenVersion ends the currently open version of one row, if any, at the
// given position. keyValues aligns with definition.primary.
func closeOpenVersion(ctx context.Context, tx *sql.Tx, table string, definition tableDefinition, keyValues []any, position int) error {
	clauses := make([]string, len(definition.primary))
	for index, name := range definition.primary {
		clauses[index] = quoteIdentifier(name) + ` = ?`
	}
	statement := `UPDATE ` + quoteIdentifier(versionTablePrefix+table) +
		` SET "__close" = ? WHERE ` + strings.Join(clauses, ` AND `) + ` AND "__close" IS NULL`
	if _, err := tx.ExecContext(ctx, statement, append([]any{position}, keyValues...)...); err != nil {
		return fmt.Errorf("close %s version: %w", table, err)
	}
	return nil
}

// recordVersionChange closes the replaced version and opens the new one in
// the same transaction as the current-table change it mirrors. values aligns
// with definition.columns.
func recordVersionChange(ctx context.Context, tx *sql.Tx, table string, definition tableDefinition, values []any, event, operation string, position int) error {
	if err := closeOpenVersion(ctx, tx, table, definition, primaryKeyValues(definition, values), position); err != nil {
		return err
	}
	columns := make([]string, 0, len(definition.columns)+4)
	marks := make([]string, 0, len(definition.columns)+4)
	for _, column := range definition.columns {
		columns = append(columns, quoteIdentifier(column.name))
		marks = append(marks, "?")
	}
	columns = append(columns, `"__open"`, `"__event"`, `"__op"`)
	marks = append(marks, "?", "?", "?")
	statement := `INSERT INTO ` + quoteIdentifier(versionTablePrefix+table) +
		` (` + strings.Join(columns, ",") + `) VALUES (` + strings.Join(marks, ",") + `)`
	arguments := append(append([]any{}, values...), position, event, operation)
	if _, err := tx.ExecContext(ctx, statement, arguments...); err != nil {
		return fmt.Errorf("open %s version: %w", table, err)
	}
	return nil
}

// QueryAt executes one read-only SQL statement against the projection as it
// stood after the event at the requested position (position 0 is the empty
// projection). The statement text is the same SQL a current read would use:
// the historical connection's temporary schema shadows every application
// table and declared view, so unqualified names resolve to the past rows.
// Schema-qualified names such as main.stock escape that shadowing by
// construction; the historical authorizer refuses them rather than let them
// silently read current rows.
func (p *Projection) QueryAt(ctx context.Context, position int, query string, arguments ...any) (QueryResult, error) {
	if p == nil || p.db == nil {
		return QueryResult{}, errors.New("projection is closed")
	}
	if position < 0 || position > p.frontier.InterpretedPosition {
		return QueryResult{}, fmt.Errorf("position %d is outside the interpreted prefix 0..%d", position, p.frontier.InterpretedPosition)
	}
	historical, err := openHistorical(p.path, p.tables, p.views, position)
	if err != nil {
		return QueryResult{}, err
	}
	defer historical.Close()
	return collectRows(ctx, historical, p.frontier, query, arguments)
}

func openHistorical(path string, tables map[string]tableDefinition, views []string, position int) (*sql.DB, error) {
	setup := []string{
		`CREATE TEMP TABLE "__gitseq_selected_position" (singleton INTEGER PRIMARY KEY CHECK (singleton = 1), position INTEGER NOT NULL) STRICT`,
		fmt.Sprintf(`INSERT INTO "__gitseq_selected_position" VALUES (1, %d)`, position),
	}
	names := make([]string, 0, len(tables))
	for name := range tables {
		names = append(names, name)
	}
	sort.Strings(names)
	visible := `"__open" <= (SELECT position FROM "__gitseq_selected_position") AND ("__close" IS NULL OR "__close" > (SELECT position FROM "__gitseq_selected_position"))`
	for _, name := range names {
		columns := make([]string, len(tables[name].columns))
		for index, column := range tables[name].columns {
			columns[index] = quoteIdentifier(column.name)
		}
		setup = append(setup, `CREATE TEMP VIEW `+quoteIdentifier(name)+` AS SELECT `+strings.Join(columns, ",")+
			` FROM main.`+quoteIdentifier(versionTablePrefix+name)+` WHERE `+visible)
	}
	for _, view := range views {
		match := viewDDLRE.FindStringSubmatch(view)
		if match == nil {
			return nil, fmt.Errorf("declared view is not CREATE VIEW: %.48q", view)
		}
		setup = append(setup, `CREATE TEMP VIEW `+match[1])
	}
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
		for _, statement := range setup {
			if err := connection.Exec(statement); err != nil {
				return fmt.Errorf("historical schema setup: %w", err)
			}
		}
		if err := connection.Exec(`PRAGMA query_only=ON`); err != nil {
			return err
		}
		return connection.SetAuthorizer(historicalAuthorizer)
	})
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(1)
	return database, nil
}

func historicalAuthorizer(action sqlite3.AuthorizerActionCode, name3rd, name4th, schema, inner string) sqlite3.AuthorizerReturnCode {
	switch action {
	case sqlite3.AUTH_SELECT:
		return sqlite3.AUTH_OK
	case sqlite3.AUTH_READ:
		if schema == "temp" {
			return sqlite3.AUTH_OK
		}
		if schema == "main" && strings.HasPrefix(name3rd, versionTablePrefix) {
			return sqlite3.AUTH_OK
		}
		// Current application tables, platform relations, and every
		// schema-qualified escape from the temporary shadow deny.
		return sqlite3.AUTH_DENY
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
