package vt

import (
	"io"

	"github.com/charmbracelet/x/ansi"
)

// Focus sends the terminal a focus event if focus events mode is enabled.
// This is the opposite of [Blur].
func (e *Emulator) Focus() {
	e.focus(true)
}

// Blur sends the terminal a blur event if focus events mode is enabled.
// This is the opposite of [Focus].
func (e *Emulator) Blur() {
	e.focus(false)
}

func (e *Emulator) focus(focus bool) {
	if mode, ok := e.modes[ansi.FocusEventMode]; ok && mode.IsSet() {
		if focus {
			_, _ = io.WriteString(e.pw, ansi.Focus)
		} else {
			_, _ = io.WriteString(e.pw, ansi.Blur)
		}
	}
}
