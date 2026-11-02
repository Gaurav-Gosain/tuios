//go:build unix

package main

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// writeWinsize writes "COLS ROWS XPIXEL YPIXEL" for the terminal on stdout
// to path: what a test needs to turn a cell into a screen pixel under a real
// terminal, read from the same ioctl the terminal fills in.
func writeWinsize(path string) {
	var ws struct{ rows, cols, xpixel, ypixel uint16 }
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, os.Stdout.Fd(), uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws))); errno != 0 {
		fmt.Fprintln(os.Stderr, errno)
		os.Exit(1)
	}
	if err := os.WriteFile(path, fmt.Appendf(nil, "%d %d %d %d\n", ws.cols, ws.rows, ws.xpixel, ws.ypixel), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
