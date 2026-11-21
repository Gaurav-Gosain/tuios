//go:build darwin

package app

import (
	"time"

	"golang.org/x/sys/unix"
)

func pollReadable(fd uintptr, timeout time.Duration) (bool, error) {
	if fd >= 1024 {
		return true, nil
	}
	var rfds unix.FdSet
	rfds.Bits[fd/32] |= 1 << (fd % 32)

	tv := unix.NsecToTimeval(timeout.Nanoseconds())
	n, err := unix.Select(int(fd)+1, &rfds, nil, nil, &tv)
	if err != nil {
		if err == unix.EINTR {
			return false, errPollInterrupted
		}
		return false, err
	}
	return n > 0, nil
}
