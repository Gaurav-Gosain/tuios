//go:build windows

package main

import (
	"os"

	"github.com/charmbracelet/x/term"
)

// watchSize sends the size of the terminal once. tuios runs the plugin on
// Linux and macOS only.
func watchSize(msgs chan<- Msg) {
	go func() {
		if w, h, err := term.GetSize(os.Stdout.Fd()); err == nil {
			msgs <- sizeMsg{Width: w, Height: h}
		}
	}()
}
