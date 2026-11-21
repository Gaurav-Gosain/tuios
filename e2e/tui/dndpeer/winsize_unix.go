//go:build unix

package main

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

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
