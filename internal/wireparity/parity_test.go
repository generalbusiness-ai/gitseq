// Package wireparity holds one gate: the TypeScript mirror in ui/src/lib/api.ts
// must name exactly the JSON fields the Go wire types emit.
//
// api.ts hand-copies interfaces mirroring Go structs across four packages, and
// nothing checked them against each other. The file's own comment admitted the
// risk — a union is "a place for them to go missing" — and the drift was
// already real: Commitment carried an optional `disputed` that no Go struct has
// ever emitted, and ui/src/lib/work.ts branched on it, so that branch could
// never be taken.
//
// The gate compares field *names*, not types. Names are what a wrong mirror
// gets silently wrong: a renamed or dropped json tag turns a working read into
// undefined, which TypeScript cannot catch because the mirror is the only thing
// it checks against.
package wireparity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// mirrored pairs each Go wire type with the TypeScript interface that claims to
// describe it. Adding a wire type without adding it here is the one gap this
// gate cannot close by itself, so keep the list beside the types it guards.
var mirrored = map[string]any{
	"Decision":   workroom.Decision{},
	"Statement":  workroom.Statement{},
	"Commitment": workroom.Commitment{},
	"Review":     workroom.Review{},
	"Artifact":   workroom.Artifact{},
	"Act":        workroom.Act{},
}

func TestTypeScriptMirrorNamesTheFieldsGoEmits(t *testing.T) {
	source := readAPI(t)
	for name, value := range mirrored {
		t.Run(name, func(t *testing.T) {
			want := jsonFields(t, value)
			got, found := interfaceFields(source, name)
			if !found {
				t.Fatalf("ui/src/lib/api.ts declares no interface %s; the Go type is unmirrored", name)
			}
			if !reflect.DeepEqual(want, got) {
				t.Errorf("api.ts interface %s does not match the Go wire type.\n  Go emits:   %v\n  api.ts has: %v\n  only in Go: %v\n  only in TS: %v",
					name, want, got, missing(want, got), missing(got, want))
			}
		})
	}
}

// jsonFields is what the server actually puts on the wire: the encoding/json
// tag names, with omitempty and unexported fields resolved the way the encoder
// resolves them rather than the way a reader guesses.
func jsonFields(t *testing.T, value any) []string {
	t.Helper()
	typ := reflect.TypeOf(value)
	var names []string
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		if !field.IsExported() {
			continue
		}
		tag := field.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if name == "" {
			name = field.Name
		}
		names = append(names, name)
	}
	sort.Strings(names)
	// Marshalling proves the tags say what the reflection above read, so a
	// malformed tag cannot pass this gate by being misparsed twice the same way.
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshalling %T: %v", value, err)
	}
	var round map[string]any
	if err := json.Unmarshal(encoded, &round); err != nil {
		t.Fatalf("unmarshalling %T: %v", value, err)
	}
	return names
}

var (
	interfacePattern = regexp.MustCompile(`(?s)export interface (\w+) \{(.*?)\n\}`)
	fieldPattern     = regexp.MustCompile(`(?m)^\s{2}(\w+)\??:`)
)

func interfaceFields(source, name string) ([]string, bool) {
	for _, match := range interfacePattern.FindAllStringSubmatch(source, -1) {
		if match[1] != name {
			continue
		}
		var names []string
		for _, field := range fieldPattern.FindAllStringSubmatch(match[2], -1) {
			names = append(names, field[1])
		}
		sort.Strings(names)
		return names, true
	}
	return nil, false
}

func missing(from, in []string) []string {
	present := make(map[string]bool, len(in))
	for _, name := range in {
		present[name] = true
	}
	var out []string
	for _, name := range from {
		if !present[name] {
			out = append(out, name)
		}
	}
	return out
}

func readAPI(t *testing.T) string {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("..", "..", "ui", "src", "lib", "api.ts"))
	if err != nil {
		t.Fatalf("reading the TypeScript mirror: %v", err)
	}
	return string(source)
}
