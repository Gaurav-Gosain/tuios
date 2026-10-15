package main

import (
	"os"
	"runtime"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestMain isolates the whole test binary from the developer's own XDG
// directories. See testutil.RunIsolated for why this cannot be a per-test
// helper.
func TestMain(m *testing.M) {
	os.Exit(testutil.RunIsolated(m, func(dir string) {
		// Windows daemon sockets live under LOCALAPPDATA, not XDG_RUNTIME_DIR.
		if runtime.GOOS == "windows" {
			if err := os.Setenv("LOCALAPPDATA", dir); err != nil {
				panic(err)
			}
		}
	}))
}
