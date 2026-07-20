//go:build !race

package terminal

// raceEnabled reports whether the test binary was built with -race.
// See race_enabled_test.go for why timing assertions consult it.
const raceEnabled = false
