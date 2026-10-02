// Command keyecho stands in for a program that reads the kitty keyboard
// protocol, the way a Wayland compositor in a pane does: it pushes a set of
// flags and appends every byte the pane receives to a log file.
//
// Usage: keyecho FLAGS LOG
//
// The terminal must already be in raw mode (stty raw -echo), so the bytes
// reach the log as tuios sent them. It prints KEYECHO-READY once the flags are
// pushed.
package main

import (
	"fmt"
	"os"
	"time"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: keyecho FLAGS LOG")
		os.Exit(2)
	}
	log, err := os.OpenFile(os.Args[2], os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer log.Close()
	_, _ = fmt.Fprintf(os.Stdout, "\x1b[>%su", os.Args[1])
	_, _ = os.Stdout.WriteString("KEYECHO-" + "READY\r\n")
	done := time.After(120 * time.Second)
	in := make(chan []byte)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				in <- append([]byte(nil), buf[:n]...)
			}
			if err != nil {
				close(in)
				return
			}
		}
	}()
	for {
		select {
		case b, ok := <-in:
			if !ok {
				return
			}
			_, _ = log.Write(b)
		case <-done:
			_, _ = os.Stdout.WriteString("\x1b[<u")
			return
		}
	}
}
