//go:build unix

package main

import (
	"os"
	"syscall"
)

// run replaces this process with the command, so the command keeps the pid
// the report names.
func run(bin string, args []string) error {
	return syscall.Exec(bin, args, os.Environ())
}
