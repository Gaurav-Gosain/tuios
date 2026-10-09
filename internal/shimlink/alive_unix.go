//go:build unix

package shimlink

import (
	"errors"
	"syscall"
)

// processAlive reports whether a process with this id runs. A process of
// another user answers EPERM, and it runs.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
