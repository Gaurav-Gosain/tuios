// Command keydump asks its terminal for a keyboard mode, then prints every
// read from its input in hex, one line per read. It runs in a pane under
// `stty raw -echo`, so the bytes are the ones the pane was sent.
//
//	keydump legacy  asks for nothing
//	keydump kitty   pushes kitty keyboard flags 1 (disambiguate)
//	keydump mok     sets modifyOtherKeys to 2
package main

import (
	"fmt"
	"os"
)

func main() {
	mode := "legacy"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}
	switch mode {
	case "kitty":
		_, _ = os.Stdout.WriteString("\x1b[>1u")
	case "mok":
		_, _ = os.Stdout.WriteString("\x1b[>4;2m")
	case "legacy":
	default:
		fmt.Fprintf(os.Stderr, "keydump: unknown mode %q\n", mode)
		os.Exit(2)
	}
	// The mode is asked for before ready is printed, in the same stream, so
	// a terminal that shows ready has read the mode.
	fmt.Printf("keydump %s ready\r\n", mode)
	buf := make([]byte, 256)
	for n := 1; ; n++ {
		k, err := os.Stdin.Read(buf)
		if err != nil {
			return
		}
		fmt.Printf("read%d=%x\r\n", n, buf[:k])
	}
}
