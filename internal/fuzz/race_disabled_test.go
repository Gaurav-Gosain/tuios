//go:build !race

package fuzz

// raceEnabled reports whether the test binary was built with -race.
// See race_enabled_test.go for why allocation assertions consult it.
const raceEnabled = false
