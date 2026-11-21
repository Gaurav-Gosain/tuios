//go:build !windows && !js

package session

import (
	"os"
	"syscall"
)

const oNoFollow = syscall.O_NOFOLLOW

const oNonBlock = syscall.O_NONBLOCK

func syncData(f *os.File) error {
	for {
		err := syscall.Fsync(int(f.Fd()))
		if err != syscall.EINTR {
			return err
		}
	}
}
