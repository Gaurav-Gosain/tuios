//go:build js

package ptyspawn

import (
	"errors"
	"os/exec"

	"github.com/charmbracelet/x/xpty"
)

// hostPty has no kernel to ask in the browser build. Every pane there runs on
// a guest pty, which the wasm entry point installs through NewGuestPty before
// the first pane opens.
func hostPty(_, _ int) (xpty.Pty, error) {
	return nil, errors.New("no guest pty in this build")
}

// configureCommand is a no-op: the guest is not a process, so there is no
// session or controlling terminal to set up.
func configureCommand(_ *exec.Cmd) {}
