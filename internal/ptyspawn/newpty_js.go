//go:build js

package ptyspawn

import (
	"errors"
	"os/exec"

	"github.com/charmbracelet/x/xpty"
)

func hostPty(_, _ int, _ func(string, ...any)) (xpty.Pty, error) {
	return nil, errors.New("no guest pty in this build")
}

func configureCommand(_ *exec.Cmd) {}

// HostIsConPTY mirrors newpty_native.go: the browser build spawns no local
// panes, so none of them can sit on a ConPTY.
func HostIsConPTY() bool { return false }
