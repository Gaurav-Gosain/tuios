package session

import "golang.org/x/sys/unix"

// pendingBytes is how many bytes the kernel holds for a reader of fd, or -1
// when that cannot be read. FIONREAD reads 0 on a darwin PTY master however
// much is waiting, so the count comes from kqueue's EVFILT_READ instead.
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
