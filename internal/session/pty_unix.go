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

	// A pane whose bytes come from somewhere other than a local pty has no file
	// descriptor to set a window size on. Its size is carried by whatever is
	// feeding it, so there is nothing to do here and nothing has gone wrong.
	fd, ok := p.pty.(interface{ Fd() uintptr })
	if !ok {
		return nil
	}

	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		fd.Fd(),
		uintptr(unix.TIOCSWINSZ),
		uintptr(unsafe.Pointer(&ws)),
	)

	if errno != 0 {
		return errno
	}
	return nil
}
