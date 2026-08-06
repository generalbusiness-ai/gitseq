package intent

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
)

func fixture(t *testing.T) (Intent, Signed) {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	i := Intent{
		Version: Version, Target: "git:sha1:abc", Schema: "example.v0",
		PayloadTree: "git:sha1:def", RestsOn: []string{"git:sha1:abc#git:sha1:123"},
		IdempotencyNS: "actor", IdempotencyKey: "one",
	}
	signed, err := Sign(i, private)
	if err != nil {
		t.Fatal(err)
	}
	return i, signed
}

func TestIntentRoundTripAndBinding(t *testing.T) {
	original, signed := fixture(t)
	decoded, err := Verify(signed)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Target != original.Target || decoded.PayloadTree != original.PayloadTree {
		t.Fatalf("round trip mismatch: %#v", decoded)
	}

	mutated := append([]byte(nil), signed.Intent...)
	mutated[len(mutated)-1] ^= 1
	signed.Intent = mutated
	if _, err := Verify(signed); err == nil {
		t.Fatal("mutated intent verified")
	}
}

func TestEnvelopeRejectsAlteredCausality(t *testing.T) {
	_, signed := fixture(t)
	message := Envelope(signed, []string{"git:sha1:abc#git:sha1:123"})
	parsed, refs, err := ParseEnvelope(message)
	if err != nil {
		t.Fatal(err)
	}
	i, err := Verify(parsed)
	if err != nil {
		t.Fatal(err)
	}
	if !EqualRefs(refs, i.RestsOn) {
		t.Fatal("matching causal trailers were rejected")
	}
	if EqualRefs([]string{"git:sha1:other#git:sha1:123"}, i.RestsOn) {
		t.Fatal("altered causal trailer matched signed intent")
	}
}

func TestSignedBindingFieldsCannotBeSwapped(t *testing.T) {
	original, signed := fixture(t)
	mutations := map[string]func(*Intent){
		"target":           func(value *Intent) { value.Target = "git:sha1:other" },
		"payload tree":     func(value *Intent) { value.PayloadTree = "git:sha1:other" },
		"causal reference": func(value *Intent) { value.RestsOn = []string{"git:sha1:other#git:sha1:event"} },
		"idempotency key":  func(value *Intent) { value.IdempotencyKey = "other" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			changed := original
			mutate(&changed)
			encoded, err := Encode(changed)
			if err != nil {
				t.Fatal(err)
			}
			forged := signed
			forged.Intent = encoded
			if _, err := Verify(forged); err == nil {
				t.Fatalf("swapped %s retained the actor signature", name)
			}
		})
	}
}

func FuzzDecode(f *testing.F) {
	_, signed := fixtureForFuzz()
	f.Add(signed.Intent)
	f.Fuzz(func(t *testing.T, data []byte) {
		i, err := Decode(data)
		if err != nil {
			return
		}
		reencoded, err := Encode(i)
		if err != nil {
			t.Fatal(err)
		}
		if string(reencoded) != string(data) {
			t.Fatal("accepted a non-deterministic encoding")
		}
	})
}

func fixtureForFuzz() (Intent, Signed) {
	_, private, _ := ed25519.GenerateKey(rand.Reader)
	i := Intent{Version: Version, Target: "git:sha1:a", Schema: "s", PayloadTree: "git:sha1:b", IdempotencyNS: "n", IdempotencyKey: "k"}
	signed, _ := Sign(i, private)
	return i, signed
}
