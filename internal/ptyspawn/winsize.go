package ptyspawn

// WinsizeSetter is a pty that can take its size in cells and in pixels in one
// call. xpty.UnixPty is one; ConPTY and a pane whose bytes come from another
// machine are not.
type WinsizeSetter interface {
	SetWinsize(cols, rows, xpixel, ypixel int) error
}

// SetWinsize sets a pane's size in cells and in pixels with one call, so the
// guest gets one window-size change and at most one SIGWINCH for it.
//
// Resize alone is not enough: xpty's UnixPty.Resize writes zero pixels, and
// kitty graphics and sixel guests read the pixel size to scale their images.
// Following it with a second TIOCSWINSZ for the pixels changed the struct a
// second time, so the kernel signalled the guest twice for one resize, and in
// between the guest could read a size with no pixels in it.
//
// A pty that is a WinsizeSetter gets the pixels. Anything else is resized in
// cells only, which is all it can carry.
func SetWinsize(p interface{ Resize(cols, rows int) error }, cols, rows, xpixel, ypixel int) error {
	if ws, ok := p.(WinsizeSetter); ok {
		return ws.SetWinsize(cols, rows, xpixel, ypixel)
	}
	return p.Resize(cols, rows)
}
