//go:build race

package server

// raceEnabled reports whether the test binary was built with -race.
//
// The race detector instruments every memory access, so a test that drives
// several SSH sessions at once needs proportionally longer to get them all
// started. Attempt budgets consult this instead of being widened for every
// build, because a budget loose enough for instrumented concurrency would stop
// catching a session that never starts at all.
const raceEnabled = true
