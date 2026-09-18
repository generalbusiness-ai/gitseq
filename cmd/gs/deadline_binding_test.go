package main

import (
	"go/ast"
	"go/parser"
	gotoken "go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Registering the deadline flag and binding it are two acts, and only the first
// is visible at runtime. A command that resolved the deadline and then dropped
// the context it returned would refuse an unreadable value exactly as it does
// now, and submit under the old wait: every test about refusals stays green
// while the setting does nothing.
//
// So the binding is read out of the source, the way the CLI surface gate reads
// the flags. The rule is one line and exact: a command that calls actFlags
// assigns the result of withSubmitDeadline back to ctx. Nothing else in this
// package may call actFlags without doing so, which is what makes a thirteenth
// appending command inherit the check instead of needing to remember it.
func TestEveryCommandRegisteringTheDeadlineBindsIt(t *testing.T) {
	bound, registered := map[string]bool{}, map[string]bool{}
	fileSet := gotoken.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fileSet, filepath.Join(".", name), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				if call, ok := node.(*ast.CallExpr); ok && calls(call, "actFlags") {
					registered[function.Name.Name] = true
				}
				assignment, ok := node.(*ast.AssignStmt)
				if !ok || len(assignment.Rhs) != 1 {
					return true
				}
				call, ok := assignment.Rhs[0].(*ast.CallExpr)
				if !ok || !calls(call, "withSubmitDeadline") {
					return true
				}
				// The first result must land back in ctx. Anything else —
				// including the blank identifier — reads the deadline and
				// throws away the only thing that carries it.
				if target, ok := assignment.Lhs[0].(*ast.Ident); ok && target.Name == "ctx" {
					bound[function.Name.Name] = true
				}
				return true
			})
		}
	}
	if len(registered) == 0 {
		t.Fatal("no command registers the deadline flag; the reader no longer matches the source")
	}
	for command := range registered {
		if !bound[command] {
			t.Errorf("%s registers --deadline but never binds it: assign withSubmitDeadline's context back to ctx, "+
				"or its submissions keep the default wait whatever the author asked for", command)
		}
	}
	// And the reverse: a function that binds without registering would be
	// carrying a deadline no author can set.
	for command := range bound {
		if !registered[command] {
			t.Errorf("%s binds a deadline it never registered a flag for", command)
		}
	}
}

func calls(call *ast.CallExpr, name string) bool {
	identifier, ok := call.Fun.(*ast.Ident)
	return ok && identifier.Name == name
}
