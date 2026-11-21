//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package session

import (
	"syscall"

	"golang.org/x/sys/unix"
)

func newGateWaiter(master, wake int) (func(master, wake int) (bool, error), func(), error) {
	syscall.ForkLock.RLock()
	kq, err := unix.Kqueue()
	if err == nil {
		unix.CloseOnExec(kq)
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return nil, nil, err
	}
	changes := make([]unix.Kevent_t, 2)
	unix.SetKevent(&changes[0], master, unix.EVFILT_READ, unix.EV_ADD)
	unix.SetKevent(&changes[1], wake, unix.EVFILT_READ, unix.EV_ADD)
	if _, err := unix.Kevent(kq, changes, nil, nil); err != nil {
		_ = unix.Close(kq)
		return nil, nil, err
	}
	events := make([]unix.Kevent_t, 2)
	waiter := func(_, wake int) (bool, error) {
		n, err := unix.Kevent(kq, nil, events, nil)
		if err != nil {
			return false, err
		}
		for _, ev := range events[:n] {
			if int(ev.Ident) == wake {
				return true, nil
			}
		}
		return false, nil
	}
	return waiter, func() { _ = unix.Close(kq) }, nil
}
