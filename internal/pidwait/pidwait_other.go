//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly && !windows

package pidwait

import (
	"context"
	"os"
	"syscall"
)

// Polls is true: this platform checks the process once a second.
const Polls = true

func wait(ctx context.Context, pid int) error {
	return pollWait(ctx, pid, alive)
}

// alive reports whether a process has pid.
func alive(pid int) bool {
	p, err := os.FindProcess(pid)
	return err == nil && p.Signal(syscall.Signal(0)) == nil
}
