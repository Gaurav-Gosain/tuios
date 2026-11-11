//go:build linux || freebsd || netbsd || openbsd

package app

import (
	"time"

	"golang.org/x/sys/unix"
)

// pollReadable uses poll to wait for the file descriptor to be readable with a timeout
func pollReadable(fd uintptr, timeout time.Duration) (bool, error) {
	fds := []unix.PollFd{
		{Fd: int32(fd), Events: unix.POLLIN},
	}
	timeoutMs := max(int(timeout.Milliseconds()), 1)

	n, err := unix.Poll(fds, timeoutMs)
	if err == unix.EINTR {
		return false, errPollInterrupted
	}
	if err != nil {
		return false, err
	}
	return n > 0 && (fds[0].Revents&unix.POLLIN) != 0, nil
}
