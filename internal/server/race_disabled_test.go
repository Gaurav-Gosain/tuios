//go:build !race

package server

// raceEnabled reports whether the test binary was built with -race.
// See race_enabled_test.go for why attempt budgets consult it.
const raceEnabled = false
