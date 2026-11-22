//go:build linux || darwin

package main

import "golang.org/x/sys/unix"

type winsize struct{ rows, cols, xpx, ypx int }

func getWinsize() winsize {
	ws, err := unix.IoctlGetWinsize(1, unix.TIOCGWINSZ)
	if err != nil || ws.Col == 0 {
		return winsize{rows: 24, cols: 80}
	}
	return winsize{int(ws.Row), int(ws.Col), int(ws.Xpixel), int(ws.Ypixel)}
}

func makeRaw() (func(), error) {
	t, err := unix.IoctlGetTermios(0, ioctlGetTermios)
	if err != nil {
		return nil, err
	}
	old := *t
	t.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	t.Oflag &^= unix.OPOST
	t.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	t.Cflag &^= unix.CSIZE | unix.PARENB
	t.Cflag |= unix.CS8
	t.Cc[unix.VMIN], t.Cc[unix.VTIME] = 1, 0
	if err := unix.IoctlSetTermios(0, ioctlSetTermios, t); err != nil {
		return nil, err
	}
	return func() { _ = unix.IoctlSetTermios(0, ioctlSetTermios, &old) }, nil
}

func monoUS() int64 {
	var ts unix.Timespec
	_ = unix.ClockGettime(unix.CLOCK_MONOTONIC, &ts)
	return ts.Sec*1_000_000 + ts.Nsec/1000
}
