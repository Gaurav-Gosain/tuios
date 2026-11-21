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

	process := w.Cmd.Process

	if process != nil {
		_ = process.Signal(os.Signal(syscall.SIGWINCH))
	}
}

func getPgid(pid int) (int, error) {
	return syscall.Getpgid(pid)
}

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
		return false
	}
	return fgpgrp != w.ShellPgid
}

// ShellAtPrompt reports whether tuios can see that the pane's own shell holds
// the terminal, which is a shell at its prompt. It fails closed: no local PTY,
// no shell pid, or no answer from the kernel is false. HasForegroundProcess is
// the opposite question and fails open.
func (w *Window) ShellAtPrompt() bool {
	if w.Pty == nil || w.ShellPgid <= 0 {
		return false
	}
	fgpgrp, ok := foregroundPgrp(w.Pty.Fd())
	return ok && fgpgrp == w.ShellPgid
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

// ForegroundPgrp is the process group that holds the window's terminal, or 0
// when the window runs no PTY of its own or the kernel gives none.
func (w *Window) ForegroundPgrp() int {
	if w == nil || w.Pty == nil {
		return 0
	}
	pgrp, _ := foregroundPgrp(w.Pty.Fd())
	return pgrp
}
