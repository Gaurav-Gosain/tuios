package session

import (
	"syscall"
	"unsafe"
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
