//go:build !race

package vt_test

// raceEnabled reports whether the test binary was built with -race.
// See race_enabled_test.go for why time budgets consult it.
const raceEnabled = false
