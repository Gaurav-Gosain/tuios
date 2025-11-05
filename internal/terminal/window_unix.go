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

	// Send SIGWINCH signal after a small delay to ensure PTY resize has completed
	// and give the shell time to process the new size before the signal arrives
	go func() {
		time.Sleep(20 * time.Millisecond)

		w.ioMu.RLock()
		process := w.Cmd.Process
		w.ioMu.RUnlock()

		if process != nil {
			// Send SIGWINCH (window change signal) to the process
			// Applications should handle this and redraw as needed
			_ = process.Signal(os.Signal(syscall.SIGWINCH)) // Best effort, ignore error
		}
	}()
}
