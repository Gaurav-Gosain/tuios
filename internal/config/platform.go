package config

import (
	"os"
	"runtime"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/testknob"
)

var macOSHost = platformIsMacOS()

// E2EPlatformDarwin reports whether the end-to-end suite asked for the macOS
// defaults and key handling on another platform, with
// the knob platform=darwin. It is for the suite only.
func E2EPlatformDarwin() bool { return testknob.String("platform") == "darwin" }

func platformIsMacOS() bool {
	if runtime.GOOS == "darwin" || E2EPlatformDarwin() {
		return true
	}
	goos := strings.ToLower(os.Getenv("GOOS"))
	ostype := strings.ToLower(os.Getenv("OSTYPE"))
	return strings.Contains(goos, "darwin") || strings.Contains(ostype, "darwin")
}

// ForceMacOSHost makes the defaults, the key normalizer and the validator build
// for the named platform, and returns the function that puts it back. It is for
// tests in other packages that assert the behaviour of the platform they are
// not running on.
//
// It is not safe for parallel tests: one process has one platform.
func ForceMacOSHost(on bool) func() {
	prev := macOSHost
	macOSHost = on
	return func() { macOSHost = prev }
}
