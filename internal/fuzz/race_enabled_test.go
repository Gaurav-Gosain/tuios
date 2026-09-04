//go:build race

package fuzz

// raceEnabled reports whether the test binary was built with -race.
//
// The detector allocates as it instruments, and its own accounting moves the
// figure by more than the thing an allocation assertion measures. Such an
// assertion opts out rather than being widened, because a bound loose enough
// to survive instrumentation would no longer catch the per-action work it
// exists to catch.
const raceEnabled = true
