package vt

// The cell size in pixels an emulator assumes until the host sets one with
// SetCellSize.
//
// A program reads the pane's size in pixels from XTWINOPS 14 and 16 and from
// the mode 2048 resize report, and an SGR-pixel mouse report (mode 1016) is in
// the same pixels. A program that divides by the pixel size quits on a zero,
// so a pane no host has measured still gets a cell size. A host that sets the
// kernel's pixel size (TIOCGWINSZ) for an unmeasured pane uses the same one,
// so the sizes agree.
const (
	FallbackCellWidth  = 10
	FallbackCellHeight = 20
)

// cellSizeOr returns w and h when both are positive, and the fallback cell
// otherwise.
func cellSizeOr(w, h int) (int, int) {
	if w <= 0 || h <= 0 {
		return FallbackCellWidth, FallbackCellHeight
	}
	return w, h
}
