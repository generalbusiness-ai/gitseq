package workroom

import (
	"reflect"
	"testing"
	"unsafe"
)

func TestDecodeRejectsNonCanonicalAndMalformedPayloads(t *testing.T) {
	canonical := &State{Kind: KindAssert, Text: "x"}
	canonicalData := []byte(`{"kind":"assert","text":"x"}`)
	decoders := []struct {
		name   string
		decode func([]byte) (any, error)
	}{
		{name: "ordinary", decode: func(data []byte) (any, error) { return Decode(SchemaState, data) }},
		{name: "pooled", decode: func(data []byte) (any, error) {
			return decode(SchemaState, data, map[string]string{})
		}},
	}
	for _, decoder := range decoders {
		got, err := decoder.decode(canonicalData)
		if err != nil {
			t.Fatalf("%s decode rejected canonical payload: %v", decoder.name, err)
		}
		if !reflect.DeepEqual(got, canonical) {
			t.Fatalf("%s decode = %#v, want %#v", decoder.name, got, canonical)
		}
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
			for _, decoder := range decoders {
				if _, err := decoder.decode(data); err == nil {
					t.Errorf("%s decode accepted %q", decoder.name, data)
				}
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

func TestPooledDecodeReusesResidentText(t *testing.T) {
	plainResident := string(append([]byte(nil), "plain"...))
	escapedResident := string(append([]byte(nil), '<'))
	pool := map[string]string{"plain": plainResident, "<": escapedResident}
	plain, err := decode(SchemaState, []byte(`{"kind":"assert","text":"plain"}`), pool)
	if err != nil || plain.(*State).Text != "plain" {
		t.Fatalf("plain decode = %#v, %v", plain, err)
	}
	if unsafe.StringData(plain.(*State).Text) != unsafe.StringData(plainResident) {
		t.Fatal("plain decode did not reuse resident backing")
	}
	escaped, err := decode(SchemaState, []byte(`{"kind":"assert","text":"\u003c"}`), pool)
	if err != nil || escaped.(*State).Text != "<" {
		t.Fatalf("escaped decode = %#v, %v", escaped, err)
	}
	if unsafe.StringData(escaped.(*State).Text) != unsafe.StringData(escapedResident) {
		t.Fatal("escaped decode did not reuse resident backing")
	}
	if _, err := decode(SchemaState, []byte(`{"kind":"assert","text":1}`), pool); err == nil {
		t.Fatal("pooled decode accepted non-string text")
	}
}
