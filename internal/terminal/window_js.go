//go:build js

package terminal

// The browser build has no processes or process groups. A pane runs an
// in-memory guest (see ptyspawn.NewGuestPty), and resizing that guest's pty
// already tells it the new size.

// TriggerRedraw is a no-op: the guest pty's Resize reaches the guest directly.
func (w *Window) TriggerRedraw() {}

func getPgid(_ int) (int, error) { return 0, nil }

// HasForegroundProcess reports false, so quitting asks the same question it
// asks on Windows.
func (w *Window) HasForegroundProcess() bool { return false }

// ForegroundCommand has no process table to consult.
func (w *Window) ForegroundCommand() string { return "" }
