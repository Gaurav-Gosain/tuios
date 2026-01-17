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
	w.ioMu.RLock()
	process := w.Cmd.Process
	w.ioMu.RUnlock()

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

// HasForegroundProcess checks if the window's terminal has an active foreground process.
// Returns true if there's a foreground process different from the shell itself.
// Returns false if only the shell is running or if unable to determine.
func (w *Window) HasForegroundProcess() bool {
	if w.Pty == nil || w.ShellPgid <= 0 {
		return false
	}

	// Get the PTY file descriptor
	ptyFd := w.Pty.Fd()

	// Get the foreground process group of the PTY
	// Using tcgetpgrp syscall via ioctl
	var fgpgrp int
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		ptyFd,
		uintptr(unix.TIOCGPGRP),
		uintptr(unsafe.Pointer(&fgpgrp)),
	)

	if errno != 0 {
		// If we can't determine, assume no foreground process
		return false
	}

	// If foreground process group is different from shell's process group,
	// there's an active foreground process running
	return fgpgrp != w.ShellPgid
}

// SetPtyPixelSize sets the pixel dimensions on the PTY using TIOCSWINSZ.
// This enables applications like kitty icat to query terminal size in pixels.
// The cols and rows are the character dimensions, xpixel and ypixel are pixel dimensions.
func (w *Window) SetPtyPixelSize(cols, rows, xpixel, ypixel int) error {
	if w.Pty == nil {
		return nil
	}

	ws := unix.Winsize{
		Row:    uint16(rows),
		Col:    uint16(cols),
		Xpixel: uint16(xpixel),
		Ypixel: uint16(ypixel),
	}

	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		w.Pty.Fd(),
		uintptr(unix.TIOCSWINSZ),
		uintptr(unsafe.Pointer(&ws)),
	)

	if errno != 0 {
		return errno
	}
	return nil
}
