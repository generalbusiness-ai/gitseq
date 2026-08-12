package perflane

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseTrace2CountsChildrenAndDuration(t *testing.T) {
	trace := strings.Join([]string{
		`{"event":"version","sid":"parent"}`,
		`{"event":"child_start","sid":"parent","child_id":0,"argv":["git","status"]}`,
		`{"event":"version","sid":"parent/child"}`,
		`{"event":"exit","sid":"parent/child","t_abs":0.0025}`,
		`{"event":"child_exit","sid":"parent","child_id":0,"t_rel":0.0025}`,
		`{"event":"exit","sid":"parent","t_abs":0.01025}`,
		"",
	}, "\n")
	summary, err := ParseTrace2(strings.NewReader(trace))
	if err != nil {
		t.Fatalf("ParseTrace2: %v", err)
	}
	if summary.ChildProcessCount != 2 || summary.CumulativeDuration != 12_750*time.Microsecond || summary.IncompleteChildren != 0 {
		t.Fatalf("summary = %#v", summary)
	}
}

func TestParseTrace2ReadsARealGitProcess(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is unavailable")
	}
	directory := t.TempDir()
	trace := filepath.Join(directory, "trace.json")
	command := exec.Command(git, "init", "-q", filepath.Join(directory, "repository"))
	command.Env = append(os.Environ(), "GIT_TRACE2_EVENT="+trace)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	file, err := os.Open(trace)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	summary, err := ParseTrace2(file)
	if err != nil {
		t.Fatal(err)
	}
	if summary.ChildProcessCount < 1 || summary.CumulativeDuration <= 0 || summary.IncompleteChildren != 0 {
		t.Fatalf("real Git summary = %#v", summary)
	}
}

func TestParseTrace2MalformedInput(t *testing.T) {
	trace := "{\"event\":\"version\",\"sid\":\"root\"}\nnot-json\n"
	summary, err := ParseTrace2(strings.NewReader(trace))
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("malformed error = %v", err)
	}
	if summary.ChildProcessCount != 1 {
		t.Fatalf("partial summary = %#v", summary)
	}
}

func TestParseTrace2ReportsIncompleteChildren(t *testing.T) {
	trace := `{"event":"version","sid":"parent"}`
	summary, err := ParseTrace2(strings.NewReader(trace))
	if !errors.Is(err, ErrIncompleteTrace) {
		t.Fatalf("incomplete error = %v", err)
	}
	if summary.ChildProcessCount != 1 || summary.IncompleteChildren != 1 {
		t.Fatalf("summary = %#v", summary)
	}
}

func TestParseTrace2RejectsContradictoryChildEvents(t *testing.T) {
	tests := []string{
		`{"event":"version"}`,
		`{"event":"exit","sid":"root","t_abs":0.1}`,
		"{\"event\":\"version\",\"sid\":\"root\"}\n{\"event\":\"version\",\"sid\":\"root\"}",
		"{\"event\":\"version\",\"sid\":\"root\"}\n{\"event\":\"exit\",\"sid\":\"root\",\"t_abs\":-1}",
		"{\"event\":\"version\",\"sid\":\"root\"}\n{\"event\":\"exit\",\"sid\":\"root\",\"t_abs\":1e300}",
	}
	for _, trace := range tests {
		if _, err := ParseTrace2(strings.NewReader(trace)); err == nil {
			t.Fatalf("ParseTrace2 accepted %q", trace)
		}
	}
}
