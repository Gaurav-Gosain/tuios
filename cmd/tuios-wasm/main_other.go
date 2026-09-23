//go:build !(js && wasm)

// Command tuios-wasm is the browser build of tuios. It only does something when
// built for js/wasm; see build.sh.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "tuios-wasm runs in a browser. Build it with cmd/tuios-wasm/build.sh.")
	os.Exit(2)
}
