//go:build js

package ptyspawn

import (
	"errors"
	"os/exec"

	"github.com/charmbracelet/x/xpty"
)

// NewGuestPty makes the in-memory terminal a pane runs on in the browser
// build, where there is no kernel pty and no process to exec. The pty it
// returns runs a Go program in place of cmd when Spawn calls Start. The wasm
// entry point sets it before the first pane opens.
var NewGuestPty func(width, height int) (xpty.Pty, error)

func newPty(width, height int) (xpty.Pty, error) {
	if NewGuestPty == nil {
		return nil, errors.New("no guest pty in this build")
	}
	return NewGuestPty(width, height)
}

// configureCommand is a no-op: the guest is not a process, so there is no
// session or controlling terminal to set up.
func configureCommand(_ *exec.Cmd) {}
