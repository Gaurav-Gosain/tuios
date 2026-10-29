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

// lockShared waits for a shared flock on f, as Rust's File::lock_shared
// takes it on Unix.
func lockShared(f *os.File) error {
	return flockRetry(f, syscall.LOCK_SH)
}

// lockExclusive waits for an exclusive flock on f, as Rust's File::lock takes
// it on Unix.
func lockExclusive(f *os.File) error {
	return flockRetry(f, syscall.LOCK_EX)
}

// flockRetry takes a blocking flock, again when a signal interrupts the wait.
func flockRetry(f *os.File, how int) error {
	for {
		err := syscall.Flock(int(f.Fd()), how)
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}
