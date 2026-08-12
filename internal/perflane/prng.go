package perflane

// PRNG is a small, repository-owned SplitMix64 generator. Its sequence is part
// of the fixture contract, so changes require updating the golden test and the
// generator version in the performance contract.
type PRNG struct {
	state uint64
}

func NewPRNG(seed uint64) *PRNG { return &PRNG{state: seed} }

func (r *PRNG) Uint64() uint64 {
	r.state += 0x9e3779b97f4a7c15
	z := r.state
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// Uint64n returns a value in [0, n) without modulo bias. It panics for n == 0,
// matching the programming-error treatment used by standard PRNG APIs.
func (r *PRNG) Uint64n(n uint64) uint64 {
	if n == 0 {
		panic("perflane: Uint64n called with zero")
	}
	threshold := -n % n
	for {
		value := r.Uint64()
		if value >= threshold {
			return value % n
		}
	}
}
