// Package cellsize is the size of one terminal cell in pixels that tuios
// assumes when the host terminal has not said.
//
// A pane program reads the pane's size in pixels from the kernel
// (TIOCGWINSZ), from an in-band resize report (mode 2048) and from XTWINOPS 14
// and 16, and an SGR-pixel mouse report (mode 1016) is in the same pixels. All
// of them come from one cell size. The emulator owns the fallback
// (vt.FallbackCellWidth and vt.FallbackCellHeight), and the pty layer takes it
// from here, so the sizes agree.
package cellsize

import "tuios.dev/vt"

const (
	// FallbackWidth is the assumed cell width in pixels.
	FallbackWidth = vt.FallbackCellWidth
	// FallbackHeight is the assumed cell height in pixels.
	FallbackHeight = vt.FallbackCellHeight
)
