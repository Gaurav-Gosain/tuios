//go:build windows

package terminal

// TriggerRedraw ensures terminal applications properly respond to resize.
// On Windows, the PTY resize itself should trigger the necessary updates.
// Windows ConPTY doesn't use SIGWINCH - it handles resize notifications automatically.
func (w *Window) TriggerRedraw() {
	// No-op on Windows - ConPTY handles resize notifications automatically
	// The Pty.Resize() call in the Resize() method is sufficient
}
