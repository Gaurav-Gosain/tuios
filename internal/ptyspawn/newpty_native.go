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

// HostIsConPTY reports whether locally spawned panes run on a Windows ConPTY.
// xpty.NewPty returns a ConPTY on Windows, and conhost drops input CSIs it
// does not recognise (before conhost 1.22) and never answers kitty keyboard
// queries — so a pane's emulator must not offer the protocol. Remote panes
// run on a real PTY at the far end and are exempt; that is why this lives
// beside the spawn code instead of a GOOS check in the encoders.
func HostIsConPTY() bool {
	return runtime.GOOS == "windows"
}
