//go:build windows

package session

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func lockFileExclusive(f *os.File) error {
	overlapped := new(windows.Overlapped)
	err := windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, overlapped,
	)
	if err != nil {
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
			return ErrDaemonStarting
		}
		return err
	}
	return nil
}
