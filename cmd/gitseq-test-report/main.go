package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
)

type event struct {
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Output  string  `json:"Output"`
	Elapsed float64 `json:"Elapsed"`
}

type timing struct {
	name    string
	elapsed float64
}

func main() {
	if err := report(os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func report(input io.Reader, output, summary io.Writer) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	packages := make([]timing, 0)
	tests := make([]timing, 0)
	started := make(map[string]bool)
	finished := make(map[string]bool)
	failed := false
	for scanner.Scan() {
		var item event
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			return fmt.Errorf("decode go test event: %w", err)
		}
		if item.Output != "" {
			if _, err := io.WriteString(output, item.Output); err != nil {
				return err
			}
		}
		if item.Action == "fail" {
			failed = true
		}
		if item.Test == "" && item.Package != "" {
			switch item.Action {
			case "start":
				started[item.Package] = true
			case "pass", "fail", "skip":
				finished[item.Package] = true
			}
		}
		if item.Action != "pass" || item.Elapsed <= 0 {
			continue
		}
		if item.Test == "" {
			packages = append(packages, timing{name: item.Package, elapsed: item.Elapsed})
		} else {
			tests = append(tests, timing{name: item.Package + ":" + item.Test, elapsed: item.Elapsed})
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read go test stream: %w", err)
	}
	if len(finished) == 0 {
		return fmt.Errorf("go test stream contained no completed package")
	}
	for name := range started {
		if !finished[name] {
			return fmt.Errorf("go test stream ended before %s completed", name)
		}
	}
	writeSlowest(summary, "packages", packages)
	writeSlowest(summary, "tests", tests)
	if failed {
		return fmt.Errorf("go test failed")
	}
	return nil
}

func writeSlowest(output io.Writer, label string, timings []timing) {
	sort.Slice(timings, func(i, j int) bool {
		if timings[i].elapsed == timings[j].elapsed {
			return timings[i].name < timings[j].name
		}
		return timings[i].elapsed > timings[j].elapsed
	})
	if len(timings) > 10 {
		timings = timings[:10]
	}
	fmt.Fprintf(output, "slowest %s from this go test stream:\n", label)
	for _, item := range timings {
		fmt.Fprintf(output, "  %6.2fs  %s\n", item.elapsed, item.name)
	}
}
