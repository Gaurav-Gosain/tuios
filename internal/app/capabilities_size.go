//go:build darwin || linux || freebsd || netbsd || openbsd

package app

import "golang.org/x/sys/unix"

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
