//go:build unix || linux || darwin || freebsd || openbsd || netbsd

package terminal

import (
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// TriggerRedraw ensures terminal applications properly respond to resize.
// This sends SIGWINCH signal to notify applications of the size change.
func (w *Window) TriggerRedraw() {
	if w.Cmd == nil || w.Cmd.Process == nil {
		return
	}

	// Send SIGWINCH signal immediately to notify the shell of the resize.
	// PTY.Resize() is synchronous, so the kernel PTY size is updated immediately.
	// Shells query the new size via ioctl(TIOCGWINSZ) when they receive SIGWINCH.
	//
	// No lock: w.Cmd is write-once, so ioMu guarded nothing here. Signalling a
	// process Close() already reaped just returns an error, which is ignored.
	process := w.Cmd.Process

	if process != nil {
		// Send SIGWINCH (window change signal) to the process
		// Applications should handle this and redraw as needed
		_ = process.Signal(os.Signal(syscall.SIGWINCH)) // Best effort, ignore error
	}
}

// getPgid gets the process group ID of a given process ID.
// Returns the PGID or an error if unable to determine it.
func getPgid(pid int) (int, error) {
	return syscall.Getpgid(pid)
}

// foregroundPgrp returns the foreground process group of the terminal open on
// fd, and whether the kernel gave one.
//
// The kernel writes a pid_t, which is 32 bits on every unix Go supports, so it
// is read into an int32. Reading it into a Go int, which is 64 bits, leaves the
// upper half zero on little-endian and gets the wrong number on big-endian.
func foregroundPgrp(fd uintptr) (int, bool) {
	var pgrp int32
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		fd,
		uintptr(unix.TIOCGPGRP),
		uintptr(unsafe.Pointer(&pgrp)),
	)
	if errno != 0 || pgrp <= 0 {
		return 0, false
	}
	return int(pgrp), true
}

// HasForegroundProcess checks if the window's terminal has an active foreground process.
// Returns true if there's a foreground process different from the shell itself.
// Returns false if only the shell is running or if unable to determine.
func (w *Window) HasForegroundProcess() bool {
	if w.Pty == nil || w.ShellPgid <= 0 {
		return false
	}
	fgpgrp, ok := foregroundPgrp(w.Pty.Fd())
	if !ok {
		// If we can't determine, assume no foreground process
		return false
	}
	// If foreground process group is different from shell's process group,
	// there's an active foreground process running
	return fgpgrp != w.ShellPgid
}

// ForegroundCommand returns the command name of the pane's foreground process
// group, or "" when it cannot be read.
//
// This is the one thing tuios can observe about what is running inside a pane
// rather than infer. The pane's program is asked nothing: the kernel is asked
// which process group owns the terminal, and then what that group is called
// (procComm: /proc on Linux, a sysctl on macOS). Callers must treat "" as "not
// known" rather than as "nothing is running".
//
// Uncached on purpose, unlike CWD. Nothing on the render path calls this: it is
// read when the keybind overlay opens and when a key is recorded, which is a
// handful of times per session rather than once per window per frame. A cache
// here would only buy a stale answer at the one moment the answer matters.
func (w *Window) ForegroundCommand() string {
	if w.Pty == nil {
		return ""
	}
	fgpgrp, ok := foregroundPgrp(w.Pty.Fd())
	if !ok {
		return ""
	}
	return procComm(fgpgrp)
}
