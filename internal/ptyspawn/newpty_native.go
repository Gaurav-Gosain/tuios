//go:build !js

package ptyspawn

import "github.com/charmbracelet/x/xpty"

// hostPty allocates a real pseudo-terminal from the kernel.
func hostPty(width, height int) (xpty.Pty, error) {
	return xpty.NewPty(width, height)
}
