//go:build race

package terminal

// raceEnabled reports whether the test binary was built with -race.
//
// The race detector instruments every memory access, which multiplies the cost
// of a VT write by enough to dominate any wall-clock budget. Timing assertions
// have to opt out rather than be widened, because a limit loose enough to pass
// under instrumentation would no longer fail the behaviour it exists to catch.
const raceEnabled = true
