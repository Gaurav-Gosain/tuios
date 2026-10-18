//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// execClient runs bin with args as a child on this console, waits for it and
// exits with its status. Windows has no exec.
func execClient(bin string, args []string) error {
	cmd := exec.Command(bin, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		os.Exit(exitErr.ExitCode())
	}
	if err != nil {
		return &diagnosticError{
			What: fmt.Sprintf("The daemon restarted, and tuios could not start again: %v.", err),
			Fix:  "Start tuios again. Your session is on the new daemon.",
			Err:  err,
		}
	}
	return nil
}
