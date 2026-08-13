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
	"sort"
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

// UnmaintainablePath reports why normal merge succession can never maintain an
// artifact at this path, or "" when it can. A page anchored to such an artifact
// cannot flare: paths match as exact strings, so no merge will ever publish a
// successor at one of these, nothing will retire the artifact, and its silence
// reads as currency.
//
// The four shapes are the ones the log has actually accumulated. "." is the
// whole-repository pointer every merge would have to rewrite. A comma-joined
// string is several paths pretending to be one, which equals no real path. An
// absolute filesystem path names one machine's copy of the repository and means
// nothing in another clone. A branch name is not a location in the tree at all,
// and disappears when the branch does.
func UnmaintainablePath(path string) string {
	switch {
	case path == ".":
		return "the whole-repository path, which no merge supersedes"
	case strings.HasPrefix(path, "/"):
		return "an absolute filesystem path, which names one machine's checkout"
	case strings.Contains(path, ","):
		return "a comma-joined pseudo-path, which is several paths and therefore none"
	case strings.HasPrefix(path, "request/"), strings.HasPrefix(path, "task/"),
		strings.HasPrefix(path, "security/"):
		return "a branch name rather than a path in the tree"
	}
	return ""
}

// CitationVerdict is what a documentation citation may do as a basis. Fatal
// names a citation that can never serve: nothing about it will improve by
// waiting. Report names one that is serving correctly and has something to say
// — staleness is the set working, not the set broken.
type CitationVerdict struct {
	Fatal  bool
	Report bool
	Reason string
}

// ClassifyCitation judges one front-matter citation from facts the caller has
// already resolved against the durable log. It takes primitives rather than
// workroom types so the whole table can be exercised without a workroom, which
// is the only way to cover the cases a real log does not currently contain.
//
// succeeded is what separates a withdrawn pointer from a moved one. A
// retirement that names a successor covering the same path leaves the page
// somewhere to go, and the log says where; that is a flare, the same as
// staleness, and the page re-anchors when someone re-reads the prose. A
// retirement that names nothing leaves the page pointing at a hole, which is
// fatal and always was. Without the distinction the set could not survive its
// own merges: every merge retires the artifacts the pages cite, so retirement
// alone being fatal meant either the merge was refused or the set went red.
func ClassifyCitation(found, isArtifact, retired, succeeded, stale bool, path, commit string) CitationVerdict {
	switch {
	case !found:
		return CitationVerdict{Fatal: true, Reason: "resolves to no statement in this workroom"}
	case !isArtifact:
		// Retiring the request that asked for a page never makes the page
		// wrong. Only an artifact stands for the implementation described.
		return CitationVerdict{Fatal: true, Reason: "is not an artifact, so retiring it would say nothing about the pages naming it"}
	case retired && !succeeded:
		return CitationVerdict{Fatal: true, Reason: "is retired with no successor, so the pages naming it rest on a withdrawn pointer"}
	case !canonicalCommit(commit):
		// Not cosmetic. gs merge refuses anything that is not the full
		// canonical object ID, and a review verdict resolves to its artifact by
		// matching this field as an exact string. An abbreviated commit here is
		// an artifact that cannot take part in merge or review at all.
		return CitationVerdict{Fatal: true, Reason: "names a commit that is not a full canonical object ID, so it can take no part in merge or review"}
	}
	if why := UnmaintainablePath(path); why != "" {
		return CitationVerdict{Fatal: true, Reason: "sits at " + why + ", so nothing will supersede it and the pages naming it can never flare"}
	}
	if retired {
		return CitationVerdict{Report: true, Reason: "is retired and its retirement names a successor covering the same path; the pages naming it should re-anchor there"}
	}
	if stale {
		// Reported, never fatal. A stale basis means something under this
		// artifact moved; the pages naming it are flaring, which is what the
		// set is for. Failing here would redden the whole set for working.
		return CitationVerdict{Report: true, Reason: "has a basis that moved; the pages naming it are flaring, which is intended"}
	}
	return CitationVerdict{}
}

// canonicalCommit reports whether a commit field is a full object ID. Git's two
// hash sizes are the only lengths a commit may have; anything shorter is an
// abbreviation that no exact-string comparison will match.
func canonicalCommit(commit string) bool {
	if len(commit) != 40 && len(commit) != 64 {
		return false
	}
	for _, r := range commit {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// BaselineEntry is one known-failing citation: why it fails, and exactly which
// pages depend on it. The page set is part of the record because the event ID
// alone cannot distinguish "the same defect, still in the same places" from
// "the same defect, now in one more place" — and the second is a new violation
// however familiar the artifact is.
type BaselineEntry struct {
	Reason string
	Pages  []string
}

// BaselineFinding is one disagreement between the known-failing list and what
// the gate currently sees.
type BaselineFinding struct {
	Citation string
	Reason   string
}

// CompareBaseline enforces the three ways a known-failing list may be wrong.
// New is a citation failing now that nobody has accounted for. Changed is one
// whose defect, or whose set of dependent pages, is not what was recorded —
// the page set matters because a citation the list already knows about can
// spread to a page it did not cover, which is a fresh violation wearing a
// familiar event ID. Fixed is an entry that has stopped failing: reported as an
// error too, because a list that keeps entries after their repair is an
// exceptions file, and the only property that stops this becoming one is that
// it must shrink.
func CompareBaseline(failing, baseline map[string]BaselineEntry) (newly, changed, fixed []BaselineFinding) {
	for citation, current := range failing {
		recorded, known := baseline[citation]
		switch {
		case !known:
			newly = append(newly, BaselineFinding{Citation: citation, Reason: current.Reason})
		case recorded.Reason != current.Reason:
			changed = append(changed, BaselineFinding{Citation: citation,
				Reason: "recorded as " + recorded.Reason + ", now " + current.Reason})
		case !samePages(recorded.Pages, current.Pages):
			changed = append(changed, BaselineFinding{Citation: citation,
				Reason: "recorded for " + strings.Join(recorded.Pages, ", ") + ", now cited by " + strings.Join(current.Pages, ", ")})
		}
	}
	for citation, recorded := range baseline {
		if _, still := failing[citation]; !still {
			fixed = append(fixed, BaselineFinding{Citation: citation,
				Reason: "no longer failing (was " + recorded.Reason + "); delete this line in the head that repaired it"})
		}
	}
	sort.Slice(newly, func(i, j int) bool { return newly[i].Citation < newly[j].Citation })
	sort.Slice(changed, func(i, j int) bool { return changed[i].Citation < changed[j].Citation })
	sort.Slice(fixed, func(i, j int) bool { return fixed[i].Citation < fixed[j].Citation })
	return newly, changed, fixed
}

func samePages(recorded, current []string) bool {
	if len(recorded) != len(current) {
		return false
	}
	for i := range recorded {
		if recorded[i] != current[i] {
			return false
		}
	}
	return true
}

// ParseBaseline reads the known-failing list. A repeated event ID is an error
// rather than a last-one-wins overwrite: two rows for one citation means the
// file disagrees with itself about which pages are covered, and silently
// keeping either would let the uncovered pages through.
func ParseBaseline(raw string) (map[string]BaselineEntry, error) {
	entries := make(map[string]BaselineEntry)
	for number, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 3 {
			return nil, fmt.Errorf("baseline line %d has %d tab-separated fields, want event, reason and pages", number+1, len(fields))
		}
		if _, repeated := entries[fields[0]]; repeated {
			return nil, fmt.Errorf("baseline line %d repeats %s; one citation has one row", number+1, fields[0])
		}
		pages := strings.Split(fields[2], ",")
		sort.Strings(pages)
		entries[fields[0]] = BaselineEntry{Reason: fields[1], Pages: pages}
	}
	return entries, nil
}
