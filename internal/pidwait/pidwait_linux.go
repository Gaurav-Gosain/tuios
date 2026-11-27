package pidwait

import (
	"context"
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// Polls is false: a pidfd turns readable when the process exits.
const Polls = false

func wait(ctx context.Context, pid int) error {
	fd, err := unix.PidfdOpen(pid, unix.PIDFD_NONBLOCK)
	if errors.Is(err, unix.EINVAL) {
		// PIDFD_NONBLOCK came with Linux 5.10.
		fd, err = unix.PidfdOpen(pid, 0)
		if err == nil {
			err = unix.SetNonblock(fd, true)
		}
	}
	switch {
	case errors.Is(err, unix.ESRCH):
		return ErrNotRunning
	case errors.Is(err, unix.ENOSYS):
		// A kernel before 5.3 has no pidfd.
		return pollWait(ctx, pid, alive)
	case err != nil:
		return err
	}
	// On the runtime's poller the wait parks a goroutine, not a thread, and
	// Close wakes it.
	f := os.NewFile(uintptr(fd), "pidfd")
	defer func() { _ = f.Close() }()
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = f.Close() })
	defer stop()
	exited := func(fd uintptr) bool {
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}} //nolint:gosec // a descriptor number
		n, err := unix.Poll(fds, 0)
		return err == nil && n > 0
	}
	if err := rc.Read(exited); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	return nil
}

// alive reports whether a process has pid, a zombie included.
func alive(pid int) bool {
	err := unix.Kill(pid, 0)
	return err == nil || errors.Is(err, unix.EPERM)
}
