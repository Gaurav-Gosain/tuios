//go:build windows

package app

import (
	"slices"
	"time"

	xwindows "github.com/charmbracelet/x/windows"
	"golang.org/x/sys/windows"
)

func queryTerminalSize(caps *HostCapabilities) {
	handle := windows.Handle(windows.Stdout)
	var info windows.ConsoleScreenBufferInfo
	if err := windows.GetConsoleScreenBufferInfo(handle, &info); err != nil {
		return
	}
	caps.Cols = int(info.Window.Right - info.Window.Left + 1)
	caps.Rows = int(info.Window.Bottom - info.Window.Top + 1)
}

func pollReadable(fd uintptr, timeout time.Duration) (bool, error) {
	handle := windows.Handle(fd)
	deadline := time.Now().Add(timeout)
	for {
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
		return true, nil
	}
	return false, xwindows.ReadConsoleInput(handle, &records[0], n, &n)
}

func isTextRecord(r xwindows.InputRecord) bool {
	if r.EventType != windows.KEY_EVENT {
		return false
	}
	key := r.KeyEvent()
	return key.KeyDown && key.Char != 0
}
