//go:build windows

package session

// SetPixelSize is a no-op on Windows as ConPTY doesn't support pixel dimensions.
func (p *PTY) SetPixelSize(cols, rows, xpixel, ypixel int) error {
	return nil
}
