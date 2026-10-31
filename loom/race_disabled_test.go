//go:build !race

package loom

// raceEnabled reports whether the test binary was built with -race.
const raceEnabled = false
