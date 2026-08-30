//go:build darwin

package app

import (
	"time"

	"golang.org/x/sys/unix"
)

func isTerminal(fd uintptr) bool {
	_, err := unix.IoctlGetTermios(int(fd), unix.TIOCGETA)
	return err == nil
}

func makeRaw(fd uintptr) (*unix.Termios, error) {
	termios, err := unix.IoctlGetTermios(int(fd), unix.TIOCGETA)
	if err != nil {
		return nil, err
	}

	oldState := *termios

	termios.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	termios.Oflag &^= unix.OPOST
	termios.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	termios.Cflag &^= unix.CSIZE | unix.PARENB
	termios.Cflag |= unix.CS8
	termios.Cc[unix.VMIN] = 1
	termios.Cc[unix.VTIME] = 0

	if err := unix.IoctlSetTermios(int(fd), unix.TIOCSETA, termios); err != nil {
		return nil, err
	}

	return &oldState, nil
}

func restoreTerminal(fd uintptr, oldState *unix.Termios) {
	if oldState != nil {
		_ = unix.IoctlSetTermios(int(fd), unix.TIOCSETA, oldState)
	}
}

// queryTerminalSize gets the terminal columns and rows using TIOCGWINSZ
func queryTerminalSize(caps *HostCapabilities) {
	ws, err := unix.IoctlGetWinsize(int(unix.Stdout), unix.TIOCGWINSZ)
	if err != nil {
		return
	}
	caps.Cols = int(ws.Col)
	caps.Rows = int(ws.Row)
	// Also get pixel dimensions if available
	if ws.Xpixel > 0 && caps.PixelWidth == 0 {
		caps.PixelWidth = int(ws.Xpixel)
	}
	if ws.Ypixel > 0 && caps.PixelHeight == 0 {
		caps.PixelHeight = int(ws.Ypixel)
	}
}

// pollReadable waits for the file descriptor to be readable, with a timeout.
//
// It uses select(2) rather than poll(2) because this fd is always a tty, and
// Darwin's poll() does not report readability for character devices: it
// returns 1 with revents=POLLNVAL while the data sits in the buffer waiting,
// so a POLLIN test reads as "nothing there" and the caller gives up on a
// terminal that answered. That made every capability probe on macOS come back
// empty, and every graphics capability come back false. select() reports the
// same tty correctly.
func pollReadable(fd uintptr, timeout time.Duration) (bool, error) {
	if fd >= 1024 {
		// Outside select's descriptor table; assume readable and let the
		// read block or return, rather than reporting a false negative.
		return true, nil
	}
	var rfds unix.FdSet
	rfds.Bits[fd/32] |= 1 << (fd % 32)

	tv := unix.NsecToTimeval(timeout.Nanoseconds())
	n, err := unix.Select(int(fd)+1, &rfds, nil, nil, &tv)
	if err != nil {
		if err == unix.EINTR {
			return false, nil
		}
		return false, err
	}
	return n > 0, nil
}
