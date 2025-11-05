//go:build unix || linux || darwin || freebsd || openbsd || netbsd

package terminal

import (
	"os"
	"syscall"
	"time"
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
