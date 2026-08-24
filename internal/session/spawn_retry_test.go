package session

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"testing"

	"github.com/charmbracelet/x/xpty"
)

// These tests pin the retry policy around the kernel's transient controlling-
// terminal refusal. The real event cannot be provoked on demand - it needs a
// pts index to be recycled out from under a dying session at the exact moment
// of the fork - so the seam injects the refusal the way the kernel delivers
// it: as an EPERM-wrapped start error. What is under test is the policy, which
// is what the flake taught: a transient EPERM is retried on a fresh PTY, a
// persistent one still fails loudly, and nothing else is ever retried.

// failStarts makes the next n starts fail with err, then delegates to the real
// start. It returns a restore function and a counter of injected failures.
func failStarts(t *testing.T, n int, err error) *int {
	t.Helper()
	real := startPTYProcess
	injected := 0
	startPTYProcess = func(p xpty.Pty, cmd *exec.Cmd) error {
		if injected < n {
			injected++
			return err
		}
		return real(p, cmd)
	}
	t.Cleanup(func() { startPTYProcess = real })
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
	injected := failStarts(t, spawnEPERMAttempts+2, epermLike)

	_, err := s.AddDaemonWindow("refused", nil)
	if err == nil {
		t.Fatal("a persistent EPERM was swallowed; a real refusal must surface")
	}
	if !errors.Is(err, syscall.EPERM) {
		t.Fatalf("the surfaced error lost its cause: %v", err)
	}
	if *injected != spawnEPERMAttempts {
		t.Fatalf("spawn was attempted %d times, want the bound %d", *injected, spawnEPERMAttempts)
	}
}

func TestNonEPERMSpawnErrorIsNotRetried(t *testing.T) {
	s := newTestSession(t)
	otherErr := fmt.Errorf("fork/exec /bin/sh: %w", syscall.ENOENT)
	injected := failStarts(t, spawnEPERMAttempts+2, otherErr)

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
