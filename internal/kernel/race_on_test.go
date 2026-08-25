//go:build race

package kernel

// raceEnabled reports whether the race detector is compiled in. The
// detector adds its own bookkeeping allocation to every instrumented
// call, so an exact allocation count measured under it describes the
// instrumentation rather than the code under test. Tests that assert on
// such a count skip the assertion when this is true; the behaviour they
// surround still runs.
const raceEnabled = true
