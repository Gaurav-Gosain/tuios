//go:build !windows

package app

import (
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
