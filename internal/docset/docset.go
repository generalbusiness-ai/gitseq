// Package docset loads the user documentation set and the command surface it
// describes, so the four documentation gates can compare one against the other.
//
// The gates live beside this file as ordinary Go tests, which is what makes
// documentation build under the project discipline instead of by inspection.
package docset

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// DocsDir is the documentation root, relative to the repository root.
const DocsDir = "docs"

// Page is one documentation page: its front matter and its body.
//
// Path is repository-relative and slash-separated, so it is the same string on
// every platform and can be used directly as an artifact path.
type Page struct {
	Path    string
	Title   string
	Summary string
	// RestsOn names the durable acts that govern the behaviour this page
	// describes. It is the page's anchor: retiring one of these acts is what
	// makes this page, and only the pages that name it, flare.
	RestsOn []string
	Body    string
	// BodyLine is the 1-based line of the file at which Body starts, so a gate
	// can report a source position a reader can jump to.
	BodyLine int
}

// Block is one fenced code block in a page body.
type Block struct {
	// Lang is the fence info string, lowercased and trimmed: "sh", "text", "".
	Lang string
	Code string
	// Line is the 1-based line of the file holding the opening fence.
	Line int
}

// eventID matches the canonical durable event identifier. Only this form is
// accepted in front matter: an abbreviated identifier resolves to nothing, and
// an act that cites nothing can never flare.
var eventID = regexp.MustCompile(`^git:[a-z0-9]+:[0-9a-f]{40,64}#git:[a-z0-9]+:[0-9a-f]{40,64}$`)

// Root returns the repository root by walking up from the working directory.
// Tests run with the working directory set to their package directory, so the
// walk is what keeps the gates independent of where they are invoked from.
func Root() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); err == nil {
			if info, err := os.Stat(filepath.Join(dir, DocsDir)); err == nil && info.IsDir() {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("repository root not found above the working directory")
		}
		dir = parent
	}
}

// Load reads every Markdown page under docs/, in stable path order.
func Load(root string) ([]Page, error) {
	var pages []Page
	docs := filepath.Join(root, DocsDir)
	err := filepath.WalkDir(docs, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		page, err := parse(filepath.ToSlash(relative), string(content))
		if err != nil {
			return fmt.Errorf("%s: %w", filepath.ToSlash(relative), err)
		}
		pages = append(pages, page)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(pages) == 0 {
		return nil, errors.New("no documentation pages found")
	}
	return pages, nil
}

// parse splits front matter from body. The front matter grammar is deliberately
// tiny — scalar `key: value` and a list of `  - value` items — so that no page
// needs a parser dependency and a malformed page fails loudly rather than
// silently losing its anchor.
func parse(path, content string) (Page, error) {
	page := Page{Path: path}
	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	line := 0
	if !scanner.Scan() {
		return page, errors.New("page is empty")
	}
	line++
	if strings.TrimSpace(scanner.Text()) != "---" {
		return page, errors.New("page does not open with front matter delimited by ---")
	}
	key := ""
	closed := false
	for scanner.Scan() {
		line++
		text := scanner.Text()
		if strings.TrimSpace(text) == "---" {
			closed = true
			break
		}
		trimmed := strings.TrimSpace(text)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "- ") {
			if key != "rests_on" {
				return page, fmt.Errorf("line %d: list item outside rests_on", line)
			}
			page.RestsOn = append(page.RestsOn, strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")))
			continue
		}
		name, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			return page, fmt.Errorf("line %d: front matter line is not key: value", line)
		}
		key = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		switch key {
		case "title":
			page.Title = value
		case "summary":
			page.Summary = value
		case "rests_on":
			if value != "" {
				return page, fmt.Errorf("line %d: rests_on takes a list, one act per line", line)
			}
		default:
			return page, fmt.Errorf("line %d: unknown front matter key %q", line, key)
		}
	}
	if err := scanner.Err(); err != nil {
		return page, err
	}
	if !closed {
		return page, errors.New("front matter is not closed by ---")
	}
	var body strings.Builder
	page.BodyLine = line + 1
	for scanner.Scan() {
		body.WriteString(scanner.Text())
		body.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		return page, err
	}
	page.Body = body.String()
	return page, nil
}

// ValidEventID reports whether an act identifier is in the canonical form the
// workroom emits.
func ValidEventID(id string) bool { return eventID.MatchString(id) }

// Blocks returns the fenced code blocks of a page, in order.
func (p Page) Blocks() []Block {
	var blocks []Block
	lines := strings.Split(p.Body, "\n")
	for index := 0; index < len(lines); index++ {
		text := lines[index]
		if !strings.HasPrefix(strings.TrimLeft(text, " "), "```") {
			continue
		}
		indent := len(text) - len(strings.TrimLeft(text, " "))
		lang := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimLeft(text, " "), "```")))
		var code strings.Builder
		start := index
		index++
		for ; index < len(lines); index++ {
			if strings.TrimSpace(lines[index]) == "```" {
				break
			}
			body := lines[index]
			if len(body) >= indent && strings.TrimSpace(body[:indent]) == "" {
				body = body[indent:]
			}
			code.WriteString(body)
			code.WriteByte('\n')
		}
		blocks = append(blocks, Block{Lang: lang, Code: code.String(), Line: p.BodyLine + start})
	}
	return blocks
}

// Section returns the body of one `## heading` section, up to the next heading
// at the same or a higher level. The gates read tables out of named sections,
// so that a flag mentioned in passing in prose is not mistaken for a
// documented flag.
func (p Page) Section(heading string) (string, bool) {
	lines := strings.Split(p.Body, "\n")
	want := strings.ToLower(heading)
	for index := 0; index < len(lines); index++ {
		text := strings.TrimSpace(lines[index])
		if !strings.HasPrefix(text, "## ") {
			continue
		}
		if strings.ToLower(strings.TrimSpace(strings.TrimPrefix(text, "## "))) != want {
			continue
		}
		var section strings.Builder
		for index++; index < len(lines); index++ {
			next := strings.TrimSpace(lines[index])
			if strings.HasPrefix(next, "## ") || strings.HasPrefix(next, "# ") {
				break
			}
			section.WriteString(lines[index])
			section.WriteByte('\n')
		}
		return section.String(), true
	}
	return "", false
}

var codeSpan = regexp.MustCompile("`([^`]+)`")

// TableKeys returns the first-column code span of every data row of the first
// Markdown table in a section: the names a page claims to document.
func TableKeys(section string) []string {
	var keys []string
	for _, line := range strings.Split(section, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(trimmed, "|"), "|")
		if len(cells) == 0 {
			continue
		}
		first := strings.TrimSpace(cells[0])
		if first == "" || strings.HasPrefix(first, "---") || strings.HasPrefix(first, ":--") {
			continue
		}
		match := codeSpan.FindStringSubmatch(first)
		if match == nil {
			continue
		}
		keys = append(keys, match[1])
	}
	return keys
}
