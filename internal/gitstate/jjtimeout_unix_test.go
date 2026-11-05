//go:build unix

package gitstate

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestJJTimeoutHoldsWithAHungChild is a regression test for the review of
// #1470: a jj wrapper whose child keeps stdout open must not hold readJJ, and
// with it tuios new, past countTimeout.
func TestJJTimeoutHoldsWithAHungChild(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("needs sleep")
	}
	repo := testutil.JJRepo(t, false)
	bin := t.TempDir()
	fake := "#!/bin/sh\n" + sleep + " 20 &\n" + sleep + " 20\n"
	if err := os.WriteFile(filepath.Join(bin, "jj"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	Forget()
	start := time.Now()
	_, ok := readJJ(repo, filepath.Join(repo, ".jj", "repo"))
	if took := time.Since(start); ok || took > countTimeout+3*time.Second {
		t.Fatalf("readJJ returned ok=%v after %v, want a failure within countTimeout and the wait delay", ok, took)
	}
}
