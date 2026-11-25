//go:build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/charmbracelet/x/term"
)

// watchSize sends the size of the terminal now and after each resize. One
// goroutine reads both, so the sizes arrive in the order they were read.
func watchSize(msgs chan<- Msg) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGWINCH)
	go func() {
		for {
			if w, h, err := term.GetSize(os.Stdout.Fd()); err == nil {
				msgs <- sizeMsg{Width: w, Height: h}
			}
			<-sig
		}
	}()
}
