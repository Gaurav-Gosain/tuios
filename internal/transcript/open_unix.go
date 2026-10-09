//go:build unix

package transcript

import (
	"errors"
	"os"
	"syscall"
)

// openNonBlocking opens path read-only with O_NONBLOCK, so a FIFO does not
// wait for a writer, and O_NOFOLLOW, so a symbolic link put in place of the
// file is not followed.
func openNonBlocking(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0) //nolint:gosec // the path came from the agent's own hook or from the manifest's directory
}

// isSymlinkLoop reports the error O_NOFOLLOW gives for a symbolic link.
func isSymlinkLoop(err error) bool {
	return errors.Is(err, syscall.ELOOP)
}

// setBlocking clears O_NONBLOCK on an open regular file.
func setBlocking(f *os.File) error {
	raw, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var serr error
	if err := raw.Control(func(fd uintptr) {
		serr = syscall.SetNonblock(int(fd), false)
	}); err != nil {
		return err
	}
	return serr
}
