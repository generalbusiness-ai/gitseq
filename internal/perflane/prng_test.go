package perflane

import "testing"

func TestPRNGGoldenSequence(t *testing.T) {
	rng := NewPRNG(0)
	want := []uint64{
		0xe220a8397b1dcdaf,
		0x6e789e6aa1b965f4,
		0x06c45d188009454f,
		0xf88bb8a8724c81ec,
		0x1b39896a51a8749b,
	}
	for index, expected := range want {
		if got := rng.Uint64(); got != expected {
			t.Fatalf("value %d = %#016x, want %#016x", index, got, expected)
		}
	}
}

func TestPRNGUint64nRangeAndDeterminism(t *testing.T) {
	left := NewPRNG(17)
	right := NewPRNG(17)
	for range 1_000 {
		got := left.Uint64n(7)
		if got >= 7 {
			t.Fatalf("Uint64n = %d", got)
		}
		if other := right.Uint64n(7); got != other {
			t.Fatalf("same seed produced %d and %d", got, other)
		}
	}
}

func TestPRNGUint64nPanicsForZero(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Uint64n(0) did not panic")
		}
	}()
	NewPRNG(0).Uint64n(0)
}
