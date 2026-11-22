//go:build !linux && !darwin

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "tuios-rd runs on Linux and macOS.")
	os.Exit(1)
}
