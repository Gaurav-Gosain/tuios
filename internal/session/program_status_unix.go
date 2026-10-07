//go:build unix

package session

import (
	"errors"
	"syscall"
)

// processGroupAlive reports whether any process is left in process group
// pgid. A signal 0 sent to the group checks without delivering anything; a
// group whose members belong to another user answers EPERM, which still
// means it exists.
func processGroupAlive(pgid int) bool {
	if pgid <= 0 {
		return false
	}
	err := syscall.Kill(-pgid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
