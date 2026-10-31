package session

import (
	"errors"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// The waitid constants of darwin's <sys/wait.h>. x/sys/unix has no Waitid on
// darwin, and release builds have no cgo, so the call is made by number.
const (
	darwinSysWaitid = 173
	darwinPPID      = 1
	darwinWEXITED   = 0x4
	darwinWNOWAIT   = 0x20
	// darwinSiginfoSize is sizeof(siginfo_t), 104 bytes, rounded up.
	darwinSiginfoSize = 128
)

// canPeekExit says this platform can wait for a child's exit without reaping
// it, which a live handoff needs (see pty_reap.go).
const canPeekExit = true

// peekExit blocks until the child pid has exited and leaves it a zombie, so
// its status is still there for whoever reaps it. ECHILD means it is not a
// child of this process, or was already reaped.
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

// waitForeignExit blocks until pid, a process that is not this one's child,
// has exited, and returns its code and signal as monitorExit records them.
// kqueue reports a non-child's real wait status with NOTE_EXITSTATUS. A
// process already gone cannot be registered (ESRCH), and its status is lost.
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
