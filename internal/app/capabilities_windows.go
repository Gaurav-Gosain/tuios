//go:build windows

package app

import (
	"time"

	"golang.org/x/sys/windows"
)

func isTerminal(fd uintptr) bool {
	var mode uint32
	err := windows.GetConsoleMode(windows.Handle(fd), &mode)
	return err == nil
}

type terminalState struct {
	mode uint32
}

func makeRaw(fd uintptr) (*terminalState, error) {
	var mode uint32
	handle := windows.Handle(fd)
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		return nil, err
	}

	oldState := &terminalState{mode: mode}

	raw := mode &^ (windows.ENABLE_ECHO_INPUT | windows.ENABLE_PROCESSED_INPUT | windows.ENABLE_LINE_INPUT)
	raw |= windows.ENABLE_VIRTUAL_TERMINAL_INPUT

	if err := windows.SetConsoleMode(handle, raw); err != nil {
		return nil, err
	}

	return oldState, nil
}

func restoreTerminal(fd uintptr, oldState *terminalState) {
	if oldState != nil {
		_ = windows.SetConsoleMode(windows.Handle(fd), oldState.mode)
	}
}

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

// pollReadable on Windows - uses WaitForSingleObject
func pollReadable(fd uintptr, timeout time.Duration) (bool, error) {
	handle := windows.Handle(fd)
	timeoutMs := max(uint32(timeout.Milliseconds()), 1)

	ret, err := windows.WaitForSingleObject(handle, timeoutMs)
	if err != nil {
		return false, err
	}
	return ret == windows.WAIT_OBJECT_0, nil
}
