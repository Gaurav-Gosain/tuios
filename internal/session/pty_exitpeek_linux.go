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
