package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestMain isolates the whole test binary from the developer's own XDG
// directories and from their login shell.
func TestMain(m *testing.M) { os.Exit(testutil.RunIsolated(m, pinResurrectionDir, pinShell)) }

// pinResurrectionDir gives the resurrection state a directory of its own.
// Without it, every test that creates a session persists a real state file and
// leaves a phantom session for the next real daemon start to resurrect. Tests
// that need to inspect state files still point the override at their own
// directory; this only provides a safe default for the ones that do not.
func pinResurrectionDir(dir string) {
	setResurrectionDirOverride(filepath.Join(dir, "resurrection"))
}

// pinShell makes the daemon spawn a POSIX shell rather than the developer's
// login shell. A window is a real process, so tests that drive one were
// running whatever $SHELL named, with that shell's startup files and startup
// cost: TestWaitForWindowExit passes under a shell that reaches its prompt
// quickly and times out under one that does not, which makes it a test of the
// machine rather than of the daemon.
func pinShell(string) {
	if err := os.Setenv("SHELL", "/bin/sh"); err != nil {
		panic(err)
	}
}

// useResurrectionDir points resurrection state at dir and returns a function
// that restores the previous value. Restoring the previous value rather than
// clearing it keeps the TestMain default in place, so a later test cannot fall
// back to the developer's real state directory.
func useResurrectionDir(dir string) func() {
	prev := setResurrectionDirOverride(dir)
	return func() { setResurrectionDirOverride(prev) }
}
