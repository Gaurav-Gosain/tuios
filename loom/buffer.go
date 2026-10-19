package loom

import (
	"image/color"

	uv "github.com/charmbracelet/ultraviolet"
)

// Buffer is a surface's picture: Height rows of Width cells. It renders to
// ANSI text or to plain text, and a host reads its cells to composite.
type Buffer struct {
	b *uv.Buffer
}

// NewBuffer returns a blank buffer of w by h cells, such as the one Frame
// draws into.
func NewBuffer(w, h int) *Buffer {
	return &Buffer{b: uv.NewBuffer(max(0, min(w, maxSide)), max(0, min(h, maxSide)))}
}

// Cell is one cell of a Buffer. Content is one grapheme, empty for the
// second cell of a wide grapheme, which has Width 0. A nil colour is the
// terminal's own.
type Cell struct {
	Content                                         string
	Width                                           int
	Fg, Bg                                          color.Color
	Bold, Faint, Italic, Underline, Strike, Reverse bool
	// Link is the cell's OSC 8 target. It is set only when the surface's
	// Options.Hyperlinks is on.
	Link string
}

// Width returns the width in cells.
func (b *Buffer) Width() int { return b.b.Width() }

// Height returns the height in cells.
func (b *Buffer) Height() int { return b.b.Height() }

// Cell returns the cell at x, y, or a blank cell outside the buffer.
func (b *Buffer) Cell(x, y int) Cell {
	c := b.b.CellAt(x, y)
	if c == nil {
		return Cell{Content: " ", Width: 1}
	}
	return Cell{
		Content: c.Content, Width: c.Width, Fg: c.Style.Fg, Bg: c.Style.Bg,
		Bold: c.Style.Attrs&uv.AttrBold != 0, Faint: c.Style.Attrs&uv.AttrFaint != 0,
		Italic: c.Style.Attrs&uv.AttrItalic != 0, Underline: c.Style.Underline != 0,
		Strike: c.Style.Attrs&uv.AttrStrikethrough != 0, Reverse: c.Style.Attrs&uv.AttrReverse != 0,
		Link: c.Link.URL,
	}
}

// String returns the text of the buffer, with no styles.
func (b *Buffer) String() string { return b.b.String() }

// Render returns the buffer as ANSI text: SGR styles, and OSC 8 links when
// they are on.
func (b *Buffer) Render() string { return b.b.Render() }
