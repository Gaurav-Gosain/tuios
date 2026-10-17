package session

import (
	"errors"

	"golang.org/x/sys/unix"
)

// canPeekExit says this platform can wait for a child's exit without reaping
// it, which a live handoff needs (see pty_reap.go).
const canPeekExit = true

// peekExit blocks until the child pid has exited and leaves it a zombie, so
// its status is still there for whoever reaps it. ECHILD means it is not a
// child of this process, or was already reaped.
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

// waitForeignExit blocks until pid, a process that is not this one's child,
// has exited. Linux tells a non-child that a process ended, through a pidfd,
// but not how: the code is -1, as for a status that is lost.
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
