package config

import (
	"os"
	"runtime"
	"strings"
)

// macOSHost is the answer detectMacOS and isMacOS give.
//
// A variable rather than a call so a test can put the other platform's
// defaults in front of code that has to behave the same on both, the way
// internal/input already does with darwinHost. Without it, a test asserting
// non-macOS behaviour can only run on a non-macOS machine: the defaults, the
// key normalizer and the validator all read this, and every one of them
// answers for the machine the test is running on.
//
// Production never writes it.
var macOSHost = platformIsMacOS()

// PlatformIsMacOS is the real answer, read once at init by this package and by
// internal/input. GOOS decides, and OSTYPE=darwin in the environment also
// counts, which is how the end-to-end suite runs the macOS key paths on Linux.
func PlatformIsMacOS() bool { return platformIsMacOS() }

// platformIsMacOS is the real answer, read once at init.
func platformIsMacOS() bool {
	// Check GOOS first (most reliable)
	if runtime.GOOS == "darwin" {
		return true
	}
	// Fallback to environment variables
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
