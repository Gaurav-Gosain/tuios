//go:build linux

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

// pollReadable uses poll to wait for the file descriptor to be readable with a timeout
func pollReadable(fd uintptr, timeout time.Duration) (bool, error) {
	fds := []unix.PollFd{
		{Fd: int32(fd), Events: unix.POLLIN},
	}
	timeoutMs := max(int(timeout.Milliseconds()), 1)

	n, err := unix.Poll(fds, timeoutMs)
	if err == unix.EINTR {
		return false, errPollInterrupted
	}
	if err != nil {
		return false, err
	}
	return n > 0 && (fds[0].Revents&unix.POLLIN) != 0, nil
}
