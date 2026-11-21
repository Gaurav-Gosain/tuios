package session

import "golang.org/x/sys/unix"

func pendingBytes(fd int) int {
	kq, err := unix.Kqueue()
	if err != nil {
		return -1
	}
	defer unix.Close(kq)
	change := []unix.Kevent_t{{Ident: uint64(fd), Filter: unix.EVFILT_READ, Flags: unix.EV_ADD | unix.EV_ONESHOT}} //nolint:gosec // a descriptor number
	events := make([]unix.Kevent_t, 1)
	n, err := unix.Kevent(kq, change, events, &unix.Timespec{})
	if err != nil {
		return -1
	}
	if n == 0 {
		return 0
	}
	return int(events[0].Data)
}
