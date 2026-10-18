//go:build unix

package herdrplugin

import (
	"errors"
	"os"
	"syscall"
)

// tryLock takes a non-blocking exclusive flock on f. The kernel drops it
// when the file closes or the process ends, also on a SIGKILL.
func tryLock(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return false, nil
	}
	return err == nil, err
}

// processAlive reports whether a process with pid runs.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
