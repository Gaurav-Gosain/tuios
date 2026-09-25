//go:build windows
// +build windows

package fang

import (
	"fmt"
	"io"

	"github.com/charmbracelet/x/term"
	"golang.org/x/sys/windows"
)

func enableVirtualTerminalProcessing(w io.Writer) error {
	f, ok := w.(term.File)
	if !ok || !term.IsTerminal(f.Fd()) {
		return nil
	}
	var mode uint32
	if err := windows.GetConsoleMode(windows.Handle(f.Fd()), &mode); err != nil {
		return fmt.Errorf("error getting console mode: %w", err)
	}

	if err := windows.SetConsoleMode(windows.Handle(f.Fd()),
		mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING); err != nil {
		return fmt.Errorf("error setting console mode: %w", err)
	}

	return nil
}
