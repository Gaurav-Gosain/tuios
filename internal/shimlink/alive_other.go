//go:build !unix && !windows

package shimlink

func processAlive(int) bool { return false }
