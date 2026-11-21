//go:build unix

package transcript

import (
	"errors"
	"os"
	"syscall"
)

func openNonBlocking(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0) //nolint:gosec // the path came from the agent's own hook or from the manifest's directory
}

func isSymlinkLoop(err error) bool {
	return errors.Is(err, syscall.ELOOP)
}

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
