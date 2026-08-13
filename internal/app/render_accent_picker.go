package app

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// accentPickerInnerWidth is the dialog's preferred inner width. It is the width
// of the shades grid plus a pad column either side, and the widest line the
// dialog carries is the old-to-new readout with two hexes on it.
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

// renderAccentPicker draws the colour picker for the window being accented and
// records the hit geometry of everything in it.
//
// Top to bottom: the hue strip, the shades grid holding that hue with
// saturation across and lightness down, then the old-to-new readout, the hex
// field, and the harmony chips. The keyboard reaches all four with tab and the
// arrows; the mouse reaches every cell of them through the rects recorded here.
func (m *OS) renderAccentPicker() (string, overlay.Geometry, []overlayRowHit) {
	pal := theme.UI()
	bg := pal.Canvas
	width := overlay.DialogFitWidth(accentPickerInnerWidth, m.GetRenderWidth())
	cols, rows := m.accentGridSize()
	s := &m.AccentPicker
	m.accentHits = m.accentHits[:0]

	// Body rows are laid out first and the dialog's own border row is added to
	// every y afterwards, so the recorded rects and the drawn rows come off the
	// same counter.
	var body []string
	at := func() int { return len(body) }

	// The hue strip: one cell per step around the circle, the held hue marked.
	hueY := at()
	hueCell := accentHueCell(s.Hue, cols)
	strip := accentFocusMark(s.Focus == accentFocusHue, bg, pal)
	for i := range cols {
		c := hslToRGB(accentHueAt(i, cols), 1, 0.5)
		if i == hueCell {
			strip += overlay.Style(accentShown(c)).Foreground(accentContrast(c)).Bold(true).Render(accentCursorGlyph())
		} else {
			strip += accentSwatch(c, 1)
		}
		m.accentHits = append(m.accentHits, accentHit{
			Rect: overlay.Rect{X0: 1 + i, Y0: hueY, X1: 2 + i, Y1: hueY + 1},
			Kind: accentHitHue, Col: i,
		})
	}
	body = append(body, overlay.Fill(strip, width, bg))

	// The shades grid.
	for row := range rows {
		y := at()
		line := accentFocusMark(row == 0 && s.Focus == accentFocusGrid, bg, pal)
		for col := range cols {
			c := accentCellColor(s.Hue, col, row, cols, rows)
			if col == s.Col && row == s.Row {
				line += overlay.Style(accentShown(c)).Foreground(accentContrast(c)).Bold(true).Render(accentCursorGlyph())
			} else {
				line += accentSwatch(c, 1)
			}
			m.accentHits = append(m.accentHits, accentHit{
				Rect: overlay.Rect{X0: 1 + col, Y0: y, X1: 2 + col, Y1: y + 1},
				Kind: accentHitGrid, Col: col, Row: row,
			})
		}
		body = append(body, overlay.Fill(line, width, bg))
	}

	body = append(body, overlay.Fill(overlay.Style(bg).Render(" ")+overlay.DashRule(max(width-2, 0), bg, pal), width, bg))
	body = append(body, m.accentNowLine(width, at(), pal))
	body = append(body, m.accentHexLine(width, at(), pal))
	body = append(body, m.accentHarmonyLine(width, at(), pal))

	title := "accent"
	if m.AccentPickerTarget == AccentTargetSession {
		// A session's colour is shared by every client attached to it, so the
		// dialog says which of the two the user is about to change.
		title = "session accent"
	}
	content, geo := overlay.Dialog{
		Title: title,
		Width: width,
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

	// The picker routes its own clicks off the rects above, so it registers no
	// generic body rows: a row hit would swallow the click before it could reach
	// the cell under it.
	return content, geo, nil
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

// accentHarmonyLine renders the complement and the two analogous neighbours of
// the picker's base colour.
func (m *OS) accentHarmonyLine(width, y int, pal overlay.Palette) string {
	bg := pal.Canvas
	s := &m.AccentPicker
	focused := s.Focus == accentFocusHarmony

	line := accentFocusMark(focused, bg, pal)
	labels := [accentHarmonyCount]string{"comp ", " ana ", " "}
	for i := range accentHarmonyCount {
		line += overlay.Style(bg).Foreground(pal.FgMute).Render(labels[i])
		c := s.harmonyColor(i)
		x := lipgloss.Width(line)
		if focused && i == s.Harmony {
			line += overlay.Style(accentShown(c)).Foreground(accentContrast(c)).Bold(true).Render(accentCursorGlyph()) +
				accentSwatch(c, 3)
		} else {
			line += accentSwatch(c, 4)
		}
		m.accentHits = append(m.accentHits, accentHit{
			Rect: overlay.Rect{X0: x, Y0: y, X1: x + 4, Y1: y + 1},
			Kind: accentHitHarmony, Col: i,
		})
	}
	return overlay.Fill(line, width, bg)
}
