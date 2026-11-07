package terminal

import (
	uv "github.com/charmbracelet/ultraviolet"
	"tuios.dev/vt"
)

// CopyText is the part of an emulator that copy mode reads: a screen of cells
// below a history of lines, oldest first. vt.Terminal has all of it, so a
// pane's emulator is one. The quick look preview is another, built from the
// rows of a file.
type CopyText interface {
	CellAt(x, y int) *uv.Cell
	Width() int
	Height() int
	ScrollbackLen() int
	ScrollbackLine(index int) uv.Line
	ScrollbackText(from, end int, fn func(index, width int, cells []vt.TextCell) bool)
	ScrollbackGeneration() uint64
	RowSoftWrapped(y int) (wrapped, known bool)
	ScrollbackSoftWrapped(index int) (wrapped, known bool)
	RowPadded(y int) bool
	ScrollbackPadded(index int) bool
}

// CopyGrid is what copy mode moves over: the text, and the view on it. The
// scroll offset is how many lines the view is scrolled back from the bottom.
// A pane's Window is one, and so is the quick look preview.
type CopyGrid interface {
	// Text is the emulator, or nil when there is none.
	Text() CopyText
	ScrollbackLen() int
	ScrollbackLine(index int) uv.Line
	ScrollOffset() int
	SetScrollOffset(offset int)
	// ViewHeight is the height half-page moves are measured in.
	ViewHeight() int
	ContentHeight() int
	LastContentRow() int
	LastContentCol() int
}

// Text is the window's emulator, or nil.
func (w *Window) Text() CopyText {
	if w.Terminal == nil {
		return nil
	}
	return w.Terminal
}

// ScrollOffset is how many lines the window is scrolled back.
func (w *Window) ScrollOffset() int { return w.ScrollbackOffset }

// SetScrollOffset scrolls the window back offset lines.
func (w *Window) SetScrollOffset(offset int) { w.ScrollbackOffset = offset }

// ViewHeight is the window's height, borders included.
func (w *Window) ViewHeight() int { return w.Height }
