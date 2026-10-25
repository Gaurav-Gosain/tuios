//go:build !unix

// Command cellsize sets a terminal's pixel size, which only a unix tty has.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "cellsize: this system has no tty pixel size")
	os.Exit(1)
}
