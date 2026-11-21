//go:build !unix

package main

import (
	"fmt"
	"os"
)

func writeWinsize(string) {
	fmt.Fprintln(os.Stderr, "winsize is not supported on this platform")
	os.Exit(1)
}
