//go:build unix

package app

import (
	"os"

	"golang.org/x/sys/unix"
)

// redirectStderr puts f on file descriptor 2, so the runtime's own panic and
// fatal error output lands in it too, not only writes through os.Stderr.
func redirectStderr(f *os.File) {
	if unix.Dup2(int(f.Fd()), 2) == nil {
		os.Stderr = f
	}
}
