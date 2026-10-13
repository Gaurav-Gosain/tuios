//go:build darwin

package app

import (
	"time"

	"golang.org/x/sys/unix"
)

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
			return false, errPollInterrupted
		}
		return false, err
	}
	return n > 0, nil
}
