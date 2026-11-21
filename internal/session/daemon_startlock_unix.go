//go:build !windows && !js

package session

import (
	"errors"
	"os"
	"syscall"
)

func lockFileExclusive(f *os.File) error {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return ErrDaemonStarting
		}
		return err
	}
	return nil
}
