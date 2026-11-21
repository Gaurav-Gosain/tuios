//go:build unix

package app

import (
	"os"

	"golang.org/x/sys/unix"
)

func redirectStderr(f *os.File) {
	if unix.Dup2(int(f.Fd()), 2) == nil {
		os.Stderr = f
	}
}
