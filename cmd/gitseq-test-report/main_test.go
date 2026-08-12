package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestReportPreservesOutputAndRanksOneStream(t *testing.T) {
	input := strings.Join([]string{
		`{"Action":"start","Package":"example/a"}`,
		`{"Action":"output","Package":"example/a","Test":"TestSlow","Output":"--- PASS: TestSlow (2.00s)\n"}`,
		`{"Action":"pass","Package":"example/a","Test":"TestSlow","Elapsed":2}`,
		`{"Action":"pass","Package":"example/a","Elapsed":3}`,
		`{"Action":"pass","Package":"example/b","Test":"TestFast","Elapsed":1}`,
		`{"Action":"pass","Package":"example/b","Elapsed":1.5}`,
	}, "\n")
	var output, summary bytes.Buffer
	if err := report(strings.NewReader(input), &output, &summary); err != nil {
		t.Fatal(err)
	}
	if output.String() != "--- PASS: TestSlow (2.00s)\n" {
		t.Fatalf("test output = %q", output.String())
	}
	want := "slowest packages from this go test stream:\n    3.00s  example/a\n    1.50s  example/b\nslowest tests from this go test stream:\n    2.00s  example/a:TestSlow\n    1.00s  example/b:TestFast\n"
	if summary.String() != want {
		t.Fatalf("summary = %q, want %q", summary.String(), want)
	}
}

func TestReportReturnsFailureFromTheSameStream(t *testing.T) {
	input := `{"Action":"fail","Package":"example/a","Test":"TestBroken","Elapsed":0.1}`
	if err := report(strings.NewReader(input), &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("failed test stream reported success")
	}
}

func TestReportRejectsAnEmptyOrInterruptedStream(t *testing.T) {
	for _, input := range []string{
		"",
		`{"Action":"start","Package":"example/a"}`,
	} {
		if err := report(strings.NewReader(input), &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
			t.Fatalf("incomplete stream %q reported success", input)
		}
	}
}
