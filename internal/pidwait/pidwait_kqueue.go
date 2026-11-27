//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package pidwait

import (
	"context"
	"errors"

	"golang.org/x/sys/unix"
)

// Polls is false: kqueue's EVFILT_PROC reports the exit.
const Polls = false

func wait(ctx context.Context, pid int) error {
	kq, err := unix.Kqueue()
	if err != nil {
		return err
	}
	unix.CloseOnExec(kq)
	defer func() { _ = unix.Close(kq) }()
	// The pipe is how a cancel wakes the goroutine parked in kevent.
	var wake [2]int
	if err := unix.Pipe(wake[:]); err != nil {
		return err
	}
	defer func() { _ = unix.Close(wake[0]); _ = unix.Close(wake[1]) }()
	changes := make([]unix.Kevent_t, 2)
	unix.SetKevent(&changes[0], pid, unix.EVFILT_PROC, unix.EV_ADD|unix.EV_ONESHOT)
	changes[0].Fflags = unix.NOTE_EXIT
	unix.SetKevent(&changes[1], wake[0], unix.EVFILT_READ, unix.EV_ADD|unix.EV_ENABLE)
	if _, err := unix.Kevent(kq, changes, nil, nil); err != nil {
		if errors.Is(err, unix.ESRCH) {
			return ErrNotRunning
		}
		return err
	}
	stop := context.AfterFunc(ctx, func() { _, _ = unix.Write(wake[1], []byte{0}) })
	defer stop()
	events := make([]unix.Kevent_t, 2)
	for {
		n, err := unix.Kevent(kq, nil, events, nil)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return err
		}
		for _, ev := range events[:n] {
			if int(ev.Ident) == wake[0] {
				return ctx.Err()
			}
			if ev.Filter == unix.EVFILT_PROC {
				return nil
			}
		}
	}
}
