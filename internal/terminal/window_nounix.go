//go:build !unix

package terminal

// Windows and the browser build have no process group holding the terminal
// the way a Unix PTY has one. On Windows ConPTY tells the program of a
// resize itself. In the browser a pane runs an in-memory guest (see
// ptyspawn.NewGuestPty), and resizing the guest's pty tells it the new size.

// TriggerRedraw is a no-op: the pty's Resize already reaches the program.
func (w *Window) TriggerRedraw() {}

// getPgid has no process group to read.
func getPgid(_ int) (int, error) { return 0, nil }

// HasForegroundProcess reports false, so quitting falls back to asking
// whenever a terminal is open.
func (w *Window) HasForegroundProcess() bool { return false }

// ForegroundCommand has no process table to consult, so the keybind report
// has no observed program name and says so rather than guessing one.
func (w *Window) ForegroundCommand() string { return "" }

// ShellAtPrompt is false: this platform does not say what holds the terminal.
func (w *Window) ShellAtPrompt() bool { return false }

// ForegroundPgrp is 0: no process group holds the terminal.
func (w *Window) ForegroundPgrp() int { return 0 }
