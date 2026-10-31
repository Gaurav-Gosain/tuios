//go:build !race

package sketch

// raceEnabled reports whether the test binary was built with -race.
const raceEnabled = false
