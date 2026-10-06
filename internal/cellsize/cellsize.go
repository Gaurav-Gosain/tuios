// Package cellsize is the size of one terminal cell in pixels that tuios
// assumes when the host terminal has not said.
//
// A pane program reads the pane's size in pixels from the kernel
// (TIOCGWINSZ), from an in-band resize report (mode 2048) and from XTWINOPS 14
// and 16, and an SGR-pixel mouse report (mode 1016) is in the same pixels. All
// of them come from one cell size. A pane that no client has measured yet, such
// as one in a detached session, still has to be given one: a program that
// divides by the pixel size quits on a zero (issue #506). The pty layer and
// both emulators take the fallback from here, so the sizes agree.
package cellsize

const (
	// FallbackWidth is the assumed cell width in pixels.
	FallbackWidth = 10
	// FallbackHeight is the assumed cell height in pixels.
	FallbackHeight = 20
)

// Or returns w and h when both are positive, and the fallback cell otherwise.
func Or(w, h int) (int, int) {
	if w <= 0 || h <= 0 {
		return FallbackWidth, FallbackHeight
	}
	return w, h
}
