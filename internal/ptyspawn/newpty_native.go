//go:build !js

package ptyspawn

import (
	"runtime"

	"github.com/charmbracelet/x/xpty"
)

// hostPty allocates a real pseudo-terminal from the kernel.
func hostPty(width, height int) (xpty.Pty, error) {
	return xpty.NewPty(width, height)
}

// HostIsConPTY reports whether locally spawned panes run on a Windows ConPTY,
// which is what xpty.NewPty returns on Windows. Such a pane does not offer
// the kitty keyboard protocol (see vt.Emulator.DisableKittyKeyboardProtocol).
func HostIsConPTY() bool {
	return runtime.GOOS == "windows"
}
