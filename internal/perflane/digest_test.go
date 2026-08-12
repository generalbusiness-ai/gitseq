package perflane

import (
	"bytes"
	"testing"
)

func TestCanonicalJSONAndDigestAreStable(t *testing.T) {
	left := map[string]any{"z": []any{1, "two"}, "a": map[string]any{"b": true, "a": nil}}
	right := map[string]any{"a": map[string]any{"a": nil, "b": true}, "z": []any{1, "two"}}
	leftJSON, err := CanonicalJSON(left)
	if err != nil {
		t.Fatal(err)
	}
	rightJSON, err := CanonicalJSON(right)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(leftJSON, rightJSON) {
		t.Fatalf("canonical JSON differs: %s != %s", leftJSON, rightJSON)
	}
	leftDigest, err := CorrectnessDigest(left)
	if err != nil {
		t.Fatal(err)
	}
	rightDigest, err := CorrectnessDigest(right)
	if err != nil {
		t.Fatal(err)
	}
	if leftDigest != rightDigest {
		t.Fatalf("stable values have different digests: %s != %s", leftDigest, rightDigest)
	}

	right["z"] = []any{1, "changed"}
	mutatedDigest, err := CorrectnessDigest(right)
	if err != nil {
		t.Fatal(err)
	}
	if mutatedDigest == leftDigest {
		t.Fatal("mutation did not change digest")
	}
}

func TestCanonicalJSONRejectsUnsupportedValue(t *testing.T) {
	if _, err := CanonicalJSON(make(chan int)); err == nil {
		t.Fatal("CanonicalJSON accepted a channel")
	}
}
