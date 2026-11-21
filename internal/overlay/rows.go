package overlay

import (
	"image/color"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

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
	fn := p.MarkCell(st)
	if fn == nil || span == "" {
		return span
	}
	return restyleRow(span, ansi.StringWidth(span), fn)
}

// MarkCell is Mark for a caller that draws cells: the function that gives one
// cell the 16-colour attribute of st. It is nil at other depths, and for a
// state with no cursor and no hover.
func (p Palette) MarkCell(st RowState) func(*uv.Cell) {
	switch {
	case p.Depth != Depth16:
		return nil
	case st.Cursor && st.Focused:
		return reverseCell
	case st.Cursor, st.Hover:
		return underlineCell
	}
	return nil
}

// CursorText underlines the text of a cursor row, from cell skip on, in the
// cursor's colour: the accent for the focused cursor and FgMute for an
// unfocused one. The focused cursor's text is bold too. It is for the depths where the ground carries the state,
// since a ground can all but vanish: a see-through terminal shows its
// wallpaper through a row painted one step off the rail's own ground. At 16
// colours the span is returned as it is, because Mark already draws the
// cursor in attributes there. Cells that are underlined already keep their
// own underline, so a name the row style marks keeps its colour.
func (p Palette) CursorText(span string, st RowState, skip int) string {
	if p.Depth == Depth16 || !st.Cursor || span == "" {
		return span
	}
	ink := p.FgMute
	if st.Focused {
		ink = p.Accent
	}
	width := ansi.StringWidth(span)
	scr := uv.NewScreenBuffer(width, 1)
	scr.Method = ansi.GraphemeWidth
	uv.NewStyledString(span).Draw(scr, scr.Bounds())
	line := scr.Line(0)
	for x := skip; x < len(line); x++ {
		c := line.At(x)
		if c == nil || c.Width == 0 || strings.TrimSpace(c.Content) == "" || c.Style.Underline != uv.UnderlineNone {
			continue
		}
		c.Style.Underline = uv.UnderlineSingle
		c.Style.UnderlineColor = ink
		if st.Focused {
			c.Style.Attrs |= uv.AttrBold
		}
	}
	return line.Render()
}

func reverseCell(c *uv.Cell) {
	c.Style.Fg, c.Style.Bg = nil, nil
	c.Style.Attrs |= uv.AttrReverse
	c.Style.Attrs &^= uv.AttrFaint
}

func underlineCell(c *uv.Cell) {
	if strings.TrimSpace(c.Content) == "" {
		return
	}
	c.Style.Underline = uv.UnderlineSingle
}

func restyleRow(s string, width int, fn func(*uv.Cell)) string {
	if width <= 0 {
		return s
	}
	scr := uv.NewScreenBuffer(width, 1)
	scr.Method = ansi.GraphemeWidth
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
