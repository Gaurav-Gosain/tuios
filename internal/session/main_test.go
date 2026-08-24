package session

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestMain isolates the whole test binary from the developer's own XDG
// directories and from their login shell.
func TestMain(m *testing.M) {
	code := testutil.RunIsolated(m, pinResurrectionDir, pinShell)
	// A test that creates a session and never stops it leaves the session's
	// periodic resurrection saver ticking for the rest of the binary, writing
	// state files every 30 seconds into whatever directory the override names
	// at that moment - another test's TempDir mid-cleanup included. That
	// contamination surfaced as a once-in-hours "TempDir RemoveAll: directory
	// not empty" flake, and it can just as silently feed one test's state file
	// to another test's restore. Catching the leak here names the problem the
	// moment it is reintroduced instead of hours later in an unrelated test.
	if leaked := leakedSaverGoroutines(); code == 0 && leaked > 0 {
		fmt.Fprintf(os.Stderr, "\n%d resurrection saver goroutine(s) outlived the run: "+
			"a test created a session and never stopped it, and its 30s saver is "+
			"still writing state files into other tests' directories\n", leaked)
		code = 1
	}
	os.Exit(code)
}

// leakedSaverGoroutines counts goroutines still parked in StartPeriodicSave.
func leakedSaverGoroutines() int {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	// The running frame appears once per goroutine; the plain function name
	// would also match each "created by" line and double-count.
	return strings.Count(string(buf[:n]), "StartPeriodicSave.func1(")
}

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
