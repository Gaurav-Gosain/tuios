//go:build unix

package session

import (
	"errors"
	"syscall"
)

// ForegroundGroup is the process group holding the terminal of the shell
// with pid shellPid, or 0 when it cannot be read.
func ForegroundGroup(shellPid int) int {
	pgid, _ := readForegroundPGID(shellPid)
	return pgid
}

// ProcessGroupAlive reports whether any process of the group pgid is alive.
// A group this user may not signal is alive all the same.
func ProcessGroupAlive(pgid int) bool {
	if pgid <= 0 {
		return false
	}
	err := syscall.Kill(-pgid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
