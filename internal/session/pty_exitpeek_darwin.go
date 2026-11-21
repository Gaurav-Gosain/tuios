package session

import (
	"errors"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	darwinSysWaitid   = 173
	darwinPPID        = 1
	darwinWEXITED     = 0x4
	darwinWNOWAIT     = 0x20
	darwinSiginfoSize = 128
)

const canPeekExit = true

func peekExit(pid int) error {
	var info [darwinSiginfoSize]byte
	for {
		_, _, errno := syscall.Syscall6(darwinSysWaitid, darwinPPID, uintptr(pid),
			uintptr(unsafe.Pointer(&info[0])), darwinWEXITED|darwinWNOWAIT, 0, 0)
		switch errno {
		case 0:
			return nil
		case syscall.EINTR:
			continue
		default:
			return errno
		}
	}
}

func waitForeignExit(pid int) (code, sig int) {
	kq, err := unix.Kqueue()
	if err != nil {
		return -1, 0
	}
	defer func() { _ = unix.Close(kq) }()
	var change [1]unix.Kevent_t
	unix.SetKevent(&change[0], pid, unix.EVFILT_PROC, unix.EV_ADD|unix.EV_ONESHOT)
	change[0].Fflags = unix.NOTE_EXIT | unix.NOTE_EXITSTATUS
	if _, err := unix.Kevent(kq, change[:], nil, nil); err != nil {
		return -1, 0
	}
	var events [1]unix.Kevent_t
	for {
		n, err := unix.Kevent(kq, nil, events[:], nil)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil || n == 0 {
			return -1, 0
		}
		ws := syscall.WaitStatus(events[0].Data) //nolint:gosec // a wait status
		if ws.Signaled() {
			return -1, int(ws.Signal())
		}
		return ws.ExitStatus(), 0
	}
}
