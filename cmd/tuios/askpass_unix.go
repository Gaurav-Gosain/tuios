//go:build unix

package main

import (
	"os"
	"syscall"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// foregroundTerminal opens the controlling terminal when the program that
// ran this one, ssh, is its foreground job: a person typed it there and
// types its answers there. A background child of an agent may still hold
// the terminal, and reading it would take the agent's keys, so it does not
// count.
func foregroundTerminal() (*os.File, bool) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, false
	}
	fg, err := unix.IoctlGetInt(int(tty.Fd()), unix.TIOCGPGRP)
	asker, err2 := syscall.Getpgid(os.Getppid())
	if err != nil || err2 != nil || fg != asker {
		_ = tty.Close()
		return nil, false
	}
	return tty, true
}

// readHidden reads one line from tty with echo off.
func readHidden(tty *os.File) ([]byte, error) {
	return term.ReadPassword(int(tty.Fd()))
}
