package app

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

const accentPickerInnerWidth = 34

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
	accentHitNamed
	accentHitHint
)

const (
	accentHintFocus = iota
	accentHintApply
	accentHintClear
	accentHintCancel
)

type accentHit struct {
	Rect     overlay.Rect
	Kind     accentHitKind
	Col, Row int
}

func accentPickerHints() []overlay.Hint {
	return []overlay.Hint{
		{Key: "tab", Label: "field"},
		{Key: overlay.EnterKey(), Label: "apply"},
		{Key: "x", Label: "clear"},
		{Key: "esc", Label: "cancel"},
	}
}

func accentClearGlyph() string {
	if overlay.UseASCII() {
		return "x"
	}
	return "✕"
}

func accentCursorGlyph() string {
	if overlay.UseASCII() {
		return "+"
	}
	return "◆"
}

func accentFocusMark(on bool, bg color.Color, pal overlay.Palette) string {
	if !on {
		return overlay.Style(bg).Render(" ")
	}
	return overlay.Style(bg).Foreground(theme.Readable(pal.AccentBright, bg)).Bold(true).Render(overlay.SigilMark())
}

func accentPaint(c color.Color) color.Color {
	if rgba, ok := c.(color.RGBA); ok {
		return accentShown(rgba)
	}
	return c
}

func accentSwatch(c color.Color, n int) string {
	return overlay.Style(accentPaint(c)).Render(strings.Repeat(" ", n))
}

func accentCursorSwatch(c color.Color, n int) string {
	return overlay.Style(accentPaint(c)).Foreground(accentContrast(toRGBA(c))).Bold(true).
		Render(accentCentred(accentCursorGlyph(), n))
}

func accentCentred(mark string, n int) string {
	n = max(n, 1)
	lead := (n - 1) / 2
	return strings.Repeat(" ", lead) + mark + strings.Repeat(" ", n-1-lead)
}

func accentChipGlyph() string {
	if overlay.UseASCII() {
		return "o"
	}
	return "●"
}

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

func (m *OS) renderAccentPicker() (string, overlay.Geometry, []overlayRowHit) {
	pal := m.overlayUI()
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
	switch m.AccentPickerTarget {
	case AccentTargetSession:
		title = "session accent"
	case AccentTargetSetting:
		if cs, ok := lookupColorSetting(m.AccentPickerTargetID); ok {
			title = strings.ToLower(cs.Label)
		}
	case AccentTargetThemeRole:
		if row, ok := m.themeEditorRowByID(m.AccentPickerTargetID); ok {
			title = strings.ToLower(row.label)
		}
	}
	content, geo := overlay.Dialog{
		Title: title,
		Width: p.Inner,
		Body:  strings.Join(body, "\n"),
		Hints: accentPickerHints(),
	}.Render(pal)

	for i := range m.accentHits {
		r := &m.accentHits[i].Rect
		r.X0, r.X1 = r.X0+geo.BodyX, r.X1+geo.BodyX
		r.Y0, r.Y1 = r.Y0+geo.BodyY, r.Y1+geo.BodyY
	}

	for i, r := range geo.Hints {
		if !r.Empty() {
			m.accentHits = append(m.accentHits, accentHit{Rect: r, Kind: accentHitHint, Col: i})
		}
	}

	return content, geo, nil
}

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

func accentColumnRule() string {
	if overlay.UseASCII() {
		return "|"
	}
	return "┆"
}

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

func (m *OS) accentSpaceColumn(p accentLayoutPlan, pal overlay.Palette) []string {
	bg := pal.Canvas
	var body []string
	at := func() int { return len(body) }

	body = append(body, m.accentNamedLines(p.ColInner, at(), pal)...)
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

func (m *OS) accentStackedBody(p accentLayoutPlan, pal overlay.Palette) []string {
	var body []string
	at := func() int { return len(body) }

	body = append(body, m.accentNamedLines(p.Inner, at(), pal)...)
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

func (m *OS) accentRuleLine(width int, pal overlay.Palette) string {
	bg := pal.Canvas
	return overlay.Fill(overlay.Style(bg).Render(" ")+overlay.DashRule(max(width-2, 0), bg, pal), width, bg)
}

func (m *OS) accentHueLine(p accentLayoutPlan, y int, pal overlay.Palette) string {
	bg := pal.Canvas
	s := &m.AccentPicker
	cells := p.HueCells
	held := accentHueCell(s.Hue, cells)

	var line strings.Builder
	line.WriteString(accentFocusMark(s.Focus == accentFocusHue, bg, pal))
	for i := range cells {
		c := hslToRGB(accentHueAt(i, cells), 1, 0.5)
		if i == held {
			line.WriteString(accentCursorSwatch(c, 1))
		} else {
			line.WriteString(accentSwatch(c, 1))
		}
		m.accentHits = append(m.accentHits, accentHit{
			Rect: overlay.Rect{X0: 1 + i, Y0: y, X1: 2 + i, Y1: y + 1},
			Kind: accentHitHue, Col: i,
		})
	}
	return overlay.Fill(line.String(), p.ColInner, bg)
}

func (m *OS) accentGridLines(p accentLayoutPlan, y int, pal overlay.Palette) []string {
	bg := pal.Canvas
	s := &m.AccentPicker
	out := make([]string, 0, p.GridRows)
	for row := range p.GridRows {
		var line strings.Builder
		line.WriteString(accentFocusMark(row == 0 && s.Focus == accentFocusGrid, bg, pal))
		for col := range p.GridCols {
			c := accentCellColor(s.Hue, col, row, p.GridCols, p.GridRows)
			if col == s.Col && row == s.Row {
				line.WriteString(accentCursorSwatch(c, p.CellWidth))
			} else {
				line.WriteString(accentSwatch(c, p.CellWidth))
			}
			x := 1 + col*p.CellWidth
			m.accentHits = append(m.accentHits, accentHit{
				Rect: overlay.Rect{X0: x, Y0: y + row, X1: x + p.CellWidth, Y1: y + row + 1},
				Kind: accentHitGrid, Col: col, Row: row,
			})
		}
		out = append(out, overlay.Fill(line.String(), p.ColInner, bg))
	}
	return out
}

func (m *OS) accentSliderLines(width, y int, pal overlay.Palette) []string {
	out := make([]string, 0, accentSliderRows)
	for ch := range accentChanCount {
		if ch == accentChanS {
			out = append(out, overlay.Fill("", width, pal.Canvas))
		}
		out = append(out, m.accentSliderLine(ch, width, y+len(out), pal))
	}
	return out
}

const accentSlotWidth = 2

func (m *OS) accentSlotLines(width, y int, pal overlay.Palette) []string {
	bg := pal.Canvas
	s := &m.AccentPicker
	focused := s.Focus == accentFocusANSI

	rowFor := func(first, count, indent, rowY int) string {
		line := accentFocusMark(first == 0 && focused, bg, pal) +
			overlay.Style(bg).Render(strings.Repeat(" ", indent*accentSlotWidth))
		for i := first; i < first+count; i++ {
			c := SlotAccent(i).Color()
			x := lipgloss.Width(line)
			if i == s.Slot {
				line += overlay.Style(accentPaint(c)).Foreground(accentContrast(toRGBA(c))).Bold(true).Render(accentCursorGlyph()) +
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

func (m *OS) accentNamedLines(width, y int, pal overlay.Palette) []string {
	s := &m.AccentPicker
	if len(s.NamedOpts) == 0 {
		return nil
	}
	bg := pal.Canvas
	focused := s.Focus == accentFocusNamed

	line := accentFocusMark(focused, bg, pal)
	for i, name := range s.NamedOpts {
		c, ok := m.namedColor(name)
		if !ok {
			continue
		}
		x := lipgloss.Width(line)
		chip := accentSwatch(c, accentSlotWidth)
		if name == s.Named {
			chip = accentCursorSwatch(c, accentSlotWidth)
		}
		ink := pal.FgDim
		if name == s.Named {
			ink = theme.Readable(pal.AccentBright, bg)
		}
		label := overlay.Style(bg).Foreground(ink).Bold(name == s.Named).Render(" " + name)
		next := line + chip + label
		if lipgloss.Width(next) > width {
			break
		}
		line = next
		m.accentHits = append(m.accentHits, accentHit{
			Rect: overlay.Rect{X0: x, Y0: y, X1: lipgloss.Width(line), Y1: y + 1},
			Kind: accentHitNamed, Col: i,
		})
		if i < len(s.NamedOpts)-1 {
			line += overlay.Style(bg).Render(" ")
		}
	}
	return []string{overlay.Fill(line, width, bg)}
}

func (m *OS) accentNowLine(width, y int, pal overlay.Palette) string {
	bg := pal.Canvas
	arrow := " → "
	if overlay.UseASCII() {
		arrow = " -> "
	}
	s := &m.AccentPicker

	line := overlay.Style(bg).Foreground(pal.FgMute).Render(" now ")
	switch {
	case s.Src.derived():
		word := " session"
		switch {
		case s.Src == accentSourceAuto:
			word = " auto"
		case s.SrcWord != "":
			word = " " + s.SrcWord
		}
		line += accentSwatch(s.Prev.Color(), 2) +
			overlay.Style(bg).Foreground(pal.FgDim).Render(word)
	case s.HadPrev:
		line += accentSwatch(s.Prev.Color(), 2) +
			overlay.Style(bg).Foreground(pal.FgDim).Render(" "+s.Prev.Label())
	default:
		line += overlay.Style(bg).Foreground(pal.FgMute).Render(accentClearGlyph() + " none")
	}
	now := overlay.Hex(s.Cur)
	if s.Named != "" {
		now = s.Named
	}
	line += overlay.Style(bg).Foreground(pal.FgMute).Render(arrow) +
		accentSwatch(s.Cur, 2) +
		overlay.Style(bg).Foreground(pal.Fg).Render(" "+now)

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

	if fb := accentFallbackLabel(s.Cur); fb != "" {
		note := overlay.Style(bg).Foreground(pal.Warning).Render("  ~" + fb)
		if lipgloss.Width(line)+lipgloss.Width(note) <= width {
			line += note
		}
	}

	m.accentHits = append(m.accentHits, accentHit{
		Rect: overlay.Rect{X0: 0, Y0: y, X1: width, Y1: y + 1},
		Kind: accentHitHex,
	})
	return overlay.Fill(line, width, bg)
}

func accentSliderGlyphs() (run, rest, thumb string) {
	if overlay.UseASCII() {
		return "=", "-", "+"
	}
	return "━", "─", "◆"
}

const accentSliderValueWidth = 4

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

func padLeft(s string, n int) string {
	if gap := n - lipgloss.Width(s); gap > 0 {
		return strings.Repeat(" ", gap) + s
	}
	return s
}

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
