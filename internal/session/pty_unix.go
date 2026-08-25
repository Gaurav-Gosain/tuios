//go:build !windows

package session

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// SetPixelSize sets the pixel dimensions on the PTY using TIOCSWINSZ.
// This enables applications like kitty icat to query terminal size in pixels.
func (p *PTY) SetPixelSize(cols, rows, xpixel, ypixel int) error {
	if p.pty == nil {
		return nil
	}

	ws := unix.Winsize{
		Row:    uint16(rows),
		Col:    uint16(cols),
		Xpixel: uint16(xpixel),
		Ypixel: uint16(ypixel),
	}

	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		p.pty.Fd(),
		uintptr(unix.TIOCSWINSZ),
		uintptr(unsafe.Pointer(&ws)),
	)

	if errno != 0 {
		return errno
	}
	return nil
}
