package app

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// Settings overlay layout constants.
const (
	settingsInnerWidth  = 62
	settingsVisibleRows = 7
)

// renderSettings draws the settings overlay and returns the rendered panel, its
// geometry, and the per-row hit rectangles for mouse routing.
func (m *OS) renderSettings() (string, overlay.Geometry, []overlayRowHit) {
	cats := m.settingsCategories()
	if len(cats) == 0 {
		return "", overlay.Geometry{}, nil
	}
	m.SettingsCategory = clampInt(m.SettingsCategory, 0, len(cats)-1)
	cat := cats[m.SettingsCategory]
	if len(cat.Items) > 0 {
		m.SettingsSelected = clampInt(m.SettingsSelected, 0, len(cat.Items)-1)
	} else {
		m.SettingsSelected = 0
	}

	pal := theme.UI()
	bg := pal.Surface

	// Build each row and remember its control width so the hit rects can be
	// derived from the panel geometry afterward.
	type rowInfo struct {
		control string
		isBool  bool
	}
	var lines []string
	infos := make([]rowInfo, len(cat.Items))
	for i, item := range cat.Items {
		line, control, isBool := m.settingsRow(item, i == m.SettingsSelected, pal)
		lines = append(lines, line)
		infos[i] = rowInfo{control: control, isBool: isBool}
	}
	for len(lines) < settingsVisibleRows {
		lines = append(lines, overlay.Style(bg).Render(" "))
	}

	desc := ""
	if len(cat.Items) > 0 {
		desc = cat.Items[m.SettingsSelected].Desc
	}
	lines = append(lines,
		overlay.Style(bg).Render(" "),
		overlay.Style(bg).Foreground(pal.FgMute).Italic(true).Render("  "+desc),
	)

	tabs := make([]string, len(cats))
	for i, c := range cats {
		tabs[i] = c.Name
	}

	panel := overlay.Panel{
		Glyph:     "", // gear
		Title:     "Settings",
		Width:     settingsInnerWidth,
		Tabs:      tabs,
		ActiveTab: m.SettingsCategory,
		Body:      strings.Join(lines, "\n"),
		Hints: []overlay.Hint{
			{Key: "↑↓", Label: "move"},
			{Key: "←→", Label: "change"},
			{Key: "tab", Label: "section"},
			{Key: "esc", Label: "close"},
		},
	}
	content, geo := panel.Render(pal)

	// Derive hit rects: each row spans the full panel width at BodyY+i; the
	// control sits right-aligned in the inner area.
	rows := make([]overlayRowHit, 0, len(cat.Items))
	for i, info := range infos {
		rowY := geo.BodyY + i
		full := overlay.Rect{X0: 0, Y0: rowY, X1: geo.Width, Y1: rowY + 1}
		ctrlW := lipgloss.Width(info.control)
		ctrlX := geo.BodyX + geo.InnerWidth - ctrlW
		hit := overlayRowHit{Rect: full, Idx: i}
		if info.isBool {
			hit.Inc = overlay.Rect{X0: ctrlX, Y0: rowY, X1: ctrlX + ctrlW, Y1: rowY + 1}
		} else {
			hit.Dec = overlay.Rect{X0: ctrlX, Y0: rowY, X1: ctrlX + 2, Y1: rowY + 1}
			hit.Inc = overlay.Rect{X0: ctrlX + ctrlW - 2, Y0: rowY, X1: ctrlX + ctrlW, Y1: rowY + 1}
		}
		rows = append(rows, hit)
	}

	return content, geo, rows
}

// settingsRow renders a single setting and returns the row string, its control
// string (for hit-rect sizing), and whether the control is a boolean toggle.
func (m *OS) settingsRow(item settingItem, selected bool, pal overlay.Palette) (string, string, bool) {
	bg := pal.Surface
	marker := "  "
	if selected {
		bg = pal.RowSel
		marker = "› "
	}

	isBool := item.Control == controlBool
	var control string
	switch item.Control {
	case controlBool:
		control = overlay.Toggle(item.boolVal(m), selected, bg, pal)
	case controlString:
		control = m.settingsStringControl(item, selected, bg, pal)
	default:
		control = overlay.Cycler(item.value(m), selected, bg, pal)
	}

	labelColor := pal.Fg
	if !selected {
		labelColor = pal.FgDim
	}
	left := overlay.Style(bg).Foreground(pal.Accent).Bold(true).Render(marker) +
		overlay.Style(bg).Foreground(labelColor).Bold(selected).Render(item.Label)

	gap := max(settingsInnerWidth-lipgloss.Width(left)-lipgloss.Width(control), 1)
	return left + overlay.Style(bg).Render(strings.Repeat(" ", gap)) + control, control, isBool
}

// settingsStringControl renders a free-text setting as a bracketed field. An
// empty value shows the placeholder greyed out; while the row is being edited it
// shows the live buffer with a trailing cursor.
func (m *OS) settingsStringControl(item settingItem, selected bool, bg color.Color, pal overlay.Palette) string {
	editing := m.SettingsEditing && selected
	val := item.value(m)
	if editing {
		val = m.SettingsEditBuffer
	}

	fg := pal.Fg
	if !selected {
		fg = pal.FgDim
	}
	text := val
	if val == "" && !editing {
		text = item.Placeholder
		fg = pal.FgMute
	}
	text = overlay.Truncate(text, 30)
	if editing {
		cursor := "▏"
		if overlay.ASCII {
			cursor = "_"
		}
		text += cursor
	}

	bracketColor := pal.FgMute
	if selected {
		bracketColor = pal.AccentBright
	}
	bracket := overlay.Style(bg).Foreground(bracketColor)
	return bracket.Render("[ ") +
		overlay.Style(bg).Foreground(fg).Render(text) +
		bracket.Render(" ]")
}
