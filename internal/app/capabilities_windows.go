//go:build windows

package app

import (
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
