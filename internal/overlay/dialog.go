package overlay

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Dialog is a hairline micro-dialog: a rounded muted frame with its title set
// into the top border, its key hints set into the bottom border, and an
// interior on bare canvas.
//
// It is the small end of a two-object grammar. A Panel is the surface-filled
// object for a list of many things; a Dialog is the object for one focused
// little thing, where a filled slab and a card-backed key chip inside a frame
// would be two greys in a box. The vocabulary is shared: the same hint wording,
// the same field sigil, the same casing.
//
// Width is the inner content width between the two border cells, so the
// rendered block is Width+2 cells across. Body lines carry their own leading
// pad, which keeps the column arithmetic in one place: the caller's.
type Dialog struct {
	Title string // lowercase, set into the top border
	Width int
	Body  string // pre-styled, multi-line; each line is canvas-filled
	Hints []Hint
}

// MinDialogWidth is the narrowest inner width a dialog lays itself out at:
// below this the borders have nothing left to carry.
const MinDialogWidth = 8

// DialogFitWidth returns the inner width for a dialog that would prefer
// preferred columns on a screen screenW columns wide, leaving room for its two
// border cells.
func DialogFitWidth(preferred, screenW int) int {
	if screenW <= 0 {
		return preferred
	}
	return max(min(preferred, screenW-2), 1)
}

// dialogFrame returns the corner, horizontal and vertical border glyphs.
func dialogFrame() (tl, tr, bl, br, h, v string) {
	if UseASCII() {
		return "+", "+", "+", "+", "-", "|"
	}
	return "╭", "╮", "╰", "╯", "─", "│"
}

// DashRule returns a dashed internal separator, the micro-dialog's answer to a
// Panel's solid rule: lighter, so it divides without drawing a second frame.
func DashRule(width int, bg color.Color, pal Palette) string {
	ch := "╌"
	if UseASCII() {
		ch = "-"
	}
	ch = chromeOr(func(c *Chrome) string { return c.DashRule }, ch)
	return Style(bg).Foreground(pal.FgMute).Render(strings.Repeat(ch, max(width, 0)))
}

// EnterKey names the return key for a hint strip: the glyph where it renders,
// the word where it does not. Three cells either way, like "esc" beside it.
func EnterKey() string {
	if UseASCII() {
		return "enter"
	}
	return "↵"
}

// SigilMark is the one-cell marker fronting an input field or the row a cursor
// is on. Sigil is the same mark plus its trailing space, which is the two-cell
// form list rows and search lines use.
func SigilMark() string {
	def := "›"
	if UseASCII() {
		def = ">"
	}
	return chromeOr(func(c *Chrome) string { return c.Sigil }, def)
}

// Sigil is SigilMark plus a space.
func Sigil() string { return SigilMark() + " " }

// Cursor renders one cell as the text cursor: reverse video over the colours
// the row already carries, rather than a painted background. A bg-painted
// cursor vanishes on a monochrome terminal, which is the one place a cursor
// still has to be findable, and a block glyph tofus in ASCII mode.
func Cursor(ch string, bg, fg color.Color) string {
	if ch == "" {
		ch = " "
	}
	return Style(bg).Foreground(fg).Reverse(true).Render(ch)
}

// hintStrip renders key hints for a border: keys bright and bold, labels muted,
// two cells between pairs.
func hintStrip(hints []Hint, bg color.Color, pal Palette) (string, int) {
	if len(hints) == 0 {
		return "", 0
	}
	// Both measured against the ground they land on: the accent follows the
	// terminal theme, and FgMute is furniture rather than text.
	keyStyle := Style(bg).Foreground(Readable(pal.AccentBright, bg)).Bold(true)
	labelStyle := Style(bg).Foreground(pal.FgDim)
	parts := make([]string, 0, len(hints))
	w := 0
	for i, h := range hints {
		if i > 0 {
			w += 2
		}
		parts = append(parts, keyStyle.Render(h.Key)+labelStyle.Render(" "+h.Label))
		w += hintWidth(h)
	}
	return strings.Join(parts, labelStyle.Render("  ")), w
}

// HintStrip renders key hints as one line on the given background, in the shape
// every footer uses. It is for the surfaces that carry hints outside a panel of
// their own, so they say what a key does the same way the panels do.
func HintStrip(hints []Hint, bg color.Color, pal Palette) string {
	s, _ := hintStrip(hints, bg, pal)
	return s
}

// Render assembles the dialog and returns the rendered string plus the geometry
// of its interactive regions in dialog-relative coordinates.
func (d Dialog) Render(pal Palette) (string, Geometry) {
	bg := pal.Canvas
	w := max(d.Width, MinDialogWidth)
	tl, tr, bl, br, h, v := dialogFrame()

	frame := Style(bg).Foreground(pal.FgMute)
	rule := func(n int) string { return frame.Render(strings.Repeat(h, max(n, 0))) }

	// Top border: the title sits in it rather than on a row of its own, which
	// is what keeps a three-row dialog three rows.
	top := frame.Render(tl)
	if title := Truncate(d.Title, max(w-3, 1)); title != "" {
		top += rule(1) +
			Style(bg).Render(" ") +
			Style(bg).Foreground(Readable(pal.Accent, bg)).Bold(true).Render(title) +
			Style(bg).Render(" ") +
			rule(w-lipgloss.Width(title)-3)
	} else {
		top += rule(w)
	}
	top += frame.Render(tr)

	// Bottom border: the hints ride in it, right-aligned, dropping from the end
	// when the frame runs out of room rather than wrapping onto a row.
	hints := d.Hints
	strip, stripW := hintStrip(hints, bg, pal)
	for len(hints) > 0 && stripW+3 > w {
		hints = hints[:len(hints)-1]
		strip, stripW = hintStrip(hints, bg, pal)
	}
	bottom := frame.Render(bl)
	if stripW > 0 {
		bottom += rule(w-stripW-3) + Style(bg).Render(" ") + strip + Style(bg).Render(" ") + rule(1)
	} else {
		bottom += rule(w)
	}
	bottom += frame.Render(br)

	side := frame.Render(v)
	lines := []string{top}
	for bodyLine := range strings.SplitSeq(d.Body, "\n") {
		if lipgloss.Width(bodyLine) > w {
			bodyLine = ansi.Truncate(bodyLine, w, "")
		}
		lines = append(lines, side+Fill(bodyLine, w, bg)+side)
	}
	lines = append(lines, bottom)

	// Where each hint landed on the bottom border, so a host can make them
	// pressable: the strip is right-aligned with one pad cell and one rule cell
	// after it, and the pairs are two cells apart.
	var hintRects []Rect
	if stripW > 0 {
		x, y := w-stripW-1, len(lines)-1
		for i, h := range hints {
			if i > 0 {
				x += 2
			}
			hintRects = append(hintRects, Rect{X0: x, Y0: y, X1: x + hintWidth(h), Y1: y + 1})
			x += hintWidth(h)
		}
	}

	return strings.Join(lines, "\n"), Geometry{
		Width:      w + 2,
		Height:     len(lines),
		InnerWidth: w,
		BodyX:      1,
		BodyY:      1,
		TitleBar:   Rect{X0: 0, Y0: 0, X1: w + 2, Y1: 1},
		Hints:      hintRects,
	}
}
