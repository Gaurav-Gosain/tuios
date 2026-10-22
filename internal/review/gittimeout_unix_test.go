//go:build unix

package review

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestGitTimeoutHoldsWithAHungChild is a regression test for the review of
// #956: a git whose child keeps the output open, as git-remote-https does on
// a remote that stopped answering, must not hold Git past its context.
func TestGitTimeoutHoldsWithAHungChild(t *testing.T) {
	bin := t.TempDir()
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("needs sleep")
	}
	pidFile := filepath.Join(t.TempDir(), "child")
	fake := "#!/bin/sh\n" + sleep + " 20 &\necho $! >" + pidFile + "\n" + sleep + " 20\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	// The clock starts before the context does. The deadline is measured from
	// inside WithTimeout, so a start taken after it is later than the
	// deadline's own start, and a Git that returned on time read as early.
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, err = Git(ctx, t.TempDir(), nil, "fetch")
	if took := time.Since(start); err == nil || took < 500*time.Millisecond || took > 500*time.Millisecond+gitWaitDelay+2*time.Second {
		t.Fatalf("Git returned after %v with %v, want an error within the timeout and the wait delay", took, err)
	}
	// On Linux the child git started is killed too, not left running.
	if runtime.GOOS == "linux" {
		b, _ := os.ReadFile(pidFile)
		pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
		time.Sleep(100 * time.Millisecond)
		if pid > 0 && syscall.Kill(pid, 0) == nil {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Errorf("git's child %d still runs after Git returned", pid)
		}
	}
}
