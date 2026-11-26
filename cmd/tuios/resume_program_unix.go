//go:build !windows

package main

import (
	"os"
	"syscall"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

// execProgram replaces this process with the program, so the program is the
// shell's foreground job itself. argv[0] is the name the program reported.
func execProgram(bin string, argv []string) error {
	if err := syscall.Exec(bin, argv, os.Environ()); err != nil {
		return errfmt.Errorf("tuios cannot resume %s: %w", argv[0], err)
	}
	return nil
}
