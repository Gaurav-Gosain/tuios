package app

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// accentPickerInnerWidth is the compact dialog's preferred inner width. It is
// the width of the shades grid plus a pad column either side, and the widest
// line that layout carries is the old-to-new readout with two hexes on it.
const accentPickerInnerWidth = 34

// accentHitKind names what a recorded rect in the picker does when it is
// clicked or dragged over.
type accentHitKind uint8

const (
	accentHitNone accentHitKind = iota
	accentHitGrid
	accentHitHue
	accentHitHex
	accentHitHarmony
	accentHitClear
	accentHitANSI
	accentHitSlider
	// The key hints in the bottom border, which are the mouse's only way to
	// apply or cancel: the keyboard has enter and esc, and a boxed button inside
	// the dialog would be the only object of its kind in the overlay grammar.
	accentHitHint
)

// The hints, in the order they are drawn and so in the order their rects come
// back from the dialog. Col carries the index.
const (
	accentHintFocus = iota
	accentHintApply
	accentHintClear
	accentHintCancel
)

// accentHit is where one interactive cell of the picker was drawn, in
// dialog-relative coordinates. Recorded by the renderer as it draws rather than
// recomputed by the mouse handler, so a click lands on the cell the user is
// pointing at even when a narrow screen has reflowed the grid under them. Col
// and Row carry the grid cell, the hue index, or the chip index, depending on
// Kind.
type accentHit struct {
	Rect     overlay.Rect
	Kind     accentHitKind
	Col, Row int
}

// accentPickerHints are built per render because the key glyphs follow ASCII
// mode.
func accentPickerHints() []overlay.Hint {
	return []overlay.Hint{
		{Key: "tab", Label: "field"},
		{Key: overlay.EnterKey(), Label: "apply"},
		{Key: "x", Label: "clear"},
		{Key: "esc", Label: "cancel"},
	}
}

// accentClearGlyph is the mark on the control that takes an accent away.
func accentClearGlyph() string {
	if overlay.UseASCII() {
		return "x"
	}
	return "✕"
}

// accentCursorGlyph is the mark drawn on the swatch under the cursor. It is
// drawn in a colour picked to read against that swatch, so it is findable on a
// pale cell and on a dark one.
func accentCursorGlyph() string {
	if overlay.UseASCII() {
		return "+"
	}
	return "◆"
}

// accentFocusMark is the one cell in the body's left pad that says which
// control the keyboard is driving.
func accentFocusMark(on bool, bg color.Color, pal overlay.Palette) string {
	if !on {
		return overlay.Style(bg).Render(" ")
	}
	return overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render(overlay.SigilMark())
}

// swatch paints n cells of a colour, stepped down to what the terminal can
// actually show so the block and the hex printed beside it agree.
func accentSwatch(c color.RGBA, n int) string {
	return overlay.Style(accentShown(c)).Render(strings.Repeat(" ", n))
}

// accentCursorSwatch paints the same n cells with the cursor mark centred in
// them, in a colour picked to read against that swatch.
func accentCursorSwatch(c color.RGBA, n int) string {
	return overlay.Style(accentShown(c)).Foreground(accentContrast(c)).Bold(true).
		Render(accentCentred(accentCursorGlyph(), n))
}

// accentCentred puts a one-cell mark in the middle of n cells of space.
func accentCentred(mark string, n int) string {
	n = max(n, 1)
	lead := (n - 1) / 2
	return strings.Repeat(" ", lead) + mark + strings.Repeat(" ", n-1-lead)
}

// accentChipGlyph is the mark a harmony chip wears where the terminal cannot
// paint a background.
func accentChipGlyph() string {
	if overlay.UseASCII() {
		return "o"
	}
	return "●"
}

// accentChip paints one harmony chip.
//
// On a colourless terminal every background-painted swatch in the dialog
// renders as blank space, and the chips would be a row of nothing: no cursor to
// see, no target to aim at, and no way to tell there was a control there. The
// grid and the strip at least keep their shape from the cursor mark and the
// numbers beside them. So the chips fall back to a foreground glyph, which
// survives, and the hex line says what the one under the cursor holds. A plainer
// picker beats an unusable one.
func accentChip(c color.RGBA, n int, cursor bool, pal overlay.Palette) string {
	if !accentMonochrome() {
		if cursor {
			return accentCursorSwatch(c, n)
		}
		return accentSwatch(c, n)
	}
	mark := accentChipGlyph()
	if cursor {
		mark = accentCursorGlyph()
	}
	return overlay.Style(pal.Canvas).Foreground(theme.Readable(c, pal.Canvas)).Bold(cursor).
		Render(accentCentred(mark, n))
}

// renderAccentPicker draws the colour picker for the window being accented and
// records the hit geometry of everything in it.
//
// Wide, it is two columns either side of a dashed rule: the colour space on the
// left (the theme's colours, the hue strip, the shades grid, the readout) and
// the numbers on the right (the hex field, the five sliders, the harmony
// chips). Narrower, the same controls stack into one column, and narrower still
// the sliders go and the swatches shrink to a cell each.
//
// The keyboard reaches every control with tab and the arrows; the mouse reaches
// every cell of them through the rects recorded here.
func (m *OS) renderAccentPicker() (string, overlay.Geometry, []overlayRowHit) {
	pal := theme.UI()
	p := m.accentPlan()
	m.accentHits = m.accentHits[:0]

	var body []string
	if p.Mode == accentLayoutWide {
		left := m.accentRecordAt(0, 0, func() []string { return m.accentSpaceColumn(p, pal) })
		right := m.accentRecordAt(accentWideLeft+1, 0, func() []string { return m.accentNumberColumn(p, pal) })
		body = accentJoinColumns(left, right, pal)
	} else {
		body = m.accentRecordAt(0, 0, func() []string { return m.accentStackedBody(p, pal) })
	}

	title := "accent"
	if m.AccentPickerTarget == AccentTargetSession {
		// A session's colour is shared by every client attached to it, so the
		// dialog says which of the two the user is about to change.
		title = "session accent"
	}
	content, geo := overlay.Dialog{
		Title: title,
		Width: p.Inner,
		Body:  strings.Join(body, "\n"),
		Hints: accentPickerHints(),
	}.Render(pal)

	// Everything above recorded itself in body coordinates, which is the frame
	// the lines were built in. Shift the whole set onto the dialog's own grid in
	// one pass rather than making every caller carry the border offset.
	for i := range m.accentHits {
		r := &m.accentHits[i].Rect
		r.X0, r.X1 = r.X0+geo.BodyX, r.X1+geo.BodyX
		r.Y0, r.Y1 = r.Y0+geo.BodyY, r.Y1+geo.BodyY
	}

	// The hints ride the border rather than the body, so they come from the
	// dialog's own geometry and are already in its coordinates. A narrow frame
	// drops them from the end, and what comes back is what was drawn.
	for i, r := range geo.Hints {
		m.accentHits = append(m.accentHits, accentHit{Rect: r, Kind: accentHitHint, Col: i})
	}

	// The picker routes its own clicks off the rects above, so it registers no
	// generic body rows: a row hit would swallow the click before it could reach
	// the cell under it.
	return content, geo, nil
}

// accentRecordAt runs a column builder and moves every rect it recorded onto
// that column's origin. Builders count from their own left edge, so a control
// does not have to know which side of the rule it landed on, and the rects still
// come from the renderer as it draws rather than from arithmetic repeated in a
// handler.
func (m *OS) accentRecordAt(x, y int, build func() []string) []string {
	first := len(m.accentHits)
	lines := build()
	for i := first; i < len(m.accentHits); i++ {
		r := &m.accentHits[i].Rect
		r.X0, r.X1 = r.X0+x, r.X1+x
		r.Y0, r.Y1 = r.Y0+y, r.Y1+y
	}
	return lines
}

// accentColumnRule is the vertical dash between the wide layout's two columns.
// A rule rather than a second bordered float: two frames cost four columns and a
// z-order story, and one dialog drags as one object.
func accentColumnRule() string {
	if overlay.UseASCII() {
		return "|"
	}
	return "┆"
}

// accentJoinColumns lays two columns either side of the rule, padding the
// shorter one so the body stays a rectangle.
func accentJoinColumns(left, right []string, pal overlay.Palette) []string {
	bg := pal.Canvas
	rule := overlay.Style(bg).Foreground(pal.FgMute).Render(accentColumnRule())
	out := make([]string, 0, max(len(left), len(right)))
	for i := range max(len(left), len(right)) {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		out = append(out, overlay.Fill(l, accentWideLeft, bg)+rule+overlay.Fill(r, accentWideRight, bg))
	}
	return out
}

// accentSpaceColumn is the wide layout's left column: the colour space, from
// the theme's own colours down to the readout of what is about to be applied.
func (m *OS) accentSpaceColumn(p accentLayoutPlan, pal overlay.Palette) []string {
	bg := pal.Canvas
	var body []string
	at := func() int { return len(body) }

	if p.Slots {
		body = append(body, m.accentSlotLines(p.ColInner, at(), pal)...)
		if p.Blanks {
			body = append(body, overlay.Fill("", p.ColInner, bg))
		}
	}
	body = append(body, m.accentHueLine(p, at(), pal))
	body = append(body, m.accentGridLines(p, at(), pal)...)
	body = append(body, m.accentRuleLine(p.ColInner, pal))
	body = append(body, m.accentNowLine(p.ColInner, at(), pal))
	return body
}

// accentNumberColumn is the wide layout's right column: the hex field, the five
// sliders, and the harmony chips.
func (m *OS) accentNumberColumn(p accentLayoutPlan, pal overlay.Palette) []string {
	bg := pal.Canvas
	var body []string
	at := func() int { return len(body) }
	blank := func() {
		if p.Blanks {
			body = append(body, overlay.Fill("", accentWideRight, bg))
		}
	}

	body = append(body, m.accentHexLine(accentWideRight, at(), pal))
	blank()
	body = append(body, m.accentSliderLines(accentWideRight, at(), pal)...)
	blank()
	body = append(body, m.accentHarmonyLines(p, accentWideRight, at(), pal)...)
	return body
}

// accentStackedBody is the one-column layout, which is also the compact one
// with its sliders dropped and its swatches down to a cell each.
func (m *OS) accentStackedBody(p accentLayoutPlan, pal overlay.Palette) []string {
	var body []string
	at := func() int { return len(body) }

	if p.Slots {
		body = append(body, m.accentSlotLines(p.Inner, at(), pal)...)
	}
	body = append(body, m.accentHueLine(p, at(), pal))
	body = append(body, m.accentGridLines(p, at(), pal)...)
	body = append(body, m.accentRuleLine(p.Inner, pal))
	body = append(body, m.accentNowLine(p.Inner, at(), pal))
	body = append(body, m.accentHexLine(p.Inner, at(), pal))
	if p.Sliders {
		body = append(body, m.accentSliderLines(p.Inner, at(), pal)...)
	}
	body = append(body, m.accentHarmonyLines(p, p.Inner, at(), pal)...)
	return body
}

// accentRuleLine is the dashed divider under the shades grid.
func (m *OS) accentRuleLine(width int, pal overlay.Palette) string {
	bg := pal.Canvas
	return overlay.Fill(overlay.Style(bg).Render(" ")+overlay.DashRule(max(width-2, 0), bg, pal), width, bg)
}

// accentHueLine renders the hue strip: one cell per step around the circle,
// with the held hue marked.
func (m *OS) accentHueLine(p accentLayoutPlan, y int, pal overlay.Palette) string {
	bg := pal.Canvas
	s := &m.AccentPicker
	cells := p.HueCells
	held := accentHueCell(s.Hue, cells)

	line := accentFocusMark(s.Focus == accentFocusHue, bg, pal)
	for i := range cells {
		c := hslToRGB(accentHueAt(i, cells), 1, 0.5)
		if i == held {
			line += accentCursorSwatch(c, 1)
		} else {
			line += accentSwatch(c, 1)
		}
		m.accentHits = append(m.accentHits, accentHit{
			Rect: overlay.Rect{X0: 1 + i, Y0: y, X1: 2 + i, Y1: y + 1},
			Kind: accentHitHue, Col: i,
		})
	}
	return overlay.Fill(line, p.ColInner, bg)
}

// accentGridLines renders the shades grid: saturation across, lightness down,
// at the held hue.
func (m *OS) accentGridLines(p accentLayoutPlan, y int, pal overlay.Palette) []string {
	bg := pal.Canvas
	s := &m.AccentPicker
	out := make([]string, 0, p.GridRows)
	for row := range p.GridRows {
		line := accentFocusMark(row == 0 && s.Focus == accentFocusGrid, bg, pal)
		for col := range p.GridCols {
			c := accentCellColor(s.Hue, col, row, p.GridCols, p.GridRows)
			if col == s.Col && row == s.Row {
				line += accentCursorSwatch(c, p.CellWidth)
			} else {
				line += accentSwatch(c, p.CellWidth)
			}
			x := 1 + col*p.CellWidth
			m.accentHits = append(m.accentHits, accentHit{
				Rect: overlay.Rect{X0: x, Y0: y + row, X1: x + p.CellWidth, Y1: y + row + 1},
				Kind: accentHitGrid, Col: col, Row: row,
			})
		}
		out = append(out, overlay.Fill(line, p.ColInner, bg))
	}
	return out
}

// accentSliderLines renders the five channels with a blank between the bytes
// and the two that move the colour as a whole, which are different kinds of
// control and one blank row is what says so.
func (m *OS) accentSliderLines(width, y int, pal overlay.Palette) []string {
	out := make([]string, 0, accentSliderRows)
	for ch := accentChannel(0); ch < accentChanCount; ch++ {
		if ch == accentChanS {
			out = append(out, overlay.Fill("", width, pal.Canvas))
		}
		out = append(out, m.accentSliderLine(ch, width, y+len(out), pal))
	}
	return out
}

// accentSlotWidth is how many cells one of the theme's colours is drawn in. Two
// rather than one: this row is the easy way in, and a one-cell chip reads as
// punctuation next to the grid below it.
const accentSlotWidth = 2

// accentSlotLines renders the theme's own colours as two rows, the bright eight
// and the seven normal ones under their bright counterparts, so a colour and its
// bright twin are one column apart rather than eight cells apart on one long
// row. Bright black has no normal twin, which is why the lower row starts one
// swatch in.
//
// Only the selected colour is named: fifteen names do not fit, and the name is
// the thing the row exists to offer, so it is printed for whichever swatch the
// cursor is on and printed in that colour, lifted until it reads on the dialog.
func (m *OS) accentSlotLines(width, y int, pal overlay.Palette) []string {
	bg := pal.Canvas
	s := &m.AccentPicker
	focused := s.Focus == accentFocusANSI

	rowFor := func(first, count, indent, rowY int) string {
		line := accentFocusMark(first == 0 && focused, bg, pal) +
			overlay.Style(bg).Render(strings.Repeat(" ", indent*accentSlotWidth))
		for i := first; i < first+count; i++ {
			c := SlotAccent(i).RGB()
			x := lipgloss.Width(line)
			if i == s.Slot {
				line += overlay.Style(accentShown(c)).Foreground(accentContrast(c)).Bold(true).Render(accentCursorGlyph()) +
					accentSwatch(c, accentSlotWidth-1)
			} else {
				line += accentSwatch(c, accentSlotWidth)
			}
			m.accentHits = append(m.accentHits, accentHit{
				Rect: overlay.Rect{X0: x, Y0: rowY, X1: x + accentSlotWidth, Y1: rowY + 1},
				Kind: accentHitANSI, Col: i,
			})
		}
		return line
	}

	bright := rowFor(0, accentBrightCount, 0, y)
	if s.Slot >= 0 {
		name := " " + accentSlotNames[s.Slot]
		if lipgloss.Width(bright)+lipgloss.Width(name) <= width {
			bright += overlay.Style(bg).Foreground(theme.Readable(SlotAccent(s.Slot).RGB(), bg)).Render(name)
		}
	}
	normal := rowFor(accentBrightCount, accentSwatchCount-accentBrightCount, 1, y+1)
	return []string{overlay.Fill(bright, width, bg), overlay.Fill(normal, width, bg)}
}

// accentNowLine renders the old-to-new readout and the control that clears the
// accent, which is the only thing on the line the mouse can press.
func (m *OS) accentNowLine(width, y int, pal overlay.Palette) string {
	bg := pal.Canvas
	arrow := " → "
	if overlay.UseASCII() {
		arrow = " -> "
	}
	s := &m.AccentPicker

	line := overlay.Style(bg).Foreground(pal.FgMute).Render(" now ")
	switch {
	case s.Src == accentSourceSession || s.Src == accentSourceAuto:
		// The colour is real but the target is not holding it: a pane is wearing
		// its session's, a session is wearing the one it was assigned. Naming the
		// source rather than printing a hex is the whole difference between the
		// two states, and the word fits where the hex would have gone.
		word := " session"
		if s.Src == accentSourceAuto {
			word = " auto"
		}
		line += accentSwatch(s.Prev.RGB(), 2) +
			overlay.Style(bg).Foreground(pal.FgDim).Render(word)
	case s.HadPrev:
		line += accentSwatch(s.Prev.RGB(), 2) +
			overlay.Style(bg).Foreground(pal.FgDim).Render(" "+s.Prev.Hex())
	default:
		line += overlay.Style(bg).Foreground(pal.FgMute).Render(accentClearGlyph() + " none")
	}
	line += overlay.Style(bg).Foreground(pal.FgMute).Render(arrow) +
		accentSwatch(s.Cur, 2) +
		overlay.Style(bg).Foreground(pal.Fg).Render(" "+hexString(s.Cur))

	// The clear control rides the right-hand end, where "none" already means
	// taking the accent away. It is dropped rather than overlapped when the
	// readout has used the whole line.
	clear := overlay.Style(bg).Foreground(pal.Warn).Render(accentClearGlyph()) +
		overlay.Style(bg).Render(" ")
	if gap := width - lipgloss.Width(line) - lipgloss.Width(clear); gap >= 1 {
		line += overlay.Style(bg).Render(strings.Repeat(" ", gap))
		x := lipgloss.Width(line)
		line += clear
		m.accentHits = append(m.accentHits, accentHit{
			Rect: overlay.Rect{X0: x, Y0: y, X1: x + 1, Y1: y + 1},
			Kind: accentHitClear,
		})
	}
	return overlay.Fill(line, width, bg)
}

// accentHexLine renders the typeable hex field and, when the terminal cannot
// show the colour the field names, what it was stepped down to.
func (m *OS) accentHexLine(width, y int, pal overlay.Palette) string {
	bg := pal.Canvas
	s := &m.AccentPicker
	focused := s.Focus == accentFocusHex

	label := overlay.Style(bg).Foreground(pal.FgMute).Render(" hex ")
	field := overlay.Style(bg).Foreground(pal.Fg).Render(s.Hex)
	line := label + accentFocusMark(focused, bg, pal) + field
	if focused {
		line += overlay.Cursor(" ", bg, pal.Fg)
	}

	// The honest part: on a terminal without truecolour the swatch beside this
	// hex is not that hex, and saying so is cheaper than letting the user
	// wonder why their colour looks wrong.
	if fb := accentFallbackLabel(s.Cur); fb != "" {
		note := overlay.Style(bg).Foreground(pal.Warning).Render("  ~" + fb)
		if lipgloss.Width(line)+lipgloss.Width(note) <= width {
			line += note
		}
	}

	// The whole field is the target: clicking anywhere on it takes the keyboard,
	// which is what a text field does.
	m.accentHits = append(m.accentHits, accentHit{
		Rect: overlay.Rect{X0: 0, Y0: y, X1: width, Y1: y + 1},
		Kind: accentHitHex,
	})
	return overlay.Fill(line, width, bg)
}

// accentSliderGlyphs are the run, the rest of the track, and the thumb.
func accentSliderGlyphs() (run, rest, thumb string) {
	if overlay.UseASCII() {
		return "=", "-", "+"
	}
	return "━", "─", "◆"
}

// accentSliderValueWidth is the cells the printed value is right-aligned in:
// three digits for a byte, or two and a percent sign.
const accentSliderValueWidth = 4

// accentSliderLine renders one channel: a sigil, the letter, the track with the
// thumb on it, and the number.
//
// The number is printed from the value the picker holds, not from the thumb's
// column. The bar quantises and the value does not, so deriving one from the
// other is how a slider comes to disagree with itself.
func (m *OS) accentSliderLine(ch accentChannel, width, y int, pal overlay.Palette) string {
	bg := pal.Canvas
	s := &m.AccentPicker
	focused := s.Focus == ch.focus()
	grabbed := m.accentDragging && m.accentDrag == accentHitSlider && m.accentDragCol == int(ch)

	barW := accentSliderBarWidth(width)
	v := s.sliderValue(ch)
	pos := accentSliderCol(v, barW, ch.max())
	run, rest, thumb := accentSliderGlyphs()
	runColor := ch.runColor(pal)

	line := accentFocusMark(focused, bg, pal) +
		overlay.Style(bg).Foreground(pal.FgDim).Render(ch.label()+" ")
	x := lipgloss.Width(line)

	if pos > 0 {
		line += overlay.Style(bg).Foreground(runColor).Render(strings.Repeat(run, pos))
	}
	// The thumb is lifted off its own run until it reads against it, which is the
	// same rule the cursor on a swatch follows.
	thumbStyle := overlay.Style(bg).Foreground(theme.Readable(runColor, bg)).Bold(true)
	if grabbed {
		thumbStyle = thumbStyle.Reverse(true)
	}
	line += thumbStyle.Render(thumb)
	if tail := barW - pos - 1; tail > 0 {
		line += overlay.Style(bg).Foreground(pal.FgMute).Render(strings.Repeat(rest, tail))
	}
	m.accentHits = append(m.accentHits, accentHit{
		Rect: overlay.Rect{X0: x, Y0: y, X1: x + barW, Y1: y + 1},
		Kind: accentHitSlider, Col: int(ch),
	})

	line += overlay.Style(bg).Render(" ") +
		overlay.Style(bg).Foreground(pal.Fg).Render(padLeft(ch.text(v), accentSliderValueWidth))
	return overlay.Fill(line, width, bg)
}

// padLeft right-aligns s in n cells.
func padLeft(s string, n int) string {
	if gap := n - lipgloss.Width(s); gap > 0 {
		return strings.Repeat(" ", gap) + s
	}
	return s
}

// accentHarmonyLines renders the harmony chips: even turns around the circle
// from the base colour's complement, each at the saturation and lightness the
// picker is holding.
//
// Wide and stacked draw the wheel; compact keeps the named three, where there is
// no room to draw a wheel and three relationships are worth more than three
// arbitrary points on one.
func (m *OS) accentHarmonyLines(p accentLayoutPlan, width, y int, pal overlay.Palette) []string {
	bg := pal.Canvas
	s := &m.AccentPicker
	focused := s.Focus == accentFocusHarmony
	count := p.HarmonyCount()

	var out []string
	if p.HarmonyLabel {
		out = append(out, overlay.Fill(
			overlay.Style(bg).Render(" ")+overlay.Style(bg).Foreground(pal.FgDim).Render("harmony"), width, bg))
	}

	labels := [accentHarmonyCompactCount]string{"comp ", " ana ", " "}
	for row := range p.HarmonyRows {
		if row > 0 && p.Blanks {
			out = append(out, overlay.Fill("", width, bg))
		}
		line := accentFocusMark(row == 0 && focused, bg, pal)
		for col := range p.HarmonyCols {
			i := row*p.HarmonyCols + col
			if count == accentHarmonyCompactCount {
				line += overlay.Style(bg).Foreground(pal.FgMute).Render(labels[i])
			} else if col > 0 && p.ChipGap > 0 {
				line += overlay.Style(bg).Render(strings.Repeat(" ", p.ChipGap))
			}
			c := s.harmonyColor(i, count)
			x := lipgloss.Width(line)
			line += accentChip(c, p.ChipWidth, focused && i == s.Harmony, pal)
			m.accentHits = append(m.accentHits, accentHit{
				Rect: overlay.Rect{X0: x, Y0: y + len(out), X1: x + p.ChipWidth, Y1: y + len(out) + 1},
				Kind: accentHitHarmony, Col: i,
			})
		}
		out = append(out, overlay.Fill(line, width, bg))
	}
	return out
}
