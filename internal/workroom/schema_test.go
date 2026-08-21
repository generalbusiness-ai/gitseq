package workroom

import "testing"

func TestDecodeRejectsNonCanonicalAndMalformedPayloads(t *testing.T) {
	canonical := &State{Kind: KindAssert, Text: "x"}
	canonicalData := []byte(`{"kind":"assert","text":"x"}`)
	if _, err := Decode(SchemaState, canonicalData); err != nil {
		t.Fatalf("Decode rejected canonical payload: %v", err)
	}
	if !canonicalJSONEqual(canonical, canonicalData) {
		t.Fatal("canonical comparison rejected canonical payload")
	}

	cases := []struct {
		name string
		data string
	}{
		{name: "unknown field", data: `{"kind":"assert","text":"x","unknown":"value"}`},
		{name: "duplicate key", data: `{"kind":"assert","kind":"assert","text":"x"}`},
		{name: "trailing whitespace", data: `{"kind":"assert","text":"x"} `},
		{name: "trailing JSON value", data: `{"kind":"assert","text":"x"}{}`},
		{name: "non-canonical escape", data: `{"kind":"assert","text":"\u0078"}`},
		{name: "reordered fields", data: `{"text":"x","kind":"assert"}`},
		{name: "empty input", data: ``},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			data := []byte(test.data)
			if _, err := Decode(SchemaState, data); err == nil {
				t.Fatalf("Decode accepted %q", data)
			}
			// Decode may reject malformed JSON before reaching this comparison.
			// Pin the comparison separately so weakening it still makes every
			// adversarial case fail, including empty or trailing JSON values.
			if canonicalJSONEqual(canonical, data) {
				t.Fatalf("canonical comparison accepted %q", data)
			}
		})
	}
}
