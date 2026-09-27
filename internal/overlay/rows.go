package overlay

import (
	"image/color"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// The focus and hover rule every list in tuios follows: the rail, the Inbox,
// the review overlay, settings, the command palette and every picker.
//
//  1. Reserve the cell. Whatever a row shows when it is the cursor (a mark, a
//     bold name) has a cell kept for it on every other row, so nothing moves
//     when the cursor does.
//  2. Change only colour. The cursor, the pointer and focus are grounds and
//     inks, never extra or fewer cells.
//  3. Always show the cursor. A list that does not have the keyboard still
//     shows where its cursor is, on the quieter RowSelQuiet, so the user knows
//     where they will land and which list is listening.
//
// At 16 colours the grounds are the terminal's own, so the states are
// attributes instead: the focused cursor is reverse video, and an unfocused
// cursor and the pointer underline the row's text.

// RowState is what a list row is at this moment.
type RowState struct {
	Cursor  bool // the row is the list's cursor
	Focused bool // the list has the keyboard
	Hover   bool // the pointer is over the row
}

// Ground is the fill a row in state st draws on, for a list whose resting
// rows sit on base. The cursor always wins over the pointer: a row carrying
// both would read as a third state that means nothing.
func (p Palette) Ground(st RowState, base color.Color) color.Color {
	switch {
	case st.Cursor && st.Focused:
		return p.RowSel
	case st.Cursor:
		return p.RowSelQuiet
	case st.Hover:
		return p.Hover
	}
	return base
}

// Row finishes a list row: pads content to width on the row's ground and, at
// 16 colours, applies the attribute that stands in for the ground there.
// content is the row as the caller drew it on p.Ground(st, base).
func (p Palette) Row(content string, width int, st RowState, base color.Color) string {
	row := Fill(content, width, p.Ground(st, base))
	if p.Depth != Depth16 {
		return row
	}
	switch {
	case st.Cursor && st.Focused:
		return restyleRow(row, width, reverseCell)
	case st.Cursor, st.Hover:
		return restyleRow(row, width, underlineCell)
	}
	return row
}

// Mark applies the 16-colour cursor attributes to a span that is not a whole
// row, such as the rail's cursor cell or a tab: reverse video for the focused
// cursor and an underline for a quiet one. At other depths the span is
// returned as it is, since its ground already carries the state.
func (p Palette) Mark(span string, st RowState) string {
	if p.Depth != Depth16 || span == "" {
		return span
	}
	w := ansi.StringWidth(span)
	switch {
	case st.Cursor && st.Focused:
		return restyleRow(span, w, reverseCell)
	case st.Cursor, st.Hover:
		return restyleRow(span, w, underlineCell)
	}
	return span
}

// reverseCell makes a cell part of a reverse-video bar. Its colours are
// dropped so the bar is one solid run in the terminal's own two colours,
// rather than a patchwork of every ink the row was drawn in; weight and
// underline survive, so a match highlight is still bold on the bar.
func reverseCell(c *uv.Cell) {
	c.Style.Fg, c.Style.Bg = nil, nil
	c.Style.Attrs |= uv.AttrReverse
	c.Style.Attrs &^= uv.AttrFaint
}

// underlineCell underlines a cell that carries text, leaving the padding
// alone so the line runs under the words rather than across the panel.
func underlineCell(c *uv.Cell) {
	if strings.TrimSpace(c.Content) == "" {
		return
	}
	c.Style.Underline = uv.UnderlineSingle
}

// restyleRow reads a styled single-line string into cells, applies fn to each
// and writes it back. It runs only at 16 colours and only for the one or two
// rows a list has lit, so its cost is a row's worth of cells a frame.
func restyleRow(s string, width int, fn func(*uv.Cell)) string {
	if width <= 0 {
		return s
	}
	scr := uv.NewScreenBuffer(width, 1)
	uv.NewStyledString(s).Draw(scr, scr.Bounds())
	line := scr.Line(0)
	for x := range line {
		c := line.At(x)
		if c == nil || c.Width == 0 {
			continue
		}
		fn(c)
	}
	return line.Render()
}
