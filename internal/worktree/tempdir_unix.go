//go:build unix

package worktree

import (
	"errors"
	"os"
	"syscall"
)

// processAlive reports whether a process with this id runs. A process of
// another user answers EPERM, and it runs.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// ownedByMe reports whether this user owns the folder at path.
func ownedByMe(path string) bool {
	st, err := os.Lstat(path)
	if err != nil || !st.IsDir() {
		return false
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	return ok && int(sys.Uid) == os.Getuid()
}
