//go:build !unix && !windows

package shimlink

// processAlive cannot tell here, so it answers that the process ended.
func processAlive(int) bool { return false }
