//go:build windows

package app

import (
	"slices"
	"time"

	xwindows "github.com/charmbracelet/x/windows"
	"golang.org/x/sys/windows"
)

// queryTerminalSize gets the terminal columns and rows on Windows
func queryTerminalSize(caps *HostCapabilities) {
	handle := windows.Handle(windows.Stdout)
	var info windows.ConsoleScreenBufferInfo
	if err := windows.GetConsoleScreenBufferInfo(handle, &info); err != nil {
		return
	}
	caps.Cols = int(info.Window.Right - info.Window.Left + 1)
	caps.Rows = int(info.Window.Bottom - info.Window.Top + 1)
}

// pollReadable waits until a read of fd has text to return, or the timeout
// runs out.
//
// A console input handle is signalled while it holds any input record, but a
// read returns only text and blocks through the rest: a focus change, which a
// new Windows Terminal tab sends as it opens, a mouse move, or a key release.
// Taking such a record as readable left the probe's read blocked past its
// timeout, until a host that never answers was sent a key. Those records are
// read and dropped here. Nothing needs them: the probe runs before the
// program, which sets its own input modes and asks for what it needs.
//
// A handle that is not a console, such as a pipe, is readable once signalled.
func pollReadable(fd uintptr, timeout time.Duration) (bool, error) {
	handle := windows.Handle(fd)
	deadline := time.Now().Add(timeout)
	for {
		// Clamped before the conversion: the deadline can pass between the
		// check below and here, and a negative wait made unsigned is 49 days.
		timeoutMs := uint32(max(time.Until(deadline).Milliseconds(), 1))
		ret, err := windows.WaitForSingleObject(handle, timeoutMs)
		if err != nil {
			return false, err
		}
		if ret != windows.WAIT_OBJECT_0 {
			return false, nil
		}
		text, err := dropNonText(handle)
		if err != nil || text {
			return true, nil
		}
		if !time.Now().Before(deadline) {
			return false, nil
		}
	}
}

// dropNonText looks at the records at the front of a console's input buffer
// and reports whether one holds text, which it leaves for the read. If none
// does, it reads and drops them; the caller waits again for any behind them,
// so the deadline is checked between batches. A handle that is not a console
// input is an error.
func dropNonText(handle windows.Handle) (bool, error) {
	var records [16]xwindows.InputRecord
	var n uint32
	if err := xwindows.PeekConsoleInput(handle, &records[0], uint32(len(records)), &n); err != nil {
		return false, err
	}
	if n == 0 {
		return false, nil
	}
	if slices.ContainsFunc(records[:n], isTextRecord) {
		// A read skips the records in front of the text by itself.
		return true, nil
	}
	return false, xwindows.ReadConsoleInput(handle, &records[0], n, &n)
}

// isTextRecord reports whether a read of the console returns something for r.
// With VT input on, a terminal's reply arrives as key presses that carry its
// characters.
func isTextRecord(r xwindows.InputRecord) bool {
	if r.EventType != windows.KEY_EVENT {
		return false
	}
	key := r.KeyEvent()
	return key.KeyDown && key.Char != 0
}
