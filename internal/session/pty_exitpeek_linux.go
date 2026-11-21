package session

import (
	"errors"
	"syscall"
	"time"

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
// has exited, and returns its code and signal as monitorExit records them.
// waitid from a non-parent fails with ECHILD, so the end is heard on a pidfd,
// and the status read from it (pidfdExitStatus). A process already gone when
// the pidfd would be opened leaves its status lost.
func waitForeignExit(pid int) (code, sig int) {
	fd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		return -1, 0
	}
	defer func() { _ = unix.Close(fd) }()
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}} //nolint:gosec // a descriptor number
	for {
		if _, err := unix.Poll(fds, -1); !errors.Is(err, unix.EINTR) {
			break
		}
	}
	return pidfdExitStatus(fd, foreignReapWait)
}

// foreignReapWait is how long pidfdExitStatus waits for the status once the
// process has ended. The kernel records it when the process is reaped, by its
// new parent: init or a subreaper, which reap an orphan at once.
const foreignReapWait = 2 * time.Second

// pidfdExitStatus reads the status of the ended process fd refers to, which
// Linux 6.15 and later keep for anyone holding a pidfd (PIDFD_INFO_EXIT of
// PIDFD_GET_INFO), parent or not. It is there once the process is reaped, so
// it is polled for up to wait. An older kernel refuses the ioctl (6.12 and
// before) or answers without the status and then, once the process is reaped,
// with ESRCH (6.13 and 6.14); either way the status is lost: -1, as for a
// status that is lost. So is one whose new parent never reaps it.
func pidfdExitStatus(fd int, wait time.Duration) (code, sig int) {
	deadline := time.Now().Add(wait)
	for {
		info := unix.PidfdInfo{Mask: unix.PIDFD_INFO_EXIT}
		if err := unix.IoctlPidfdInfo(fd, &info); err != nil {
			return -1, 0
		}
		if info.Mask&unix.PIDFD_INFO_EXIT != 0 {
			ws := syscall.WaitStatus(info.Exit_code) //nolint:gosec // a wait status
			if ws.Signaled() {
				return -1, int(ws.Signal())
			}
			return ws.ExitStatus(), 0
		}
		if time.Now().After(deadline) {
			return -1, 0
		}
		time.Sleep(10 * time.Millisecond)
	}
}
