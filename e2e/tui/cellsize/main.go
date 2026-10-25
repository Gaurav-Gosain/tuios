//go:build unix

// Command cellsize stands in for a pane that reports a cell size its host does
// not have: it sets the pixel size of its terminal's window to WxH pixels a
// cell, keeping the cell count, and then runs the command.
//
// Usage: cellsize WxH COMMAND [ARG...]
//
// A tuios pane on a daemon reports the cell size the daemon was told, which
// another client with another font can have set. A program that sizes a
// picture from it then draws for cells the host is not drawing.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

type winsize struct{ row, col, xpixel, ypixel uint16 }

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: cellsize WxH COMMAND [ARG...]")
		os.Exit(2)
	}
	w, h, _ := strings.Cut(os.Args[1], "x")
	cw, err1 := strconv.Atoi(w)
	ch, err2 := strconv.Atoi(h)
	if err1 != nil || err2 != nil || cw <= 0 || ch <= 0 {
		fmt.Fprintln(os.Stderr, "cellsize: give the size as WxH")
		os.Exit(2)
	}
	var ws winsize
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, 0, syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&ws))); e != 0 {
		fmt.Fprintln(os.Stderr, "cellsize: read the window size:", e)
		os.Exit(1)
	}
	ws.xpixel, ws.ypixel = uint16(int(ws.col)*cw), uint16(int(ws.row)*ch) //nolint:gosec // a test pane, far below 65535 pixels
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, 0, syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(&ws))); e != 0 {
		fmt.Fprintln(os.Stderr, "cellsize: set the window size:", e)
		os.Exit(1)
	}
	path, err := exec.LookPath(os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, "cellsize:", err)
		os.Exit(1)
	}
	if err := syscall.Exec(path, os.Args[2:], os.Environ()); err != nil {
		fmt.Fprintln(os.Stderr, "cellsize:", err)
		os.Exit(1)
	}
}
