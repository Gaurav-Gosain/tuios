//go:build !windows && !js

package main

import (
	"os"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// windowPixels is the terminal's size in pixels and cells, where it reports one.
func windowPixels(f *os.File) (pw, ph, cols, rows int, ok bool) {
	ws, err := unix.IoctlGetWinsize(int(f.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		return 0, 0, 0, 0, false
	}
	return int(ws.Xpixel), int(ws.Ypixel), int(ws.Col), int(ws.Row), ws.Xpixel > 0
}

// readAnswer reads the terminal's answers until the primary device attributes
// reply ends them, or until the time is up.
func readAnswer(in *os.File, wait time.Duration) (string, error) {
	deadline := time.Now().Add(wait)
	var got strings.Builder
	buf := make([]byte, 256)
	for {
		left := time.Until(deadline)
		if left <= 0 {
			return got.String(), errNoAnswer
		}
		fds := []unix.PollFd{{Fd: int32(in.Fd()), Events: unix.POLLIN}}
		n, err := unix.Poll(fds, int(left/time.Millisecond)+1)
		if err == unix.EINTR {
			continue
		}
		if err != nil || n == 0 {
			return got.String(), errNoAnswer
		}
		k, err := unix.Read(int(in.Fd()), buf)
		if err != nil || k <= 0 {
			return got.String(), errNoAnswer
		}
		got.Write(buf[:k])
		s := got.String()
		// The device attributes reply is CSI ? ... c.
		if i := strings.LastIndex(s, "\x1b[?"); i >= 0 && strings.HasSuffix(s, "c") {
			return s, nil
		}
		if got.Len() > 4096 {
			return s, nil
		}
	}
}
