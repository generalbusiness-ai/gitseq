package intent

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
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

func TestSigningBytesRequireCanonicalIntentAndReturnFreshBytes(t *testing.T) {
	original, signed := fixture(t)
	got, err := SigningBytes(signed.Intent)
	if err != nil {
		t.Fatal(err)
	}
	want := append([]byte(domainTag), signed.Intent...)
	if string(got) != string(want) {
		t.Fatal("signing bytes do not contain the domain-separated intent")
	}

	got[len(got)-1] ^= 1
	reencoded, err := Encode(original)
	if err != nil {
		t.Fatal(err)
	}
	if string(signed.Intent) != string(reencoded) {
		t.Fatal("signing bytes alias the encoded intent")
	}

	// Version zero is canonically encoded as 0x00. The two-byte form is valid
	// CBOR with the same value, but it is not core-deterministic.
	noncanonical := make([]byte, 0, len(signed.Intent)+1)
	noncanonical = append(noncanonical, signed.Intent[0], 0x18, 0x00)
	noncanonical = append(noncanonical, signed.Intent[2:]...)
	if _, err := SigningBytes(noncanonical); err == nil {
		t.Fatal("non-canonical encoded intent received signing bytes")
	}
}

func TestSignUsesThePinnedDomain(t *testing.T) {
	_, signed := fixture(t)
	pinned := append([]byte(domainTag), signed.Intent...)
	if !ed25519.Verify(ed25519.PublicKey(signed.ActorKey), pinned, signed.Signature) {
		t.Fatal("Sign did not cover the pinned domain-separated intent")
	}
}

func TestVerifyUsesThePinnedDomain(t *testing.T) {
	original, _ := fixture(t)
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := Encode(original)
	if err != nil {
		t.Fatal(err)
	}
	external := Signed{
		Intent: encoded, ActorKey: private.Public().(ed25519.PublicKey),
		Signature: ed25519.Sign(private, append([]byte(domainTag), encoded...)),
	}
	if _, err := Verify(external); err != nil {
		t.Fatalf("Verify refused the pinned domain-separated intent: %v", err)
	}
}

func TestEnvelopeRejectsAlteredCausality(t *testing.T) {
	_, signed := fixture(t)
	message := Envelope(signed, []string{"git:sha1:abc#git:sha1:123"})
	parsed, refs, err := ParseEnvelope(message, uint64(len(message)))
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

func TestIntentBoundsEveryStringAndCausalCount(t *testing.T) {
	original, _ := fixture(t)
	oversized := strings.Repeat("x", MaxStringBytes+1)
	tests := map[string]func(*Intent){
		"target":                func(value *Intent) { value.Target = oversized },
		"schema":                func(value *Intent) { value.Schema = oversized },
		"payload tree":          func(value *Intent) { value.PayloadTree = oversized },
		"idempotency namespace": func(value *Intent) { value.IdempotencyNS = oversized },
		"idempotency key":       func(value *Intent) { value.IdempotencyKey = oversized },
		"causal reference":      func(value *Intent) { value.RestsOn = []string{oversized} },
		"causal count": func(value *Intent) {
			value.RestsOn = make([]string, MaxCausalReferences+1)
			for index := range value.RestsOn {
				value.RestsOn[index] = "r"
			}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			changed := original
			mutate(&changed)
			if _, err := Encode(changed); err == nil {
				t.Fatalf("oversized %s was encoded", name)
			}
		})
	}
}

func TestEnvelopeParserUsesExplicitAggregateBound(t *testing.T) {
	original, _ := fixture(t)
	ref := strings.Repeat("r", MaxStringBytes)
	original.RestsOn = []string{ref, ref, ref}
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := Sign(original, private)
	if err != nil {
		t.Fatal(err)
	}
	message := Envelope(signed, original.RestsOn)
	if len(message) <= 64<<10 {
		t.Fatalf("test envelope is only %d bytes", len(message))
	}
	if _, _, err := ParseEnvelope(message, uint64(len(message)-1)); err == nil {
		t.Fatal("parser accepted an envelope above its explicit bound")
	}
	parsed, refs, err := ParseEnvelope(message, uint64(len(message)))
	if err != nil {
		t.Fatal(err)
	}
	if !parsed.Equal(signed) || !EqualRefs(refs, original.RestsOn) {
		t.Fatal("large bounded envelope changed during parsing")
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
