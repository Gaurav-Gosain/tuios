//go:build windows

package herdrplugin

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// tryLock takes a non-blocking exclusive byte-range lock on f. Windows drops
// it when the handle closes, also on an abnormal exit.
func tryLock(f *os.File) (bool, error) {
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, new(windows.Overlapped))
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
		return false, nil
	}
	return err == nil, err
}

// lockShared waits for a shared lock on all of f, as Rust's File::lock_shared
// takes it on Windows: LockFileEx over the whole range.
func lockShared(f *os.File) error {
	return lockRange(f, 0)
}

// lockExclusive waits for an exclusive lock on all of f, as Rust's File::lock
// takes it on Windows.
func lockExclusive(f *os.File) error {
	return lockRange(f, windows.LOCKFILE_EXCLUSIVE_LOCK)
}

func lockRange(f *os.File, flags uint32) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, ^uint32(0), ^uint32(0), new(windows.Overlapped))
}
