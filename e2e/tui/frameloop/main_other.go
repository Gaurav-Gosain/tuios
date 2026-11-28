//go:build !unix

// Command frameloop streams frames sized from the tty's pixel size, which
// only a unix tty has.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "frameloop: this system has no tty pixel size")
	os.Exit(1)
}
