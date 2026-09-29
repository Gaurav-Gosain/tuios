//go:build unix

package session

import (
	"os"
	"syscall"
)

// stdinIsTerminal reports whether this process's standard input is the
// terminal at path, by device number.
func stdinIsTerminal(path string) bool {
	in, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	want, err := os.Stat(path)
	if err != nil {
		return false
	}
	a, ok1 := in.Sys().(*syscall.Stat_t)
	b, ok2 := want.Sys().(*syscall.Stat_t)
	if !ok1 || !ok2 || in.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	return a.Rdev == b.Rdev
}
