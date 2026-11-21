//go:build windows

package ptyspawn

import (
	"os/exec"
)

func configureCommand(_ *exec.Cmd) {}
