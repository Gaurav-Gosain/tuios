package session

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

const fakeAgentName = "crush"

// TestMain isolates the whole test binary from the developer's own XDG
// directories and from their login shell.
func TestMain(m *testing.M) {
	if filepath.Base(os.Args[0]) == fakeAgentName {
		time.Sleep(60 * time.Second)
		os.Exit(0)
	}
	if base := filepath.Base(os.Args[0]); base == fakeCallerName || slices.Contains(fakeParentNames, base) {
		if socket := os.Getenv("TUIOS_TEST_CALL_SOCKET"); socket != "" {
			os.Exit(runFakeCaller(socket))
		}
		time.Sleep(60 * time.Second)
		os.Exit(0)
	}
	code := testutil.RunIsolated(m, pinResurrectionDir, pinShell)
	if leaked := leakedSaverGoroutines(); code == 0 && leaked > 0 {
		fmt.Fprintf(os.Stderr, "\n%d resurrection saver goroutine(s) outlived the run: "+
			"a test created a session and never stopped it, and its 30s saver is "+
			"still writing state files into other tests' directories\n", leaked)
		code = 1
	}
	os.Exit(code)
}

func leakedSaverGoroutines() int {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	return strings.Count(string(buf[:n]), "startPeriodicSaveWith.func1(")
}

func pinResurrectionDir(dir string) {
	setResurrectionDirOverride(filepath.Join(dir, "resurrection"))
}

func pinShell(string) {
	if err := os.Setenv("SHELL", "/bin/sh"); err != nil {
		panic(err)
	}
}

func useResurrectionDir(dir string) func() {
	prev := setResurrectionDirOverride(dir)
	return func() { setResurrectionDirOverride(prev) }
}
