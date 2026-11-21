package session

import (
	"errors"

	"golang.org/x/sys/unix"
)

const canPeekExit = true

func peekExit(pid int) error {
	var info unix.Siginfo
	for {
		err := unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		return err
	}
}

func waitForeignExit(pid int) (code, sig int) {
	fd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		return -1, 0
	}
	defer func() { _ = unix.Close(fd) }()
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}} //nolint:gosec // a descriptor number
	for {
		if _, err := unix.Poll(fds, -1); !errors.Is(err, unix.EINTR) {
			return -1, 0
		}
	}
}
