//go:build !race

package kernel

// raceEnabled reports whether the race detector is compiled in. See
// race_on_test.go for why allocation-count assertions consult it.
const raceEnabled = false
