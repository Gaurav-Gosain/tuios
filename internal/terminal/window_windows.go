//go:build windows

package terminal

// TriggerRedraw ensures terminal applications properly respond to resize.
// On Windows, the PTY resize itself should trigger the necessary updates.
// Windows ConPTY doesn't use SIGWINCH - it handles resize notifications automatically.
func (w *Window) TriggerRedraw() {
	// No-op on Windows - ConPTY handles resize notifications automatically
	// The Pty.Resize() call in the Resize() method is sufficient
}

// getPgid is a stub for Windows - not supported on this platform.
// Returns an error indicating the operation is not supported.
func getPgid(_ int) (int, error) {
	// Windows doesn't have process groups in the same way Unix does
	// Fall back to showing the quit dialog if there are open terminals
	return 0, nil
}

// HasForegroundProcess is a stub for Windows - always returns false to fall back to
// the default behavior of showing the quit dialog if terminals are open.
// Full implementation would require Windows API calls to enumerate child processes.
func (w *Window) HasForegroundProcess() bool {
	// On Windows, we fall back to the simple check of "windows exist"
	// rather than trying to detect active processes
	return false
}

// SetPtyPixelSize is a stub for Windows - ConPTY doesn't support pixel dimensions.
func (w *Window) SetPtyPixelSize(cols, rows, xpixel, ypixel int) error {
	return nil
}
