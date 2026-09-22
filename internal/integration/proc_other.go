//go:build !linux && !darwin

package integration

import "os"

// Other platforms have no session id a pane's shell leads and no portable
// parent read, so only the immediate parent is offered.
func selfSID() int { return 0 }

func parentPID(pid int) int {
	if pid == 0 {
		return os.Getppid()
	}
	return 0
}

// processName has no portable source here, so every name is unknown and
// HarnessPID takes the nearest ancestor.
func processName(int) string { return "" }
