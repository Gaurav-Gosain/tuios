//go:build darwin || dragonfly || freebsd || linux || solaris || aix

package main

import (
	"os"

	"github.com/charmbracelet/x/term"
	"golang.org/x/sys/unix"
)

func withoutHardTabs() func() {
	fd := os.Stdin.Fd()
	if !term.IsTerminal(fd) {
		return func() {}
	}
	orig, err := term.GetState(fd)
	if err != nil || orig.Oflag&unix.TABDLY != unix.TAB0 {
		return func() {}
	}
	changed := *orig
	changed.Oflag = changed.Oflag&^unix.TABDLY | unix.TAB3
	if err := term.SetState(fd, &changed); err != nil {
		return func() {}
	}
	return func() { _ = term.SetState(fd, orig) }
}
