//go:build !js && !windows

package ptyspawn

import (
	"github.com/charmbracelet/x/xpty"
)

// hostPty allocates a real pseudo-terminal from the kernel.
func hostPty(width, height int, _ func(string, ...any)) (xpty.Pty, error) {
	return xpty.NewPty(width, height)
}

// HostIsConPTY reports whether locally spawned panes run on a Windows ConPTY.
// They do only on Windows (see newpty_windows.go).
func HostIsConPTY() bool { return false }
