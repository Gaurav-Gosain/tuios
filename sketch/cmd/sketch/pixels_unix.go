//go:build !windows && !js && !plan9 && !wasip1

package main

import (
	"os"

	"golang.org/x/sys/unix"
)

// windowPixels is the terminal's size in pixels and cells, where it reports one.
func windowPixels(f *os.File) (pw, ph, cols, rows int, ok bool) {
	ws, err := unix.IoctlGetWinsize(int(f.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		return 0, 0, 0, 0, false
	}
	return int(ws.Xpixel), int(ws.Ypixel), int(ws.Col), int(ws.Row), ws.Xpixel > 0
}
