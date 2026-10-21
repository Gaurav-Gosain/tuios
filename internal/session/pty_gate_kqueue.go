//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package session

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// newGateWaiter returns a read gate's wait on a kqueue of its own, with the
// master and the wake pipe registered once, here. On macOS poll(2) builds and
// tears down a kqueue on every call, and the reader waits once per read: a
// flood on macOS reads about 1 KiB at a time, so a poll per read cost a flood
// about a millisecond of CPU per MB and a tenth of its throughput
// (docs/design/pane-read-path.md). The BSDs have the same kqueue, and wait on
// it the same way.
//
// The registration lasts as long as the master: closing a descriptor drops
// its registration, so a number the master gave up is not watched. Between a
// close and the reader's exit, ControlMaster reports the master closed before
// the wait is reached.
func newGateWaiter(master, wake int) (func(master, wake int) (bool, error), func(), error) {
	// Under ForkLock, as the os package makes its descriptors, so a handoff
	// that execs between the two calls does not carry the kqueue into the new
	// image.
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
