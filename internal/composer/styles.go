package composer

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Styles is the look of the editor. The host fills it from its theme and
// hands it to every Render. The composer has no colours of its own.
//
// A nil colour is the terminal's default. A nil function leaves its input as
// it is.
type Styles struct {
	// Field is the ground of the draft.
	Field color.Color
	// Text is the ink of the draft. Muted is the ink of the placeholder, the
	// scroll marks and a list's badges. Dim is the ink of quiet notes.
	Text, Muted, Dim color.Color
	// Key is the ink of the sigil and of a block's number.
	Key color.Color
	// Accent tints the ground of a block, through Blend.
	Accent color.Color
	// Warning is the ink of a notice under the editor.
	Warning color.Color
	// Selected is the ground of the selected row of a list and of the
	// selected block.
	Selected color.Color

	// Plain is for a terminal of 16 colours, where a ground cannot show a
	// selection. A selected row is then reversed and a selected block
	// underlined, through Reverse and Underline.
	Plain bool
	// ASCII draws ASCII marks in place of arrows.
	ASCII bool
	// Sigil is the mark in front of the draft's first row. A space follows
	// it.
	Sigil string

	// Readable returns fg, or the colour nearest fg that reads on bg.
	Readable func(fg, bg color.Color) color.Color
	// Blend returns a carried toward b by t, the ground of a block. Leave it
	// nil where a block keeps the field's ground, such as at 16 colours.
	Blend func(a, b color.Color, t float64) color.Color
	// Reverse and Underline restyle every cell of s, a row width cells wide,
	// when Plain is set.
	Reverse, Underline func(s string, width int) string
}

// Hint is one key the editor offers, for the host's footer.
type Hint struct {
	Key   string
	Label string
	// Optional marks a hint a narrow footer can drop first.
	Optional bool
}

// readable is s.Readable, or fg as it is.
func (s *Styles) readable(fg, bg color.Color) color.Color {
	if s.Readable == nil {
		return fg
	}
	return s.Readable(fg, bg)
}

// ground is the ground of a list row on bg.
func (s *Styles) ground(selected bool, bg color.Color) color.Color {
	if selected {
		return s.Selected
	}
	return bg
}

// row pads a list row to width on its ground, and marks it at 16 colours
// when it is selected.
func (s *Styles) row(content string, width int, selected bool, bg color.Color) string {
	row := fill(content, width, s.ground(selected, bg))
	if selected && s.Plain && s.Reverse != nil {
		return s.Reverse(row, width)
	}
	return row
}

// underline marks a selected span at 16 colours.
func (s *Styles) underline(span string) string {
	if !s.Plain || span == "" || s.Underline == nil {
		return span
	}
	return s.Underline(span, ansi.StringWidth(span))
}

// arrow is glyph, or ascii where the terminal draws only ASCII.
func (s *Styles) arrow(glyph, ascii string) string {
	if s.ASCII {
		return ascii
	}
	return glyph
}

// on returns a style with the ground bg. Every span on a row carries the
// row's ground, or its reset would punch a hole through the row's fill.
func on(bg color.Color) lipgloss.Style {
	return lipgloss.NewStyle().Background(bg)
}

// fill pads s with spaces on bg to width cells.
func fill(s string, width int, bg color.Color) string {
	w := lipgloss.Width(s)
	if w >= width {
		return s
	}
	return s + on(bg).Render(strings.Repeat(" ", width-w))
}

// cursor draws one cell as the text cursor: reverse video over the row's
// colours, so it shows on a terminal with no colours at all.
func cursor(ch string, bg, fg color.Color) string {
	if ch == "" {
		ch = " "
	}
	return on(bg).Foreground(fg).Reverse(true).Render(ch)
}

// enterKey names the return key.
func enterKey(ascii bool) string {
	if ascii {
		return "enter"
	}
	return "↵"
}

// noColor reports whether c is the terminal's default colour.
func noColor(c color.Color) bool {
	switch c.(type) {
	case nil, lipgloss.NoColor, *lipgloss.NoColor:
		return true
	}
	return false
}
