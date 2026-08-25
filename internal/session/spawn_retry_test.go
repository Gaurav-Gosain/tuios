package session

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"testing"

	"github.com/charmbracelet/x/xpty"

	"github.com/Gaurav-Gosain/tuios/internal/ptyspawn"
)

// These tests pin what the daemon's spawn path does about the kernel's
// transient controlling-terminal refusal, by injecting it at the one door every
// PTY-backed process goes through. The policy itself is pinned in
// internal/ptyspawn; what is under test here is that this path goes through
// that door - the thing that stopped being true of the standalone path when the
// policy lived in only one of two copies of the same code.

// failStarts makes the next n starts fail with err, then delegates to the real
// start. It returns a restore function and a counter of injected failures.
func failStarts(t *testing.T, n int, err error) *int {
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

func TestTransientSpawnEPERMIsRetriedOnAFreshPTY(t *testing.T) {
	s := newTestSession(t)
	epermLike := fmt.Errorf("fork/exec /bin/sh: %w", syscall.EPERM)
	injected := failStarts(t, 1, epermLike)

	w, err := s.AddDaemonWindow("survivor", nil)
	if err != nil {
		t.Fatalf("a one-shot EPERM was not absorbed by the retry: %v", err)
	}
	if *injected != 1 {
		t.Fatalf("the injection was consumed %d times, want 1", *injected)
	}
	if pty := s.GetPTY(w.PTYID); pty == nil {
		t.Fatal("the retried window has no PTY registered")
	}
}

func TestPersistentSpawnEPERMStillFails(t *testing.T) {
	s := newTestSession(t)
	epermLike := fmt.Errorf("fork/exec /bin/sh: %w", syscall.EPERM)
	injected := failStarts(t, ptyspawn.Attempts+2, epermLike)

	_, err := s.AddDaemonWindow("refused", nil)
	if err == nil {
		t.Fatal("a persistent EPERM was swallowed; a real refusal must surface")
	}
	if !errors.Is(err, syscall.EPERM) {
		t.Fatalf("the surfaced error lost its cause: %v", err)
	}
	if *injected != ptyspawn.Attempts {
		t.Fatalf("spawn was attempted %d times, want the bound %d", *injected, ptyspawn.Attempts)
	}
}

func TestNonEPERMSpawnErrorIsNotRetried(t *testing.T) {
	s := newTestSession(t)
	otherErr := fmt.Errorf("fork/exec /bin/sh: %w", syscall.ENOENT)
	injected := failStarts(t, ptyspawn.Attempts+2, otherErr)

	_, err := s.AddDaemonWindow("missing", nil)
	if err == nil {
		t.Fatal("a non-transient error was swallowed")
	}
	if !errors.Is(err, syscall.ENOENT) {
		t.Fatalf("the surfaced error lost its cause: %v", err)
	}
	if *injected != 1 {
		t.Fatalf("a non-EPERM error was attempted %d times, want exactly 1", *injected)
	}
}
