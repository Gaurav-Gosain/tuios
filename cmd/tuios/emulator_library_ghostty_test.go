//go:build ghostty

package main

import (
	"os"
	"testing"
)

// TestGhosttyLinksThePinnedLibrary holds the libghostty-vt this test binary
// linked to the version scripts/ghostty-lib.sh pins. The go command can link a
// library from another folder out of its build cache (scripts/ghostty-env.sh
// says how), and then every other ghostty test measures the wrong library.
// TUIOS_GHOSTTY_VT_PIN is set by ghostty-env.sh; without it, as in a plain go
// test, there is nothing to compare against and the test skips.
func TestGhosttyLinksThePinnedLibrary(t *testing.T) {
	pin := os.Getenv("TUIOS_GHOSTTY_VT_PIN")
	if pin == "" {
		t.Skip("TUIOS_GHOSTTY_VT_PIN is not set; scripts/ghostty-env.sh sets it")
	}
	if got, want := emulatorLibrary(), "libghostty-vt "+pin; got != want {
		t.Fatalf("this test binary linked %s, want %s: the build cache linked a library from another folder", got, want)
	}
}
