//go:build unix

package review

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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
	fake := "#!/bin/sh\n" + sleep + " 20 &\n" + sleep + " 20\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = Git(ctx, t.TempDir(), nil, "fetch")
	if took := time.Since(start); err == nil || took < 500*time.Millisecond || took > 500*time.Millisecond+gitWaitDelay+2*time.Second {
		t.Fatalf("Git returned after %v with %v, want an error within the timeout and the wait delay", took, err)
	}
}
