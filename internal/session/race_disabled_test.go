//go:build !race

package session

// raceEnabled reports whether the test binary was built with -race.
const raceEnabled = false
