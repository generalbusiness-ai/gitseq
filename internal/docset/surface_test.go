package docset

import (
	"path"
	"sort"
	"strings"
	"testing"
)

// Gate 1, surface completeness. The reference is one page per `gs` subcommand
// and one page per MCP tool, and each page's tables must name exactly the
// flags or arguments the implementation accepts. A subcommand added without a
// page, a flag added without a table row, a page for something that no longer
// exists, and a row for a flag that was removed all fail here.
//
// The comparison is against the implementation source, not against a list
// maintained beside it, because a hand-kept list is forgotten by the same
// person who forgets the documentation.

const (
	gsPages  = DocsDir + "/reference/gs"
	mcpPages = DocsDir + "/reference/mcp"
)

func TestGateSurfaceCoversEveryCLISubcommand(t *testing.T) {
	root := mustRoot(t)
	commands, err := CLISurface(root)
	if err != nil {
		t.Fatal(err)
	}
	pages := pagesUnder(t, root, gsPages)

	documented := make(map[string]bool, len(pages))
	for name := range pages {
		documented[name] = true
	}
	for _, command := range commands {
		page, ok := pages[command.Name]
		if !ok {
			t.Errorf("gs %s has no reference page at %s/%s.md", command.Name, gsPages, command.Name)
			continue
		}
		delete(documented, command.Name)
		section, ok := page.Section("Flags")
		if !ok {
			t.Errorf("%s: no `## Flags` section", page.Path)
			continue
		}
		want := make([]string, 0, len(command.Flags))
		for _, flag := range command.Flags {
			want = append(want, "--"+flag)
		}
		got := TableKeys(section)
		sort.Strings(got)
		sort.Strings(want)
		if diff := difference(got, want); diff != "" {
			t.Errorf("%s: documented flags do not match the implementation:\n%s", page.Path, diff)
		}
	}
	for name := range documented {
		t.Errorf("%s/%s.md documents a subcommand `gs` does not have", gsPages, name)
	}
}

func TestGateSurfaceCoversEveryMCPTool(t *testing.T) {
	root := mustRoot(t)
	tools, err := MCPSurface(root)
	if err != nil {
		t.Fatal(err)
	}
	pages := pagesUnder(t, root, mcpPages)

	documented := make(map[string]bool, len(pages))
	for name := range pages {
		documented[name] = true
	}
	for _, tool := range tools {
		page, ok := pages[tool.Name]
		if !ok {
			t.Errorf("MCP tool %s has no reference page at %s/%s.md", tool.Name, mcpPages, tool.Name)
			continue
		}
		delete(documented, tool.Name)
		section, ok := page.Section("Arguments")
		if !ok {
			t.Errorf("%s: no `## Arguments` section", page.Path)
			continue
		}
		if len(tool.Arguments) == 0 {
			if !strings.Contains(section, "No arguments.") {
				t.Errorf("%s: %s takes no arguments; the Arguments section must say `No arguments.`", page.Path, tool.Name)
			}
			if keys := TableKeys(section); len(keys) != 0 {
				t.Errorf("%s: %s takes no arguments but the page documents %v", page.Path, tool.Name, keys)
			}
			continue
		}
		got := TableKeys(section)
		sort.Strings(got)
		if diff := difference(got, tool.Arguments); diff != "" {
			t.Errorf("%s: documented arguments do not match the input schema:\n%s", page.Path, diff)
		}
		if diff := difference(requiredRows(section), tool.Required); diff != "" {
			t.Errorf("%s: rows marked required do not match the input schema:\n%s", page.Path, diff)
		}
		for _, value := range tool.Enums {
			if !strings.Contains(page.Body, value) {
				t.Errorf("%s: schema enum value %q is not named on the page; a caller following the page cannot form a legal call", page.Path, value)
			}
		}
	}
	for name := range documented {
		t.Errorf("%s/%s.md documents a tool the adapter does not serve", mcpPages, name)
	}
}

// requiredRows reads the argument names whose second table column says
// `required`, so that a page cannot pass by listing every argument and
// leaving the reader to guess which ones a call must carry.
func requiredRows(section string) []string {
	var required []string
	for _, line := range strings.Split(section, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(trimmed, "|"), "|")
		if len(cells) < 2 {
			continue
		}
		names := TableKeys(trimmed)
		if len(names) == 0 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(cells[1]), "required") {
			required = append(required, names[0])
		}
	}
	sort.Strings(required)
	return required
}

func pagesUnder(t *testing.T, root, directory string) map[string]Page {
	t.Helper()
	all := mustPages(t, root)
	found := make(map[string]Page)
	for _, page := range all {
		if path.Dir(page.Path) != directory {
			continue
		}
		found[strings.TrimSuffix(path.Base(page.Path), ".md")] = page
	}
	if len(found) == 0 {
		t.Fatalf("no pages under %s", directory)
	}
	return found
}

func difference(got, want []string) string {
	missing := subtract(want, got)
	extra := subtract(got, want)
	if len(missing) == 0 && len(extra) == 0 {
		return ""
	}
	var report strings.Builder
	if len(missing) > 0 {
		report.WriteString("  undocumented: " + strings.Join(missing, ", ") + "\n")
	}
	if len(extra) > 0 {
		report.WriteString("  documented but absent from the implementation: " + strings.Join(extra, ", ") + "\n")
	}
	return report.String()
}

func subtract(from, remove []string) []string {
	index := make(map[string]bool, len(remove))
	for _, value := range remove {
		index[value] = true
	}
	var result []string
	for _, value := range from {
		if !index[value] {
			result = append(result, value)
		}
	}
	return result
}

func mustRoot(t *testing.T) string {
	t.Helper()
	root, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func mustPages(t *testing.T, root string) []Page {
	t.Helper()
	pages, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return pages
}
