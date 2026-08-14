//go:build !windows

package session

import (
	"errors"
	"os"
	"syscall"
)

// lockFileExclusive takes a non-blocking exclusive flock on f. The lock is tied
// to the open file description, so it is dropped by close and by process exit,
// including a SIGKILL: a daemon that dies never leaves the lock held.
func lockFileExclusive(f *os.File) error {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return ErrDaemonStarting
		}
		return err
	}
	return nil
}
