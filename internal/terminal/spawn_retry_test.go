package terminal

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"testing"

	"github.com/charmbracelet/x/xpty"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/ptyspawn"
)

// This is the standalone half of the spawn story. A pane created without a
// daemon used to allocate its own PTY and start its own process inline, with no
// retry, so the kernel's transient controlling-terminal refusal came back as a
// nil window: a pane the user asked for that never appeared, and no error
// anywhere saying why. The daemon path had learned about the refusal months
// earlier; this path had not, because the knowledge lived in a copy.
//
// Negative control, confirmed against the tree before this change: both tests
// fail there, and they fail on the injection never being consumed. That is the
// finding stated as an assertion - NewWindow called xpty.NewPty and pty.Start
// itself, so a refusal aimed at the shared door never reached it, and no policy
// written at that door could have covered this path.

// failNextStarts makes the next n starts fail with err, then delegates to the
// real start. It returns a counter of injected failures.
func failNextStarts(t *testing.T, n int, err error) *int {
	t.Helper()
	real := ptyspawn.StartProcess
	injected := 0
	ptyspawn.StartProcess = func(p xpty.Pty, cmd *exec.Cmd) error {
		if injected < n {
			injected++
			return err
		}
		return real(p, cmd)
	}
	t.Cleanup(func() { ptyspawn.StartProcess = real })
	return &injected
}

func TestStandalonePaneSurvivesATransientSpawnEPERM(t *testing.T) {
	injected := failNextStarts(t, 1, fmt.Errorf("fork/exec /bin/sh: %w", syscall.EPERM))

	exitChan := make(chan string, 1)
	window, err := NewWindow("spawn-eperm-0001", "Test", 0, 0, 80, 24, 0, exitChan, nil, config.DefaultScrollbackLines)
	if err != nil {
		t.Fatalf("a one-shot EPERM left the pane with no shell; the standalone path is not going through the retry: %v", err)
	}
	t.Cleanup(func() { window.Close() })

	if *injected != 1 {
		t.Fatalf("the injection was consumed %d times, want 1", *injected)
	}
	if window.Pty == nil || window.Cmd == nil || window.Cmd.Process == nil {
		t.Fatal("the retried pane has no running process")
	}
}

// A refusal that does not go away still has to end the pane rather than loop.
func TestStandalonePaneGivesUpOnAPersistentSpawnEPERM(t *testing.T) {
	injected := failNextStarts(t, ptyspawn.Attempts+2, fmt.Errorf("fork/exec /bin/sh: %w", syscall.EPERM))

	exitChan := make(chan string, 1)
	window, err := NewWindow("spawn-eperm-0002", "Test", 0, 0, 80, 24, 0, exitChan, nil, config.DefaultScrollbackLines)
	if err == nil {
		window.Close()
		t.Fatal("a persistent EPERM produced a pane anyway; a real refusal must surface")
	}
	if !errors.Is(err, syscall.EPERM) {
		t.Fatalf("the surfaced error lost its cause: %v", err)
	}
	if *injected != ptyspawn.Attempts {
		t.Fatalf("spawn was attempted %d times, want the bound %d", *injected, ptyspawn.Attempts)
	}
}
