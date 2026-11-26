//go:build windows

package main

import (
	"errors"
	"os"
	"os/exec"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

// execProgram runs the program and exits with its status. Windows has no
// exec, so this process waits in its place.
func execProgram(bin string, argv []string) error {
	cmd := exec.Command(bin, argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		os.Exit(exitErr.ExitCode())
	}
	if err != nil {
		return errfmt.Errorf("tuios cannot resume %s: %w", argv[0], err)
	}
	return nil
}
