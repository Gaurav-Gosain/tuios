//go:build race

package app

// raceEnabled reports whether the test binary was built with -race.
//
// Under -race sync.Pool drops a share of what is put back on purpose, so code
// that is allocation free through a pool (regexp matching is) allocates on some
// calls. An allocation assertion that reaches a pool opts out rather than being
// widened, because a bound loose enough for that would stop catching the
// per-frame allocation it exists to catch.
const raceEnabled = true
