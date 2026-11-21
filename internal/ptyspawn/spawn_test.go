package ptyspawn

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"testing"

	"github.com/charmbracelet/x/xpty"
)

type spawnRecord struct {
	pty xpty.Pty
	cmd *exec.Cmd
}

func failStarts(t *testing.T, n int, err error) *[]spawnRecord {
	t.Helper()
	real := StartProcess
	var seen []spawnRecord
	StartProcess = func(p xpty.Pty, cmd *exec.Cmd) error {
		seen = append(seen, spawnRecord{pty: p, cmd: cmd})
		if len(seen) <= n {
			return err
		}
		return real(p, cmd)
	}
	t.Cleanup(func() { StartProcess = real })
	return &seen
}

func trueCommand() *exec.Cmd { return exec.Command("/bin/sh", "-c", "exit 0") }

// TestTheRetryUsesAFreshPTY is the part of the policy that is about the cause
// rather than about persistence. The refusal is about the pts index, so
// retrying on the same pty would retry the thing that was refused.
func TestTheRetryUsesAFreshPTY(t *testing.T) {
	seen := failStarts(t, 1, fmt.Errorf("fork/exec /bin/sh: %w", syscall.EPERM))

	pty, cmd, err := Spawn(80, 24, trueCommand, nil)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	t.Cleanup(func() { _ = pty.Close() })
	_ = cmd.Wait()

	if len(*seen) != 2 {
		t.Fatalf("spawn was attempted %d times, want 2", len(*seen))
	}
	if (*seen)[0].pty == (*seen)[1].pty {
		t.Fatal("the retry reused the refused PTY; the refusal is about that pts index")
	}
	if (*seen)[0].cmd == (*seen)[1].cmd {
		t.Fatal("the retry reused the failed exec.Cmd, which cannot be started twice")
	}
}

func TestPersistentEPERMStillFails(t *testing.T) {
	seen := failStarts(t, Attempts+2, fmt.Errorf("fork/exec /bin/sh: %w", syscall.EPERM))

	_, _, err := Spawn(80, 24, trueCommand, nil)
	if err == nil {
		t.Fatal("a persistent EPERM was swallowed; a real refusal must surface")
	}
	if !errors.Is(err, syscall.EPERM) {
		t.Fatalf("the surfaced error lost its cause: %v", err)
	}
	if len(*seen) != Attempts {
		t.Fatalf("spawn was attempted %d times, want the bound %d", len(*seen), Attempts)
	}
}

func TestNonEPERMIsNotRetried(t *testing.T) {
	seen := failStarts(t, Attempts+2, fmt.Errorf("fork/exec /bin/sh: %w", syscall.ENOENT))

	_, _, err := Spawn(80, 24, trueCommand, nil)
	if err == nil {
		t.Fatal("a non-transient error was swallowed")
	}
	if !errors.Is(err, syscall.ENOENT) {
		t.Fatalf("the surfaced error lost its cause: %v", err)
	}
	if len(*seen) != 1 {
		t.Fatalf("a non-EPERM error was attempted %d times, want exactly 1", len(*seen))
	}
}
