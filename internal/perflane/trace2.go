package perflane

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"time"
)

const maxTrace2LineBytes = 1024 * 1024

const maxTrace2Duration = time.Duration(1<<63 - 1)

var ErrIncompleteTrace = errors.New("incomplete Git Trace2 process data")

type Trace2Summary struct {
	ChildProcessCount  int           `json:"child_process_count"`
	CumulativeDuration time.Duration `json:"cumulative_duration_ns"`
	IncompleteChildren int           `json:"incomplete_children"`
}

type trace2Event struct {
	Event string   `json:"event"`
	SID   string   `json:"sid"`
	TAbs  *float64 `json:"t_abs"`
}

// ParseTrace2 parses concatenated Git Trace2 event streams. Every Git process
// emits its own version and exit events, including Git children of another Git
// process. Counting those session IDs measures the processes launched by the
// operation without double-counting the parent's child_start announcement.
// A missing exit returns the partial
// summary and ErrIncompleteTrace; malformed or contradictory records return a
// line-numbered error.
func ParseTrace2(reader io.Reader) (Trace2Summary, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), maxTrace2LineBytes)
	active := make(map[string]struct{})
	var summary Trace2Summary
	line := 0
	for scanner.Scan() {
		line++
		if len(scanner.Bytes()) == 0 {
			continue
		}
		var event trace2Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return summary, fmt.Errorf("parse Git Trace2 line %d: %w", line, err)
		}
		switch event.Event {
		case "version":
			if event.SID == "" {
				return summary, fmt.Errorf("parse Git Trace2 line %d: version has no sid", line)
			}
			if _, exists := active[event.SID]; exists {
				return summary, fmt.Errorf("parse Git Trace2 line %d: duplicate active process %q", line, event.SID)
			}
			active[event.SID] = struct{}{}
			summary.ChildProcessCount++
		case "exit":
			if event.SID == "" || event.TAbs == nil {
				return summary, fmt.Errorf("parse Git Trace2 line %d: exit requires sid and t_abs", line)
			}
			if math.IsNaN(*event.TAbs) || math.IsInf(*event.TAbs, 0) || *event.TAbs < 0 {
				return summary, fmt.Errorf("parse Git Trace2 line %d: invalid process duration", line)
			}
			nanoseconds := math.Round(*event.TAbs * float64(time.Second))
			if nanoseconds >= float64(maxTrace2Duration) {
				return summary, fmt.Errorf("parse Git Trace2 line %d: process duration overflows nanoseconds", line)
			}
			if _, exists := active[event.SID]; !exists {
				return summary, fmt.Errorf("parse Git Trace2 line %d: exit without matching version for %q", line, event.SID)
			}
			delete(active, event.SID)
			duration := time.Duration(nanoseconds)
			if duration > maxTrace2Duration-summary.CumulativeDuration {
				return summary, fmt.Errorf("parse Git Trace2 line %d: cumulative child duration overflows nanoseconds", line)
			}
			summary.CumulativeDuration += duration
		}
	}
	if err := scanner.Err(); err != nil {
		return summary, fmt.Errorf("parse Git Trace2 line %d: %w", line+1, err)
	}
	if len(active) > 0 {
		summary.IncompleteChildren = len(active)
		return summary, fmt.Errorf("%w: %d Git process(es) have no exit", ErrIncompleteTrace, len(active))
	}
	return summary, nil
}
