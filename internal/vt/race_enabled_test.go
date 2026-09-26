//go:build race

package vt_test

// raceEnabled reports whether the test binary was built with -race.
//
// The race detector instruments every memory access, which multiplies the
// cost of a write by enough to dominate any wall-clock budget: the nightly
// race build failed a 1500ms budget that the uninstrumented build meets in a
// few milliseconds. A time budget opts out rather than being widened, because
// a limit loose enough to pass under instrumentation would no longer catch the
// regression. What the budget pins is also held by an allocation budget, which
// the detector does not move.
const raceEnabled = true
