package perflane

import "testing"

func TestBenchmarkCasesHaveStableOrderAndNames(t *testing.T) {
	cases, err := BenchmarkCases(validContract())
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 67 {
		t.Fatalf("case count = %d, want 67", len(cases))
	}
	wants := map[int]string{
		0:  "startup/depth-000100",
		4:  "startup/depth-500000",
		25: "checkpoint_restart/depth-000257/tail-0000",
		30: "checkpoint_restart/depth-001257/tail-1000",
		31: "honest_fallback/depth-000100",
		41: "concurrent_read_write/depth-000100/concurrency-01",
		55: "concurrent_read_write/depth-500000/concurrency-16",
		60: "bounded_soak/depth-500000",
		61: "cold_status/depth-000100/actors-008",
		62: "cold_status/depth-000100/actors-050",
		66: "submit_ack/depth-001000/fanout-256",
	}
	for index, want := range wants {
		if got := cases[index].Name(); got != want {
			t.Fatalf("case %d = %q, want %q", index, got, want)
		}
	}
}

func TestAlternatingSampleOrder(t *testing.T) {
	runs, err := AlternatingSampleOrder(3)
	if err != nil {
		t.Fatal(err)
	}
	want := []Revision{BaseRevision, CandidateRevision, CandidateRevision, BaseRevision, BaseRevision, CandidateRevision}
	for index, revision := range want {
		if runs[index].Revision != revision || runs[index].Round != index/2+1 || runs[index].Position != index%2+1 {
			t.Fatalf("run %d = %#v", index, runs[index])
		}
	}
	if _, err := AlternatingSampleOrder(0); err == nil {
		t.Fatal("AlternatingSampleOrder accepted zero rounds")
	}
}
