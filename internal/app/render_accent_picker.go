package app

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// accentPickerInnerWidth is the dialog's inner width: a marker, a swatch, a
// name and its digit is all a row carries.
const accentPickerInnerWidth = 28

// accentPickerHints are built per render because the key glyph follows ASCII
// mode.
func accentPickerHints() []overlay.Hint {
	return []overlay.Hint{
		{Key: overlay.EnterKey(), Label: "apply"},
		{Key: "esc", Label: "cancel"},
	}
}

// accentClearGlyph is the mark on the row that takes an accent away.
func accentClearGlyph() string {
	if overlay.UseASCII() {
		return "x"
	}
	return "✕"
}

// accentCurrentGlyph marks the row holding the accent the target already has.
// It is a different shape from the cursor marker so the two never read as each
// other when they land on different rows.
func accentCurrentGlyph() string {
	if overlay.UseASCII() {
		return "*"
	}
	return "●"
}

// accentPickerRow lays one slot row out on fixed columns: pad, cursor marker,
// swatch, then the name in its own colour, with the current-accent dot and the
// direct-select digit right-aligned.
func accentPickerRow(idx int, cursor, current bool, width int, pal overlay.Palette) string {
	bg := pal.Canvas
	if cursor {
		bg = pal.Surface
	}
	marker := " "
	if cursor {
		marker = overlay.SigilMark()
	}
	left := overlay.Style(bg).Render(" ") +
		overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render(marker) +
		overlay.Style(bg).Render(" ") +
		overlay.Style(accentColor(idx)).Render("  ") +
		overlay.Style(bg).Render(" ") +
		// The name is drawn in the colour it names, which is the one place the
		// list can show what a slot looks like on text rather than on a block.
		overlay.Style(bg).Foreground(accentColor(idx)).Bold(cursor).Render(accentNames[idx])

	right := overlay.Style(bg).Render("  ")
	if idx < 9 {
		right = overlay.Style(bg).Foreground(pal.FgMute).Render(strconv.Itoa(idx+1)) + right
	} else {
		right = overlay.Style(bg).Render(" ") + right
	}
	if current {
		right = overlay.Style(bg).Foreground(accentColor(idx)).Render(accentCurrentGlyph()) +
			overlay.Style(bg).Render(" ") + right
	} else {
		right = overlay.Style(bg).Render("  ") + right
	}

	gap := max(width-lipgloss.Width(left)-lipgloss.Width(right), 0)
	return left + overlay.Style(bg).Render(strings.Repeat(" ", gap)) + right
}

// renderAccentPicker draws the accent micro-dialog for the window being
// accented: an old-vs-new line, the slots with their names in their own
// colours, and the row that clears.
//
// It is a dialog rather than a panel because it chooses among a fixed small set
// rather than listing many things, and there is no search line: fifteen rows
// with digit direct-select would make one pure ceremony.
func (m *OS) renderAccentPicker() (string, overlay.Geometry, []overlayRowHit) {
	pal := theme.UI()
	rows := accentSwatchCount + 1 // the swatches, then the row that clears
	m.AccentPickerSelected = clampInt(m.AccentPickerSelected, 0, rows-1)

	width := overlay.DialogFitWidth(accentPickerInnerWidth, m.GetRenderWidth())
	bg := pal.Canvas

	// Rows the frame cannot carry are scrolled rather than dropped, so the
	// dialog fits a short screen without losing a slot.
	const furniture = 2 + 1 + 1 + 1 + 1 // borders, now line, two rules, clear row
	visible := clampInt(m.GetRenderHeight()-furniture, 1, accentSwatchCount)
	m.AccentPickerScroll = scrollWindow(m.AccentPickerScroll, min(m.AccentPickerSelected, accentSwatchCount-1), accentSwatchCount, visible)
	start := m.AccentPickerScroll
	end := min(start+visible, accentSwatchCount)

	current, hasCurrent := m.WindowAccent(m.AccentPickerWindowID)

	body := []string{
		m.accentNowLine(width, current, hasCurrent, pal),
		overlay.Fill(overlay.Style(bg).Render(" ")+overlay.DashRule(max(width-2, 0), bg, pal), width, bg),
	}
	for i := start; i < end; i++ {
		body = append(body, accentPickerRow(i, i == m.AccentPickerSelected, hasCurrent && i == current, width, pal))
	}
	body = append(body,
		overlay.Fill(overlay.Style(bg).Render(" ")+overlay.DashRule(max(width-2, 0), bg, pal), width, bg),
		m.accentClearRow(width, m.AccentPickerSelected == accentSwatchCount, pal))

	content, geo := overlay.Dialog{
		Title: "accent",
		Width: width,
		Body:  strings.Join(body, "\n"),
		Hints: accentPickerHints(),
	}.Render(pal)

	// One rect per drawn row, in drawn order, so a click lands on the slot the
	// user is pointing at whatever the list is scrolled to.
	hits := make([]overlayRowHit, 0, end-start+1)
	for i := start; i < end; i++ {
		y := geo.BodyY + 2 + (i - start)
		hits = append(hits, overlayRowHit{
			Rect: overlay.Rect{X0: 0, Y0: y, X1: geo.Width, Y1: y + 1},
			Idx:  i,
		})
	}
	clearY := geo.BodyY + 2 + (end - start) + 1
	hits = append(hits, overlayRowHit{
		Rect: overlay.Rect{X0: 0, Y0: clearY, X1: geo.Width, Y1: clearY + 1},
		Idx:  accentSwatchCount,
	})
	return content, geo, hits
}

// accentNowLine renders the old-vs-new readout.
func (m *OS) accentNowLine(width, current int, hasCurrent bool, pal overlay.Palette) string {
	bg := pal.Canvas
	arrow := " → "
	if overlay.UseASCII() {
		arrow = " -> "
	}
	slot := func(idx int, ok bool) string {
		if !ok {
			return overlay.Style(bg).Foreground(pal.FgMute).Render(accentClearGlyph() + " none")
		}
		return overlay.Style(accentColor(idx)).Render("  ") +
			overlay.Style(bg).Foreground(accentColor(idx)).Render(" "+accentNames[idx])
	}
	next, hasNext := m.accentPreview(m.AccentPickerWindowID)
	line := overlay.Style(bg).Foreground(pal.FgMute).Render(" now ") +
		slot(current, hasCurrent) +
		overlay.Style(bg).Foreground(pal.FgMute).Render(arrow) +
		slot(next, hasNext)
	if lipgloss.Width(line) > width {
		line = overlay.Truncate(stripANSIForTrace(line), width)
		line = overlay.Style(bg).Foreground(pal.FgDim).Render(line)
	}
	return overlay.Fill(line, width, bg)
}

// accentClearRow is the row that takes the accent away.
func (m *OS) accentClearRow(width int, cursor bool, pal overlay.Palette) string {
	bg := pal.Canvas
	if cursor {
		bg = pal.Surface
	}
	marker := " "
	if cursor {
		marker = overlay.SigilMark()
	}
	row := overlay.Style(bg).Render(" ") +
		overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render(marker) +
		overlay.Style(bg).Render(" ") +
		overlay.Style(bg).Foreground(pal.FgMute).Render(accentClearGlyph()) +
		overlay.Style(bg).Foreground(pal.FgMute).Bold(cursor).Render(" none")
	return overlay.Fill(row, width, bg)
}
