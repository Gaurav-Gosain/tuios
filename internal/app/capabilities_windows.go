//go:build windows

package app

import (
	"time"

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

// pollReadable on Windows uses WaitForSingleObject.
func pollReadable(fd uintptr, timeout time.Duration) (bool, error) {
	handle := windows.Handle(fd)
	timeoutMs := max(uint32(timeout.Milliseconds()), 1)

	ret, err := windows.WaitForSingleObject(handle, timeoutMs)
	if err != nil {
		return false, err
	}
	return ret == windows.WAIT_OBJECT_0, nil
}
