//go:build unix

package herdrplugin

import (
	"errors"
	"os"
	"syscall"
)

func tryLock(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return false, nil
	}
	return err == nil, err
}

func lockShared(f *os.File) error {
	return flockRetry(f, syscall.LOCK_SH)
}

func lockExclusive(f *os.File) error {
	return flockRetry(f, syscall.LOCK_EX)
}

func flockRetry(f *os.File, how int) error {
	for {
		err := syscall.Flock(int(f.Fd()), how)
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}
