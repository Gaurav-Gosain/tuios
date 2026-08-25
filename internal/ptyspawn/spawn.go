// Package ptyspawn is the one door every PTY-backed process in tuios goes
// through. Allocating the pty, giving the child its controlling terminal, and
// starting it are one operation here, because they have one failure mode that
// has to be handled in one place.
//
// That failure mode is a transient EPERM from the kernel's controlling-terminal
// grab (ioctl TIOCSCTTY in the forked child). Captured under strace on Linux
// 7.2, the child is a clean session leader (setsid succeeded) holding the
// freshly opened slave at fd 0, no LSM is filtering, and the identical grab on a
// fresh pty succeeds milliseconds later. Every capture involved the just-freed
// lowest pts index while other shells were being SIGKILLed, so this is the pts
// index being recycled out from under a session that is still being torn down.
//
// It is treated the way EINTR is treated: retried, bounded, and never quietly.
// The retry allocates a new pty rather than reusing the refused one, because the
// refusal is about that index. A persistent EPERM still surfaces at the bound,
// and no other error is ever retried.
//
// The daemon path and the standalone path both call Spawn. They used to carry
// their own copy of this sequence, and only one of them learned about the
// refusal; a pane whose shell never started was the standalone copy's share of
// the same kernel behaviour.
package ptyspawn

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"

	"github.com/charmbracelet/x/xpty"
)

// Attempts bounds the retries of a spawn refused with EPERM. Two retries is
// already one more than any captured refusal needed: in every strace capture
// the second attempt succeeded.
const Attempts = 3

// StartProcess is the seam a test injects the kernel's refusal through. The
// real event cannot be provoked on purpose - it needs a pts index to be
// recycled out from under a dying session at the exact moment of the fork - so
// tests replace this to deliver an EPERM the way the kernel delivers it.
// Production code never assigns to it.
var StartProcess = func(p xpty.Pty, cmd *exec.Cmd) error {
	return p.Start(cmd)
}

// Spawn allocates a PTY of the given size, builds the command with build,
// configures it to take that PTY as its controlling terminal, and starts it.
//
// build is called once per attempt and must return a fresh *exec.Cmd every
// time: an exec.Cmd that failed to start cannot be started again. It must not
// set SysProcAttr; Spawn owns that, because that is the part the refusal is
// about.
//
// logf, when non-nil, gets one line per absorbed refusal, so a retry shows up in
// whichever log the calling package already writes.
//
// On success the caller owns both returned values, including closing the PTY.
// On failure nothing is left open.
func Spawn(width, height int, build func() *exec.Cmd, logf func(string, ...any)) (xpty.Pty, *exec.Cmd, error) {
	for attempt := 1; ; attempt++ {
		pty, err := xpty.NewPty(width, height)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create PTY: %w", err)
		}

		cmd := build()
		configureCommand(cmd)

		if err = StartProcess(pty, cmd); err == nil {
			return pty, cmd, nil
		}
		_ = pty.Close()

		if attempt < Attempts && errors.Is(err, syscall.EPERM) {
			if logf != nil {
				logf("[PTY] transient EPERM starting %s (attempt %d), retrying on a fresh PTY", cmd.Path, attempt)
			}
			time.Sleep(time.Duration(attempt) * time.Millisecond)
			continue
		}
		// Name the process that failed: with a command this is the program the
		// user picked, and "shell" would send them looking at the wrong config.
		return nil, nil, fmt.Errorf("failed to start %s: %w", cmd.Path, err)
	}
}
