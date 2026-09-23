//go:build !js

package ptyspawn

import "github.com/charmbracelet/x/xpty"

// newPty allocates a real pseudo-terminal from the kernel.
func newPty(width, height int) (xpty.Pty, error) {
	return xpty.NewPty(width, height)
}
