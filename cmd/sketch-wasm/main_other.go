//go:build !(js && wasm)

// Command sketch-wasm is the browser build of tuios.dev/sketch. It only does
// something when built for js/wasm; see build.sh.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "sketch-wasm runs in a browser. Build it with cmd/sketch-wasm/build.sh.")
	os.Exit(2)
}
