package perflane

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEvidenceDistinguishesZeroFromUnavailable(t *testing.T) {
	zero := Available(0)
	if err := zero.Validate(); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(zero)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"value":0}` {
		t.Fatalf("zero JSON = %s", data)
	}

	unavailable := Unavailable[int]("not supported on this platform")
	if err := unavailable.Validate(); err != nil {
		t.Fatal(err)
	}
	data, err = json.Marshal(unavailable)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"value":null,"unavailable_reason":"not supported on this platform"}` {
		t.Fatalf("unavailable JSON = %s", data)
	}
}

func TestEvidenceRejectsAmbiguousStates(t *testing.T) {
	value := 1
	for _, evidence := range []Evidence[int]{
		{},
		{Value: &value, UnavailableReason: "also unavailable"},
	} {
		if err := evidence.Validate(); err == nil {
			t.Fatalf("Validate accepted %#v", evidence)
		}
	}
}

func TestEnvironmentEvidenceRequiresEveryField(t *testing.T) {
	environment := EnvironmentEvidence{
		OS:              Available("linux"),
		Architecture:    Available("amd64"),
		GoVersion:       Available("go1.26"),
		GitVersion:      Available("2.51"),
		CPUModel:        Unavailable[string]("not exposed"),
		LogicalCPUs:     Available(0),
		MemoryBytes:     Unavailable[uint64]("not exposed"),
		ContainerCPU:    Unavailable[string]("not in a constrained container"),
		ContainerMemory: Unavailable[uint64]("not in a constrained container"),
		Filesystem:      Unavailable[string]("not exposed"),
		PowerMode:       Unavailable[string]("not exposed"),
		DirtyWorktree:   Available(false),
	}
	if err := environment.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	environment.OS = Evidence[string]{}
	if err := environment.Validate(); err == nil || !strings.Contains(err.Error(), "environment os") {
		t.Fatalf("missing OS error = %v", err)
	}
}
