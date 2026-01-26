//go:build linux

package app

import (
	"time"

	"golang.org/x/sys/unix"
)

func isTerminal(fd uintptr) bool {
	_, err := unix.IoctlGetTermios(int(fd), unix.TCGETS)
	return err == nil
}

func makeRaw(fd uintptr) (*unix.Termios, error) {
	termios, err := unix.IoctlGetTermios(int(fd), unix.TCGETS)
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

	if err := unix.IoctlSetTermios(int(fd), unix.TCSETS, termios); err != nil {
		return nil, err
	}

	return &oldState, nil
}

func restoreTerminal(fd uintptr, oldState *unix.Termios) {
	if oldState != nil {
		_ = unix.IoctlSetTermios(int(fd), unix.TCSETS, oldState)
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

// pollReadable uses poll to wait for the file descriptor to be readable with a timeout
func pollReadable(fd uintptr, timeout time.Duration) (bool, error) {
	fds := []unix.PollFd{
		{Fd: int32(fd), Events: unix.POLLIN},
	}
	timeoutMs := max(int(timeout.Milliseconds()), 1)

	n, err := unix.Poll(fds, timeoutMs)
	if err != nil {
		return false, err
	}
	return n > 0 && (fds[0].Revents&unix.POLLIN) != 0, nil
}
