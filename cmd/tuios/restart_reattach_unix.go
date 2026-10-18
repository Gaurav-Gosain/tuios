//go:build !windows

package main

import (
	"fmt"
	"os"
	"syscall"
)

// execClient replaces this process with bin and args.
func execClient(bin string, args []string) error {
	if err := syscall.Exec(bin, append([]string{bin}, args...), os.Environ()); err != nil {
		return &diagnosticError{
			What: fmt.Sprintf("The daemon restarted, and tuios could not start again: %v.", err),
			Fix:  "Start tuios again. Your session is on the new daemon.",
			Err:  err,
		}
	}
	return nil
}
