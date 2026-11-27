// Package pidwait waits for a process that is not a child of the caller to
// exit.
//
// os.Process.Wait and waitid wait only for a child. A script that started a
// build in the background, or a lead that waits on a landing job another
// shell started, needs to wait on any process it can see. A loop of kill -0
// and sleep wakes the waiter for nothing, and a pgrep loop can match its own
// command line and never end. Here the kernel says when the process ends: a
// pidfd on Linux, kqueue EVFILT_PROC on macOS and the BSDs, and the process
// handle on Windows. Other platforms check once a second, and Polls says so.
package pidwait

import (
	"context"
	"errors"
)

// ErrNotRunning is returned when no process has the pid when the wait
// starts. It may have exited already.
var ErrNotRunning = errors.New("pidwait: no such process")

// Wait blocks until the process pid exits, or ctx ends. It returns nil when
// the process exited, ErrNotRunning when there was no such process to start
// with, and ctx.Err() when ctx ended first. The exit status is not known: a
// process that is not a child cannot be reaped.
func Wait(ctx context.Context, pid int) error {
	if pid <= 0 {
		return ErrNotRunning
	}
	return wait(ctx, pid)
}
