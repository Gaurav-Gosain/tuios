//go:build !windows

package session

import (
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// configurePTYCommand sets up the command for PTY usage on Unix systems.
// This creates a new session and sets up the controlling terminal.
func configurePTYCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true, // Create new session
		Setctty: true, // Set controlling terminal
		Ctty:    0,    // Use stdin (which will be the PTY slave)
	}
}

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
