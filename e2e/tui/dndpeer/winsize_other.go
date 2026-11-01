//go:build !unix

package main

import (
	"fmt"
	"os"
)

// writeWinsize has no ioctl to read here; the test that needs it runs under
// an X server and never on this platform.
func writeWinsize(string) {
	fmt.Fprintln(os.Stderr, "winsize is not supported on this platform")
	os.Exit(1)
}
