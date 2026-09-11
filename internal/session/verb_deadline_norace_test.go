//go:build !race

package session

// testDeadlineScale stretches the test client's socket deadlines. The race
// detector makes everything several times slower, and a deadline written for an
// ordinary build is not a statement about the code under a detector.
const testDeadlineScale = 1
