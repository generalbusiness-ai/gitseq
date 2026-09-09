package service

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// A preview answers with a bounded window of a text file rather than the
// whole of it. The Git read budget (gitstore.PreviewReadBudget) bounds the
// verification work; these bounds are what one answer may carry back.
const (
	// PreviewWindowLines is the most lines one window carries.
	PreviewWindowLines = 400
	// PreviewWindowBytes is the most content bytes one window carries; a
	// window of long lines closes early rather than exceed it.
	PreviewWindowBytes = 64 << 10
	// PreviewLineBytes is the most bytes one line is shown with. A longer
	// line is cut at a UTF-8 boundary and named in the window's truncated
	// list, so a pathological line cannot defeat the byte bound alone.
	PreviewLineBytes = 4096
	// previewLineCeiling bounds the line and start numbers a request may
	// name; seven digits already exceed anything the read budget can hold.
	previewLineCeiling = 9_999_999
)

// previewWindow describes which lines of the file the content field holds.
// Partial is false only when the content is the complete file, in which case
// the content is the exact blob text. Previous and Next are the first lines
// of the neighbouring windows, absent at either end.
type previewWindow struct {
	Start     int   `json:"start"`
	End       int   `json:"end"`
	Total     int   `json:"total"`
	Partial   bool  `json:"partial"`
	Previous  int   `json:"previous,omitempty"`
	Next      int   `json:"next,omitempty"`
	Truncated []int `json:"truncated,omitempty"`
}

// previewLines splits text the way a reader counts lines: a trailing newline
// ends the last line rather than starting an empty one, and an empty file has
// no lines.
func previewLines(content []byte) [][]byte {
	if len(content) == 0 {
		return nil
	}
	lines := bytes.Split(content, []byte{'\n'})
	if len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// windowFor selects the window a request asked for. Windows are a fixed
// partition of the file: reading from line one, each window takes up to
// PreviewWindowLines lines and closes early when the next line would take
// it past PreviewWindowBytes (a line is measured as shown, cut at
// PreviewLineBytes, so every window carries at least one line). Because the
// partition depends only on the file, a link naming any line of a window
// opens that same window later, Next and Previous are exact neighbours, and
// a window opened for a cited line always contains it. For a file of short
// lines the partition is the aligned 400-line windows.
//
// An explicit start wins and names the window containing that line;
// otherwise the cited line's window; with neither, the first window. It
// returns the content to show, the window, and a message for a cited line
// or start beyond the file. A file that fits in one window is returned
// whole and unchanged.
func windowFor(content []byte, line, start int) (string, previewWindow, string) {
	lines := previewLines(content)
	total := len(lines)
	if total <= PreviewWindowLines && len(content) <= PreviewWindowBytes && longestLine(lines) <= PreviewLineBytes {
		if start > total {
			return "", previewWindow{Start: start, End: start - 1, Total: total, Partial: true, Previous: min(1, total)},
				fmt.Sprintf("Window start %d is outside this file, which has %d lines.", start, total)
		}
		window := previewWindow{Start: min(1, total), End: total, Total: total}
		message := ""
		if line > total {
			message = fmt.Sprintf("Cited line %d is outside this file, which has %d lines.", line, total)
		}
		return string(content), window, message
	}
	starts := partition(lines)
	message := ""
	target := 1
	switch {
	case start > 0:
		if start > total {
			return "", previewWindow{Start: start, End: start - 1, Total: total, Partial: true, Previous: starts[len(starts)-1]},
				fmt.Sprintf("Window start %d is outside this file, which has %d lines.", start, total)
		}
		target = start
	case line > 0:
		target = line
		if line > total {
			message = fmt.Sprintf("Cited line %d is outside this file, which has %d lines.", line, total)
			target = total
		}
	}
	index := sort.Search(len(starts), func(i int) bool { return starts[i] > target }) - 1
	first := starts[index]
	last := total
	if index+1 < len(starts) {
		last = starts[index+1] - 1
	}
	window := previewWindow{Start: first, End: last, Total: total, Partial: true}
	var out strings.Builder
	for i := first - 1; i < last; i++ {
		if i >= first {
			out.WriteByte('\n')
		}
		if len(lines[i]) > PreviewLineBytes {
			window.Truncated = append(window.Truncated, i+1)
		}
		out.Write(shownLine(lines[i]))
	}
	if index > 0 {
		window.Previous = starts[index-1]
	}
	if index+1 < len(starts) {
		window.Next = starts[index+1]
	}
	return out.String(), window, message
}

// shownLine is a line as a window carries it: cut at the byte bound on a
// rune boundary.
func shownLine(line []byte) []byte {
	if len(line) > PreviewLineBytes {
		return cutUTF8(line, PreviewLineBytes)
	}
	return line
}

// partition returns the first line (1-based) of every window of the file,
// in order, always beginning with 1.
func partition(lines [][]byte) []int {
	starts := []int{1}
	count, used := 0, 0
	for i, line := range lines {
		size := len(shownLine(line)) + 1
		if count > 0 && (count == PreviewWindowLines || used+size > PreviewWindowBytes) {
			starts = append(starts, i+1)
			count, used = 0, 0
		}
		count++
		used += size
	}
	return starts
}

func longestLine(lines [][]byte) int {
	longest := 0
	for _, line := range lines {
		longest = max(longest, len(line))
	}
	return longest
}

// cutUTF8 shortens text to at most limit bytes without splitting a rune.
func cutUTF8(text []byte, limit int) []byte {
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}
