//go:build !windows

package main

import (
	"fmt"
	"os"
	"syscall"
)

// execClient replaces this process with bin and args.
func execClient(bin string, args []string) error {
	stopCPUProfile()
	if err := syscall.Exec(bin, append([]string{bin}, args...), os.Environ()); err != nil {
		return &diagnosticError{
			What: fmt.Sprintf("The daemon is back, and tuios could not start %s on this terminal: %v.", bin, err),
			Fix:  "Start tuios again. Your session is on the daemon.",
			Err:  err,
		}
	}
	return nil
}
