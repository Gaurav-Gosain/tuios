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

// Text is the window's emulator, or nil. Its history is read through the
// window (see PaneText).
func (w *Window) Text() CopyText {
	if w.Terminal == nil {
		return nil
	}
	return w.paneTextOf()
}

// PaneText is the window's emulator for a reader of its screen, history and
// marks, such as the scrollback browser, or nil. The screen and the marks are
// the emulator's. The history lines are read through the window.
func (w *Window) PaneText() PaneText {
	if w.Terminal == nil {
		return nil
	}
	return w.paneTextOf()
}

// PaneText is CopyText with the emulator's OSC 133 marks.
type PaneText interface {
	CopyText
	SemanticMarkers() *vt.SemanticMarkerList
}

// paneTextOf is the window's paneText, kept while the emulator is the same,
// so a reader that asks for it per key or per row does not allocate.
func (w *Window) paneTextOf() *paneText {
	t := w.Terminal
	if p := w.paneText.Load(); p != nil && p.t == t {
		return p
	}
	p := &paneText{t: t, w: w}
	w.paneText.Store(p)
	return p
}

// paneText is a pane's emulator with its history read through the window.
// It forwards only the methods PaneText names: a type that embeds vt.Terminal
// gets a wrapper for each of its many methods, tens of KB of binary.
type paneText struct {
	t vt.Terminal
	w *Window
}

func (p *paneText) CellAt(x, y int) *uv.Cell                   { return p.t.CellAt(x, y) }
func (p *paneText) Width() int                                 { return p.t.Width() }
func (p *paneText) Height() int                                { return p.t.Height() }
func (p *paneText) RowSoftWrapped(y int) (wrapped, known bool) { return p.t.RowSoftWrapped(y) }
func (p *paneText) RowPadded(y int) bool                       { return p.t.RowPadded(y) }
func (p *paneText) SemanticMarkers() *vt.SemanticMarkerList    { return p.t.SemanticMarkers() }
func (p *paneText) ScrollbackLen() int                         { return p.w.ScrollbackLen() }
func (p *paneText) ScrollbackLine(index int) uv.Line           { return p.w.ScrollbackLine(index) }
func (p *paneText) ScrollbackPadded(index int) bool            { return p.w.ScrollbackPadded(index) }
func (p *paneText) ScrollbackGeneration() uint64               { return p.w.ScrollbackGeneration() }

func (p *paneText) ScrollbackSoftWrapped(index int) (wrapped, known bool) {
	return p.w.ScrollbackSoftWrapped(index)
}

func (p *paneText) ScrollbackText(from, end int, fn func(index, width int, cells []vt.TextCell) bool) {
	p.w.ScrollbackText(from, end, fn)
}

// ScrollOffset is how many lines the window is scrolled back.
func (w *Window) ScrollOffset() int { return w.ScrollbackOffset }

// SetScrollOffset scrolls the window back offset lines.
func (w *Window) SetScrollOffset(offset int) { w.ScrollbackOffset = offset }

// ViewHeight is the window's height, borders included.
func (w *Window) ViewHeight() int { return w.Height }
