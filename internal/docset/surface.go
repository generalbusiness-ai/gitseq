package docset

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
)

// Subcommand is one `gs` subcommand and the flags it accepts.
type Subcommand struct {
	Name  string
	Flags []string
}

// Tool is one MCP tool and the argument names its input schema declares.
type Tool struct {
	Name      string
	Arguments []string
	Required  []string
	Enums     []string
}

// The command surface is read out of the implementation rather than from a
// list kept beside it. A hand-maintained list would have to be updated by the
// same person who forgot to update the documentation, so it would not catch
// the mistake the gate exists to catch.
const (
	gsMain  = "cmd/gs/main.go"
	mcpMain = "cmd/gitseq-mcp/main.go"
)

// CLISurface returns every `gs` subcommand, with its flags, in name order.
func CLISurface(root string) ([]Subcommand, error) {
	file, err := parseFile(filepath.Join(root, gsMain))
	if err != nil {
		return nil, err
	}
	shared, err := collectFlags(file, "flags", map[string]bool{})
	if err != nil {
		return nil, fmt.Errorf("%s: shared flags: %w", gsMain, err)
	}
	if len(shared) == 0 {
		return nil, fmt.Errorf("%s: no shared flags found; the extractor no longer matches the source", gsMain)
	}
	dispatch, err := dispatchTable(file)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", gsMain, err)
	}
	commands := make([]Subcommand, 0, len(dispatch))
	for name, function := range dispatch {
		own, err := flagsIn(file, function)
		if err != nil {
			return nil, fmt.Errorf("%s: %s: %w", gsMain, name, err)
		}
		flags := append(append([]string(nil), shared...), own...)
		sort.Strings(flags)
		commands = append(commands, Subcommand{Name: name, Flags: flags})
	}
	sort.Slice(commands, func(i, j int) bool { return commands[i].Name < commands[j].Name })
	if len(commands) == 0 {
		return nil, fmt.Errorf("%s: no subcommands found; the extractor no longer matches the source", gsMain)
	}
	return commands, nil
}

// MCPSurface returns every MCP tool the adapter advertises, in listed order.
func MCPSurface(root string) ([]Tool, error) {
	file, err := parseFile(filepath.Join(root, mcpMain))
	if err != nil {
		return nil, err
	}
	function := functionNamed(file, "tools")
	if function == nil {
		return nil, fmt.Errorf("%s: func tools() not found", mcpMain)
	}
	var tools []Tool
	var walkErr error
	ast.Inspect(function, func(node ast.Node) bool {
		literal, ok := node.(*ast.CompositeLit)
		if !ok {
			return true
		}
		tool := Tool{}
		found := false
		for _, element := range literal.Elts {
			pair, ok := element.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := stringValue(pair.Key)
			if !ok {
				continue
			}
			switch key {
			case "name":
				value, ok := stringValue(pair.Value)
				if !ok {
					walkErr = errors.New("tool name is not a string literal")
					return false
				}
				tool.Name = value
				found = true
			case "inputSchema":
				arguments, required, ok := schemaFields(function, pair.Value)
				if !ok {
					walkErr = fmt.Errorf("tool input schema is not an object(...) call over resolvable properties")
					return false
				}
				tool.Arguments, tool.Required = arguments, required
				tool.Enums = enumValues(pair.Value)
			}
		}
		if found {
			tools = append(tools, tool)
			return false
		}
		return true
	})
	if walkErr != nil {
		return nil, fmt.Errorf("%s: %w", mcpMain, walkErr)
	}
	if len(tools) == 0 {
		return nil, fmt.Errorf("%s: no tools found; the extractor no longer matches the source", mcpMain)
	}
	return tools, nil
}

func parseFile(path string) (*ast.File, error) {
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	return file, nil
}

func functionNamed(file *ast.File, name string) *ast.FuncDecl {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Recv == nil && function.Name.Name == name {
			return function
		}
	}
	return nil
}

// dispatchTable reads the `switch os.Args[1]` in main: each case string is a
// subcommand and the function it calls holds that subcommand's flags.
func dispatchTable(file *ast.File) (map[string]string, error) {
	main := functionNamed(file, "main")
	if main == nil {
		return nil, errors.New("func main() not found")
	}
	table := map[string]string{}
	ast.Inspect(main, func(node ast.Node) bool {
		clause, ok := node.(*ast.CaseClause)
		if !ok {
			return true
		}
		var names []string
		for _, expression := range clause.List {
			if value, ok := stringValue(expression); ok {
				names = append(names, value)
			}
		}
		if len(names) == 0 {
			return true
		}
		called := ""
		ast.Inspect(clause, func(inner ast.Node) bool {
			call, ok := inner.(*ast.CallExpr)
			if !ok {
				return true
			}
			if identifier, ok := call.Fun.(*ast.Ident); ok {
				called = identifier.Name
				return false
			}
			return true
		})
		if called == "" {
			return true
		}
		for _, name := range names {
			table[name] = called
		}
		return true
	})
	if len(table) == 0 {
		return nil, errors.New("no subcommand cases found")
	}
	return table, nil
}

// flagsIn collects the flag names a function registers on its flag set,
// following calls to other functions in the same file. `gs review` registers
// its flags in a delegate so a test can inject a validator, and a gate that
// stopped at the entry point would report that subcommand as flagless.
func flagsIn(file *ast.File, name string) ([]string, error) {
	return collectFlags(file, name, map[string]bool{"flags": true})
}

func collectFlags(file *ast.File, name string, seen map[string]bool) ([]string, error) {
	if seen[name] {
		return nil, nil
	}
	seen[name] = true
	function := functionNamed(file, name)
	if function == nil {
		return nil, fmt.Errorf("func %s not found", name)
	}
	var flags []string
	var err error
	var delegates []string
	ast.Inspect(function, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		identifier, ok := call.Fun.(*ast.Ident)
		if ok && !seen[identifier.Name] && functionNamed(file, identifier.Name) != nil {
			delegates = append(delegates, identifier.Name)
		}
		return true
	})
	ast.Inspect(function, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		receiver, ok := selector.X.(*ast.Ident)
		if !ok || receiver.Name != "set" {
			return true
		}
		position := -1
		switch selector.Sel.Name {
		case "String", "Bool", "Int", "Int64", "Uint", "Uint64", "Float64", "Duration":
			position = 0
		case "Var", "Func", "BoolFunc", "TextVar":
			position = 1
		default:
			return true
		}
		if len(call.Args) <= position {
			err = fmt.Errorf("%s: flag registration has too few arguments", name)
			return false
		}
		value, ok := stringValue(call.Args[position])
		if !ok {
			err = fmt.Errorf("%s: flag name is not a string literal", name)
			return false
		}
		flags = append(flags, value)
		return true
	})
	if err != nil {
		return nil, err
	}
	for _, delegate := range delegates {
		inherited, err := collectFlags(file, delegate, seen)
		if err != nil {
			return nil, err
		}
		flags = append(flags, inherited...)
	}
	return flags, nil
}

// schemaFields reads argument names out of an `object(properties, required...)`
// call, which is how the adapter builds every tool's input schema. `scope` is
// the function the call sits in, so the helpers it uses can be resolved.
//
// Failing to resolve the properties is reported rather than read as an empty
// schema. An adapter that wraps its per-tool fields in a new helper would
// otherwise look to this gate like a set of tools that take no arguments, and
// every reference page would be required to say so.
// enumValues collects every string handed to an enum(...) call anywhere in a
// schema expression. A documentation page for the tool must name each value
// exactly, so a page cannot teach a spelling the schema refuses.
func enumValues(expression ast.Expr) []string {
	var values []string
	ast.Inspect(expression, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		identifier, ok := call.Fun.(*ast.Ident)
		if !ok || identifier.Name != "enum" {
			return true
		}
		for _, argument := range call.Args {
			if value, ok := stringValue(argument); ok {
				values = append(values, value)
			}
		}
		return true
	})
	sort.Strings(values)
	return values
}

func schemaFields(scope ast.Node, expression ast.Expr) ([]string, []string, bool) {
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return nil, nil, false
	}
	identifier, ok := call.Fun.(*ast.Ident)
	if !ok || identifier.Name != "object" {
		return nil, nil, false
	}
	if len(call.Args) == 0 {
		return nil, nil, false
	}
	arguments, ok := propertyNames(scope, call.Args[0])
	if !ok {
		return nil, nil, false
	}
	var required []string
	for _, argument := range call.Args[1:] {
		if value, ok := stringValue(argument); ok {
			required = append(required, value)
		}
	}
	sort.Strings(arguments)
	sort.Strings(required)
	return arguments, required, true
}

// propertyNames resolves a schema's properties expression to the argument
// names it declares. A `map[string]any` literal contributes its own keys,
// `nil` contributes none, and a call to a local helper contributes the shared
// keys the helper's body names plus whatever it was handed.
func propertyNames(scope ast.Node, expression ast.Expr) ([]string, bool) {
	switch value := expression.(type) {
	case *ast.CompositeLit:
		return literalKeys(value)
	case *ast.Ident:
		return nil, value.Name == "nil"
	case *ast.CallExpr:
		helper, ok := value.Fun.(*ast.Ident)
		if !ok || len(value.Args) != 1 {
			return nil, false
		}
		body := closureNamed(scope, helper.Name)
		if body == nil {
			return nil, false
		}
		shared, ok := sharedKeys(body)
		if !ok {
			return nil, false
		}
		own, ok := propertyNames(scope, value.Args[0])
		if !ok {
			return nil, false
		}
		return append(shared, own...), true
	}
	return nil, false
}

func literalKeys(literal *ast.CompositeLit) ([]string, bool) {
	var names []string
	for _, element := range literal.Elts {
		pair, ok := element.(*ast.KeyValueExpr)
		if !ok {
			return nil, false
		}
		key, ok := stringValue(pair.Key)
		if !ok {
			return nil, false
		}
		names = append(names, key)
	}
	return names, true
}

// closureNamed finds a `name := func(...)` binding inside a function.
func closureNamed(scope ast.Node, name string) *ast.FuncLit {
	var found *ast.FuncLit
	ast.Inspect(scope, func(node ast.Node) bool {
		assignment, ok := node.(*ast.AssignStmt)
		if !ok || len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 {
			return true
		}
		identifier, ok := assignment.Lhs[0].(*ast.Ident)
		if !ok || identifier.Name != name {
			return true
		}
		if literal, ok := assignment.Rhs[0].(*ast.FuncLit); ok {
			found = literal
			return false
		}
		return true
	})
	return found
}

// sharedKeys reads the property names a helper adds to every schema. The
// helper must name them in exactly one `map[string]any` literal, so that a
// helper doing something this gate cannot follow fails rather than reporting
// nothing.
func sharedKeys(body *ast.FuncLit) ([]string, bool) {
	var keys []string
	literals := 0
	var bad bool
	ast.Inspect(body, func(node ast.Node) bool {
		literal, ok := node.(*ast.CompositeLit)
		if !ok || !isStringAnyMap(literal.Type) {
			return true
		}
		literals++
		names, ok := literalKeys(literal)
		if !ok {
			bad = true
			return false
		}
		keys = names
		return true
	})
	if bad || literals != 1 {
		return nil, false
	}
	return keys, true
}

func isStringAnyMap(expression ast.Expr) bool {
	mapType, ok := expression.(*ast.MapType)
	if !ok {
		return false
	}
	key, ok := mapType.Key.(*ast.Ident)
	if !ok || key.Name != "string" {
		return false
	}
	if value, ok := mapType.Value.(*ast.Ident); ok {
		return value.Name == "any"
	}
	_, ok = mapType.Value.(*ast.InterfaceType)
	return ok
}

func stringValue(expression ast.Expr) (string, bool) {
	literal, ok := expression.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(literal.Value)
	if err != nil {
		return "", false
	}
	return value, true
}
