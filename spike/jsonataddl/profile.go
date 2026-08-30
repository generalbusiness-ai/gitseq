// Package jsonataddl is a deliberately narrow spike of the application
// interface described in notes/2026-08-26-jsonata-ddl-application-interface.md.
// It compiles a two-file SQL-and-JSONata application, replays verified host
// records into a disposable SQLite projection, and exposes that projection to
// bounded read-only SQL.
package jsonataddl

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/generalbusiness-ai/gitseq/host"
	jsonata "github.com/jsonata-go/jsonata/v206"
)

const (
	maxApplicationSQL  = 64 << 10
	maxProgramBytes    = 64 << 10
	maxPayloadBytes    = 16 << 10
	maxEvaluationInput = 24 << 10
	maxOutputBytes     = 64 << 10
	maxRowChanges      = 256
	maxFacts           = 64
	maxQueryBytes      = 16 << 10
	maxQueryRows       = 256
	maxQueryResult     = 256 << 10
	maxFoldReadSQL     = 4 << 10
	maxFoldReadVDBE    = 1024
	maxFoldSQLiteValue = 128 << 10
)

// InventoryApplication is the host identity of the embedded spike fixture.
// The fold version binds the exact evaluator, SQL profile, and limits here.
var InventoryApplication = host.Application{
	Name:        "jsonata-ddl-inventory-spike",
	FoldVersion: "jsonata-v206-sqlite-spike@0",
	SourceURL:   "https://github.com/generalbusiness-ai/gitseq.git",
}

//go:embed inventory/application.sql inventory/folds/inventory.jsonata inventory/folds/cancel.jsonata
var inventoryFiles embed.FS

var (
	identifier     = `[A-Za-z_][A-Za-z0-9_]*`
	eventRE        = regexp.MustCompile(`(?is)^CREATE\s+EVENT\s+(` + identifier + `)\s*\((.*)\)$`)
	tableRE        = regexp.MustCompile(`(?is)^CREATE\s+TABLE\s+(` + identifier + `)\b`)
	allowedDDL     = regexp.MustCompile(`(?is)^CREATE\s+(?:TABLE|INDEX|VIEW)\b`)
	foldRE         = regexp.MustCompile(`(?is)^CREATE\s+FOLD\s+(` + identifier + `)\s+ON\s+(` + identifier + `)\s+READ\s+(` + identifier + `)\s+(ONE|OPTIONAL\s+ONE)\s+AS\s+(.+?)\s+USING\s+'([^']+)'\s+WRITES\s+(.+)$`)
	ambientSQL     = regexp.MustCompile(`(?i)\b(?:random|randomblob|current_date|current_time|current_timestamp|load_extension)\b|\b(?:date|time|datetime|julianday|unixepoch|strftime)\s*\(`)
	ambientJSONata = regexp.MustCompile(`(?i)\$(?:now|millis|random|shuffle|eval)\b`)
	foldReadRE     = regexp.MustCompile(`(?is)^SELECT\s+(` + identifier + `(?:\s*,\s*` + identifier + `)*)\s+FROM\s+(` + identifier + `)\s+WHERE\s+(` + identifier + `)\s*=\s*:event\.(` + identifier + `)$`)
)

type eventDefinition struct {
	name       string
	columnsSQL string
	columns    []string
}

type readDefinition struct {
	name        string
	cardinality string
	query       string
	parameter   string
	table       string
	columns     []string
	whereColumn string
}

type foldDefinition struct {
	name       string
	event      string
	read       readDefinition
	program    *jsonata.Expression
	programRaw []byte
	writes     map[string]bool
}

// Profile is one compiled application package. Its internals are intentionally
// private: the spike proves one loading path rather than establishing a public
// platform API.
type Profile struct {
	Application  host.Application
	SchemaDigest string
	ddl          []string
	events       map[string]eventDefinition
	folds        map[string]foldDefinition
}

// LoadInventory compiles the embedded two-file inventory application.
func LoadInventory() (*Profile, error) {
	return Load(inventoryFiles, "inventory", InventoryApplication)
}

// Load compiles application.sql and every JSONata path it binds. The grammar
// is purposefully the smallest one needed by the spike: one ONE or OPTIONAL
// ONE read per fold. Unsupported syntax is refused rather than guessed.
func Load(files fs.FS, root string, application host.Application) (*Profile, error) {
	if application.Name == "" || application.FoldVersion == "" {
		return nil, errors.New("application name and fold version are required")
	}
	sqlPath := path.Join(root, "application.sql")
	source, err := fs.ReadFile(files, sqlPath)
	if err != nil {
		return nil, fmt.Errorf("read application SQL: %w", err)
	}
	if len(source) == 0 || len(source) > maxApplicationSQL {
		return nil, errors.New("application SQL is empty or too large")
	}
	statements, err := splitStatements(string(source))
	if err != nil {
		return nil, err
	}
	profile := &Profile{
		Application: application,
		events:      make(map[string]eventDefinition),
		folds:       make(map[string]foldDefinition),
	}
	tables := make(map[string]bool)
	programs := make(map[string][]byte)
	for _, statement := range statements {
		switch {
		case eventRE.MatchString(statement):
			match := eventRE.FindStringSubmatch(statement)
			name := match[1]
			if _, exists := profile.events[name]; exists {
				return nil, fmt.Errorf("event %q is declared twice", name)
			}
			columns, err := declaredColumns(match[2])
			if err != nil {
				return nil, fmt.Errorf("event %q: %w", name, err)
			}
			profile.events[name] = eventDefinition{name: name, columnsSQL: match[2], columns: columns}
		case foldRE.MatchString(statement):
			match := foldRE.FindStringSubmatch(statement)
			name, event := match[1], match[2]
			if _, exists := profile.folds[event]; exists {
				return nil, fmt.Errorf("event %q has more than one fold", event)
			}
			read, err := compileRead(match[5])
			if err != nil {
				return nil, fmt.Errorf("fold %q: %w", name, err)
			}
			read.name = match[3]
			read.cardinality = strings.Join(strings.Fields(strings.ToUpper(match[4])), " ")
			programPath := path.Clean(path.Join(root, match[6]))
			if programPath == root || !strings.HasPrefix(programPath, strings.TrimSuffix(root, "/")+"/") {
				return nil, fmt.Errorf("fold %q program escapes the application directory", name)
			}
			program, err := fs.ReadFile(files, programPath)
			if err != nil {
				return nil, fmt.Errorf("read fold %q program: %w", name, err)
			}
			if len(program) == 0 || len(program) > maxProgramBytes {
				return nil, fmt.Errorf("fold %q program is empty or too large", name)
			}
			if ambientJSONata.Match(program) {
				return nil, fmt.Errorf("fold %q uses an ambient or dynamic JSONata function", name)
			}
			expression, err := jsonata.Compile(string(program), false)
			if err != nil {
				return nil, fmt.Errorf("compile fold %q: %w", name, err)
			}
			expression.SetMaxDepth(64)
			expression.SetMaxRange(4096)
			writes, err := identifierSet(match[7])
			if err != nil {
				return nil, fmt.Errorf("fold %q writes: %w", name, err)
			}
			profile.folds[event] = foldDefinition{
				name: name, event: event,
				read:    read,
				program: expression, programRaw: program, writes: writes,
			}
			programs[programPath] = program
		case allowedDDL.MatchString(statement):
			if ambientSQL.MatchString(statement) {
				return nil, errors.New("application DDL uses an ambient or loadable SQL function")
			}
			profile.ddl = append(profile.ddl, statement)
			if match := tableRE.FindStringSubmatch(statement); match != nil {
				tables[match[1]] = true
			}
		default:
			return nil, fmt.Errorf("statement is outside the spike DDL profile: %.48q", statement)
		}
	}
	if len(profile.events) == 0 || len(profile.folds) == 0 || len(profile.ddl) == 0 {
		return nil, errors.New("application needs events, folds, and relational DDL")
	}
	for event, fold := range profile.folds {
		declaration, exists := profile.events[event]
		if !exists {
			return nil, fmt.Errorf("fold %q names undeclared event %q", fold.name, event)
		}
		if !contains(declaration.columns, fold.read.parameter) {
			return nil, fmt.Errorf("fold %q read names undeclared event field %q", fold.name, fold.read.parameter)
		}
		for table := range fold.writes {
			if !tables[table] {
				return nil, fmt.Errorf("fold %q writes undeclared table %q", fold.name, table)
			}
		}
	}
	profile.SchemaDigest = digestApplication(source, programs)
	return profile, nil
}

func compileRead(query string) (readDefinition, error) {
	query = strings.TrimSpace(query)
	if len(query) > maxFoldReadSQL {
		return readDefinition{}, errors.New("fold read is too large")
	}
	match := foldReadRE.FindStringSubmatch(query)
	if match == nil {
		return readDefinition{}, errors.New("fold read must be a primary-key equality SELECT of named columns")
	}
	columns := regexp.MustCompile(`\s*,\s*`).Split(match[1], -1)
	return readDefinition{
		query:       `SELECT ` + quotedList(columns) + ` FROM ` + quoteIdentifier(match[2]) + ` WHERE ` + quoteIdentifier(match[3]) + ` = ?`,
		parameter:   match[4],
		table:       match[2],
		columns:     columns,
		whereColumn: match[3],
	}, nil
}

func declaredColumns(body string) ([]string, error) {
	parts, err := splitComma(body)
	if err != nil {
		return nil, err
	}
	columns := make([]string, 0, len(parts))
	seen := make(map[string]bool)
	for _, part := range parts {
		fields := strings.Fields(part)
		if len(fields) < 2 || !regexp.MustCompile(`^`+identifier+`$`).MatchString(fields[0]) {
			return nil, fmt.Errorf("unsupported event column declaration %q", part)
		}
		name := fields[0]
		if seen[name] {
			return nil, fmt.Errorf("column %q is declared twice", name)
		}
		seen[name] = true
		columns = append(columns, name)
	}
	if len(columns) == 0 {
		return nil, errors.New("event has no columns")
	}
	return columns, nil
}

func identifierSet(value string) (map[string]bool, error) {
	result := make(map[string]bool)
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if !regexp.MustCompile(`^` + identifier + `$`).MatchString(item) {
			return nil, fmt.Errorf("invalid identifier %q", item)
		}
		if result[item] {
			return nil, fmt.Errorf("duplicate identifier %q", item)
		}
		result[item] = true
	}
	if len(result) == 0 {
		return nil, errors.New("empty table list")
	}
	return result, nil
}

func digestApplication(sql []byte, programs map[string][]byte) string {
	hash := sha256.New()
	hash.Write([]byte("application.sql\x00"))
	hash.Write(sql)
	paths := make([]string, 0, len(programs))
	for name := range programs {
		paths = append(paths, name)
	}
	sort.Strings(paths)
	for _, name := range paths {
		hash.Write([]byte("\x00" + name + "\x00"))
		hash.Write(programs[name])
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func splitStatements(source string) ([]string, error) {
	var statements []string
	var current strings.Builder
	quote := rune(0)
	lineComment, blockComment := false, false
	runes := []rune(source)
	for index := 0; index < len(runes); index++ {
		char := runes[index]
		next := rune(0)
		if index+1 < len(runes) {
			next = runes[index+1]
		}
		switch {
		case lineComment:
			if char == '\n' {
				lineComment = false
				current.WriteRune(char)
			}
		case blockComment:
			if char == '*' && next == '/' {
				blockComment = false
				index++
				current.WriteByte(' ')
			}
		case quote != 0:
			current.WriteRune(char)
			if char == quote {
				if next == quote {
					current.WriteRune(next)
					index++
				} else {
					quote = 0
				}
			}
		case char == '-' && next == '-':
			lineComment = true
			index++
		case char == '/' && next == '*':
			blockComment = true
			index++
		case char == '\'' || char == '"':
			quote = char
			current.WriteRune(char)
		case char == ';':
			if statement := strings.TrimSpace(current.String()); statement != "" {
				statements = append(statements, statement)
			}
			current.Reset()
		default:
			current.WriteRune(char)
		}
	}
	if quote != 0 || blockComment {
		return nil, errors.New("application SQL has an unterminated quote or comment")
	}
	if statement := strings.TrimSpace(current.String()); statement != "" {
		return nil, errors.New("application SQL statement is missing its semicolon")
	}
	return statements, nil
}

func splitComma(source string) ([]string, error) {
	var parts []string
	var current strings.Builder
	depth := 0
	quote := rune(0)
	for _, char := range source {
		switch {
		case quote != 0:
			current.WriteRune(char)
			if char == quote {
				quote = 0
			}
		case char == '\'' || char == '"':
			quote = char
			current.WriteRune(char)
		case char == '(':
			depth++
			current.WriteRune(char)
		case char == ')':
			depth--
			if depth < 0 {
				return nil, errors.New("unbalanced parentheses")
			}
			current.WriteRune(char)
		case char == ',' && depth == 0:
			parts = append(parts, strings.TrimSpace(current.String()))
			current.Reset()
		default:
			current.WriteRune(char)
		}
	}
	if quote != 0 || depth != 0 {
		return nil, errors.New("unbalanced event declaration")
	}
	parts = append(parts, strings.TrimSpace(current.String()))
	for _, part := range parts {
		if part == "" || strings.IndexFunc(part, unicode.IsControl) >= 0 {
			return nil, errors.New("empty or invalid declaration item")
		}
	}
	return parts, nil
}
