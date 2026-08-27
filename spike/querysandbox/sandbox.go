// Package querysandbox is an isolated experiment for the application-query
// boundary described in the JSONata/DDL implementation note. It is not wired
// into the resident or the sequencing kernel.
package querysandbox

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ncruces/go-sqlite3"
)

const (
	// MaxSQLBytes is checked before SQLite prepares application SQL.
	MaxSQLBytes = 8 << 10
	// MaxRows is the most complete rows returned by one query.
	MaxRows = 32
	// MaxResultBytes is the sum of SQLite values returned by one query. The
	// count excludes small Go/container overhead and is therefore not a heap
	// quota.
	MaxResultBytes = 8 << 10
	maxSQLiteValue = 64 << 10

	queryTimeout = 5 * time.Millisecond
)

var errQueryCancelled = errors.New("application query cancelled")

// Result is the bounded result of one application query.
type Result struct {
	Columns   []string
	Rows      [][]any
	Truncated bool
}

type options struct {
	installAuthorizer bool
	enforceDeadline   bool
	deadline          time.Duration
}

func defaultOptions() options {
	return options{
		installAuthorizer: true,
		enforceDeadline:   true,
		deadline:          queryTimeout,
	}
}

// Sandbox owns one serialized read-only SQLite connection. The direct binding
// is intentional: it lets the authorizer deny every application PRAGMA after
// the connection-only setup PRAGMAs have run.
type Sandbox struct {
	connection *sqlite3.Conn
	options    options
	mu         sync.Mutex
}

// Open opens an existing SQLite projection through the experimental
// application-query boundary.
func Open(path string) (*Sandbox, error) {
	return open(path, defaultOptions())
}

func open(path string, opts options) (*Sandbox, error) {
	if path == "" {
		return nil, errors.New("database path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	location := &url.URL{Scheme: "file", Path: absolute}
	connection, err := sqlite3.OpenFlags(location.String(), sqlite3.OPEN_READONLY|sqlite3.OPEN_URI)
	if err != nil {
		return nil, err
	}
	closeOnError := func(err error) (*Sandbox, error) {
		return nil, errors.Join(err, connection.Close())
	}
	if _, err := connection.Config(sqlite3.DBCONFIG_DEFENSIVE, true); err != nil {
		return closeOnError(err)
	}
	if _, err := connection.Config(sqlite3.DBCONFIG_TRUSTED_SCHEMA, false); err != nil {
		return closeOnError(err)
	}
	if _, err := connection.Config(sqlite3.DBCONFIG_ENABLE_LOAD_EXTENSION, false); err != nil {
		return closeOnError(err)
	}

	// These are engine limits, not replacements for the default-deny
	// authorizer or for the host result bounds below.
	connection.Limit(sqlite3.LIMIT_LENGTH, maxSQLiteValue)
	connection.Limit(sqlite3.LIMIT_SQL_LENGTH, MaxSQLBytes)
	connection.Limit(sqlite3.LIMIT_COLUMN, 32)
	connection.Limit(sqlite3.LIMIT_EXPR_DEPTH, 16)
	connection.Limit(sqlite3.LIMIT_COMPOUND_SELECT, 8)
	connection.Limit(sqlite3.LIMIT_VDBE_OP, 50_000)
	connection.Limit(sqlite3.LIMIT_FUNCTION_ARG, 16)
	connection.Limit(sqlite3.LIMIT_ATTACHED, 0)
	connection.Limit(sqlite3.LIMIT_VARIABLE_NUMBER, 32)
	connection.Limit(sqlite3.LIMIT_WORKER_THREADS, 0)

	// Run connection setup before installing the authorizer. This keeps every
	// PRAGMA, including a query_only read, out of the application language.
	if err := connection.Exec(`PRAGMA query_only=ON`); err != nil {
		return closeOnError(err)
	}
	if opts.installAuthorizer {
		if err := connection.SetAuthorizer(applicationAuthorizer); err != nil {
			return closeOnError(err)
		}
	}
	if opts.deadline <= 0 {
		opts.deadline = queryTimeout
	}
	return &Sandbox{connection: connection, options: opts}, nil
}

// Close releases the experimental reader.
func (s *Sandbox) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.connection == nil {
		return nil
	}
	err := s.connection.Close()
	s.connection = nil
	return err
}

// Query prepares and executes exactly one application SELECT. SQLite runtime
// limits bound statement shape; a context deadline interrupts execution; and
// host-side row and value-byte caps bound the returned result.
func (s *Sandbox) Query(ctx context.Context, query string) (Result, error) {
	if s == nil {
		return Result{}, errors.New("query sandbox is closed")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.connection == nil {
		return Result{}, errors.New("query sandbox is closed")
	}
	if len(query) == 0 || len(query) > MaxSQLBytes {
		return Result{}, fmt.Errorf("application SQL must contain 1 to %d bytes", MaxSQLBytes)
	}

	queryContext := ctx
	cancel := func() {}
	if s.options.enforceDeadline {
		queryContext, cancel = context.WithTimeout(ctx, s.options.deadline)
	}
	defer cancel()
	oldInterrupt := s.connection.SetInterrupt(queryContext)
	defer s.connection.SetInterrupt(oldInterrupt)

	statement, tail, err := s.connection.Prepare(query)
	if err != nil {
		return Result{}, queryError(queryContext, err)
	}
	if statement == nil {
		return Result{}, errors.New("application SQL contains no statement")
	}
	defer statement.Close()
	if strings.TrimSpace(tail) != "" {
		return Result{}, errors.New("application SQL contains more than one statement")
	}
	if !statement.ReadOnly() {
		return Result{}, errors.New("application SQL is not read-only")
	}

	columns := make([]string, statement.ColumnCount())
	for index := range columns {
		columns[index] = statement.ColumnName(index)
	}
	result := Result{Columns: columns}
	bytesUsed := 0
	for statement.Step() {
		if len(result.Rows) == MaxRows {
			result.Truncated = true
			break
		}
		row := make([]any, len(columns))
		rowBytes := 0
		for index := range row {
			value, size, err := columnValue(statement, index)
			if err != nil {
				return Result{}, err
			}
			row[index] = value
			rowBytes += size
		}
		if bytesUsed+rowBytes > MaxResultBytes {
			result.Truncated = true
			break
		}
		bytesUsed += rowBytes
		result.Rows = append(result.Rows, row)
	}
	if err := statement.Err(); err != nil {
		return Result{}, queryError(queryContext, err)
	}
	return result, nil
}

func queryError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("%w: %v", errQueryCancelled, ctx.Err())
	}
	return err
}

func columnValue(statement *sqlite3.Stmt, column int) (any, int, error) {
	return columnValueOfType(statement, column, statement.ColumnType(column))
}

func columnValueOfType(statement *sqlite3.Stmt, column int, datatype sqlite3.Datatype) (any, int, error) {
	switch datatype {
	case sqlite3.INTEGER:
		return statement.ColumnInt64(column), 8, nil
	case sqlite3.FLOAT:
		return statement.ColumnFloat(column), 8, nil
	case sqlite3.TEXT:
		value := statement.ColumnText(column)
		return value, len(value), nil
	case sqlite3.BLOB:
		value := statement.ColumnBlob(column, nil)
		return value, len(value), nil
	case sqlite3.NULL:
		return nil, 0, nil
	default:
		return nil, 0, fmt.Errorf("unexpected SQLite datatype %d", datatype)
	}
}

func applicationAuthorizer(action sqlite3.AuthorizerActionCode, name3rd, name4th, schema, _ string) sqlite3.AuthorizerReturnCode {
	switch action {
	case sqlite3.AUTH_SELECT, sqlite3.AUTH_RECURSIVE:
		return sqlite3.AUTH_OK
	case sqlite3.AUTH_READ:
		// The spike exposes one application table and one public view. Platform,
		// physical-schema, temporary and attached relations are not public.
		if schema == "main" && (name3rd == "inventory" || name3rd == "inventory_summary") {
			return sqlite3.AUTH_OK
		}
		return sqlite3.AUTH_DENY
	case sqlite3.AUTH_FUNCTION:
		switch strings.ToLower(name4th) {
		case "avg", "coalesce", "count", "ifnull", "max", "min", "sum", "total":
			return sqlite3.AUTH_OK
		default:
			return sqlite3.AUTH_DENY
		}
	default:
		// Writes, all DDL, PRAGMAs, transaction control, ATTACH/DETACH,
		// virtual tables, savepoints and unknown future action codes deny.
		return sqlite3.AUTH_DENY
	}
}
