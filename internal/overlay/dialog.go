package overlay

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Dialog is a hairline micro-dialog: a muted frame with its title set
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

// dialogFrame returns the corner, horizontal and vertical border glyphs: the
// pushed Frame (see SetFrame), or the rounded built-in when none is pushed.
func dialogFrame() (tl, tr, bl, br, h, v string) {
	if UseASCII() {
		return "+", "+", "+", "+", "-", "|"
	}
	if f := frame.Load(); f != nil {
		return f.TopLeft, f.TopRight, f.BottomLeft, f.BottomRight, f.Horizontal, f.Vertical
	}
	return "╭", "╮", "╰", "╯", "─", "│"
}

// edgeFrame is dialogFrame for a frame that is the only edge its surface has:
// a Box inside a panel, or a panel at 16 colours, whose ground is the
// terminal's own. A blank frame (the hidden border style) would leave such a
// surface with no edge at all, so it draws the rounded frame instead. A dialog
// and a filled panel keep the blank frame, because their fill is an edge.
func edgeFrame() (tl, tr, bl, br, h, v string) {
	tl, tr, bl, br, h, v = dialogFrame()
	if strings.TrimSpace(tl+tr+bl+br+h+v) == "" {
		return "╭", "╮", "╰", "╯", "─", "│"
	}
	return tl, tr, bl, br, h, v
}

// panelFrame is the frame a framed panel draws: edgeFrame at 16 colours,
// where the frame is the panel's only edge, else dialogFrame.
func panelFrame(pal Palette) (tl, tr, bl, br, h, v string) {
	if pal.Depth == Depth16 {
		return edgeFrame()
	}
	return dialogFrame()
}

// DashRule returns a dashed internal separator, the micro-dialog's answer to a
// Panel's solid rule: lighter, so it divides without drawing a second frame.
func DashRule(width int, bg color.Color, pal Palette) string {
	ch := DashRuleGlyph()
	return Style(bg).Foreground(pal.FgMute).Render(strings.Repeat(ch, max(width, 0)))
}

// DashRuleGlyph is the one cell DashRule repeats, exposed so a host reporting
// what a glyph set draws can read the same answer the dialog does.
func DashRuleGlyph() string {
	def := "╌"
	if UseASCII() {
		def = "-"
	}
	return chromeOr(func(c *Chrome) string { return c.DashRule }, def)
}

// EnterKey names the return key for a hint strip: the glyph where it renders,
// the word where it does not. Three cells either way, like "esc" beside it.
func EnterKey() string {
	if UseASCII() {
		return "enter"
	}
	return EnterGlyph
}

// EnterGlyph is the one glyph the return key is drawn with. A footer built
// once, before the terminal's glyph mode is known, names the key with it, and
// every hint strip draws it through hintKey, so it becomes "enter" in ASCII
// mode like a key named with EnterKey.
const EnterGlyph = "↵"

// hintKey is what a hint's key draws as: the return key as EnterKey draws it,
// anything else as written.
func hintKey(k string) string {
	if k == EnterGlyph {
		return EnterKey()
	}
	if UseASCII() {
		if word, ok := asciiArrowKeys[k]; ok {
			return word
		}
	}
	return k
}

// asciiArrowKeys are the arrow glyphs hint strips name keys with, spelled out
// for a terminal that draws only ASCII.
var asciiArrowKeys = map[string]string{
	"↑↓": "up/down", "←→": "left/right",
	"↑": "up", "↓": "down", "←": "left", "→": "right",
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

// FitHintStrip renders key hints as one line on the given background, in the
// shape every footer uses, no wider than width. It fits the hints by the
// tiers a panel footer uses (see fitHints): modifier names shortened, then
// whole hints dropped by priority. A key or a label is never cut in the
// middle, and the esc hint stays. It returns the strip and its width. It is
// for a surface with a fixed room for a mode's keys, such as the dock.
func FitHintStrip(hints []Hint, width int, bg color.Color, pal Palette) (string, int) {
	if len(hints) == 0 || width <= 0 {
		return "", 0
	}
	return renderHints(fitHints(hints, width, dialogSep), dialogSep, bg, pal)
}

// HintStripWidth is the width FitHintStrip draws hints at, with none dropped.
func HintStripWidth(hints []Hint) int {
	return hintsWidth(hints, dialogSep, false)
}

// Render assembles the dialog and returns the rendered string plus the geometry
// of its interactive regions in dialog-relative coordinates.
func (d Dialog) Render(pal Palette) (string, Geometry) {
	bg := pal.Canvas
	w := max(d.Width, MinDialogWidth)
	tl, tr, bl, br, h, v := dialogFrame()

	frame := frameStyle(bg, pal.FgMute, tl, tr, bl, br, h, v)
	// The title and hints ride in the frame, so they sit on its ground. That
	// ground is the cell beneath, known only once composited, so their colours
	// are measured against the dialog's own surface.
	label := frameGround(bg, tl, tr, bl, br, h, v)
	rule := func(n int) string { return frame.Render(strings.Repeat(h, max(n, 0))) }

	// Top border: the title sits in it rather than on a row of its own, which
	// is what keeps a three-row dialog three rows.
	top := frame.Render(tl)
	if title := Truncate(d.Title, max(w-3, 1)); title != "" {
		top += rule(1) +
			Style(label).Render(" ") +
			Style(label).Foreground(Readable(pal.Accent, bg)).Bold(true).Render(title) +
			Style(label).Render(" ") +
			rule(w-lipgloss.Width(title)-3)
	} else {
		top += rule(w)
	}
	top += frame.Render(tr)

	// Bottom border: the hints ride in it, right-aligned, shortened in tiers
	// when the frame runs out of room rather than wrapping onto a row. Three
	// cells are the border's own: a rule cell and a pad each side.
	fitted := fitHints(d.Hints, w-3, dialogSep)
	hints := fitted.Hints
	var strip string
	var stripW int
	if len(d.Hints) > 0 {
		strip, stripW = renderHintsOn(fitted, dialogSep, label, bg, pal)
	}
	bottom := frame.Render(bl)
	if stripW > 0 {
		bottom += rule(w-stripW-3) + Style(label).Render(" ") + strip + Style(label).Render(" ") + rule(1)
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
		hintRects = make([]Rect, len(d.Hints))
		x, y := w-stripW-1, len(lines)-1
		for i, h := range hints {
			if i > 0 {
				x += dialogSep
			}
			hintRects[fitted.Index[i]] = Rect{X0: x, Y0: y, X1: x + hintWidth(h), Y1: y + 1}
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
