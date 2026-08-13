package app

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// Settings overlay layout constants. These are the preferred sizes; a narrower
// or shorter screen gets a panel fitted to it (see overlay_fit.go).
const (
	// settingsMaxInnerWidth is as wide as the panel grows. A settings row is a
	// label on the left and its control on the right, and past about eighty
	// columns the eye stops carrying one to the other: the panel would be wider
	// but every row in it harder to read. Eighty also clears the section strip's
	// natural sixty-three columns with room for another section or a longer name
	// before a desktop terminal has to scroll it, and leaves all but one of the
	// descriptions a single line. It used to be 62, which is one column short of
	// the strip and is why the strip wrapped.
	settingsMaxInnerWidth = 80
	settingsVisibleRows   = 7
)

// settingsHints is the settings footer, shared by the renderer and the sizing
// helper so both measure the same panel.
var settingsHints = []overlay.Hint{
	{Key: "↑↓", Label: "move"},
	{Key: "←→", Label: "change"},
	{Key: "tab", Label: "section"},
	{Key: "esc", Label: "close"},
}

// settingsLayout returns the fitted inner width, the number of setting rows that
// fit, the footer, and whether there is room for the description line, given the
// category tabs (which may wrap onto several rows).
//
// On a screen too short to hold the minimum body, the footer goes first
// (panelBody), then the description: it restates the selected row, whereas a
// body below three rows stops being a list. Without this a second tab row would
// push the panel one line past a 12-row screen.
func (m *OS) settingsLayout(tabs []string, itemCount int) (width, rows int, hints []overlay.Hint, desc bool) {
	width = m.panelWidth(settingsMaxInnerWidth)
	preferred := max(itemCount, settingsVisibleRows)
	// Body lines that are not setting rows: a blank and the description.
	const descRows = 2
	rows, hints = m.panelBody(preferred, descRows, width, tabs, settingsHints)

	rh := m.GetRenderHeight()
	if rh <= 0 || rows+descRows+panelChrome(0, width, tabs, hints) <= rh {
		return width, rows, hints, true
	}
	rows, hints = m.panelBody(preferred, 0, width, tabs, settingsHints)
	return width, rows, hints, false
}

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

	tabs := make([]string, len(cats))
	for i, c := range cats {
		tabs[i] = c.Name
	}

	width, visible, hints, showDesc := m.settingsLayout(tabs, len(cat.Items))
	// A category with more settings than fit scrolls; the selection stays in
	// view so the arrow keys can always reach every setting.
	m.SettingsScroll = scrollWindow(m.SettingsScroll, m.SettingsSelected, len(cat.Items), visible)
	start := m.SettingsScroll
	end := min(start+visible, len(cat.Items))

	// Build each row and remember its control width so the hit rects can be
	// derived from the panel geometry afterward.
	type rowInfo struct {
		control string
		isBool  bool
		idx     int
	}
	var lines []string
	infos := make([]rowInfo, 0, end-start)
	for i := start; i < end; i++ {
		line, control, isBool := m.settingsRow(cat.Items[i], i == m.SettingsSelected, pal, width)
		lines = append(lines, line)
		infos = append(infos, rowInfo{control: control, isBool: isBool, idx: i})
	}
	for len(lines) < visible {
		lines = append(lines, overlay.Style(bg).Render(" "))
	}

	if showDesc {
		desc := ""
		if len(cat.Items) > 0 {
			desc = cat.Items[m.SettingsSelected].Desc
		}
		if len(cat.Items) > visible {
			// Say where in the category the selection is, so a scrolled-off setting
			// is discoverable rather than simply missing.
			desc = lipgloss.Sprintf("(%d/%d) ", m.SettingsSelected+1, len(cat.Items)) + desc
		}
		// "  " prefix counts against the inner width budget, same as the row
		// marker does for setting rows, so the truncation target leaves room
		// for it.
		desc = overlay.Truncate(desc, width-2)
		lines = append(lines,
			overlay.Style(bg).Render(" "),
			overlay.Style(bg).Foreground(pal.FgMute).Italic(true).Render("  "+desc),
		)
	}

	panel := overlay.Panel{
		Glyph:     "", // gear
		Title:     "Settings",
		Width:     width,
		Tabs:      tabs,
		ActiveTab: m.SettingsCategory,
		Body:      strings.Join(lines, "\n"),
		Hints:     hints,
	}
	content, geo := panel.Render(pal)

	// Derive hit rects: each row spans the full panel width at BodyY+i; the
	// control sits right-aligned in the inner area.
	rows := make([]overlayRowHit, 0, len(infos))
	for i, info := range infos {
		rowY := geo.BodyY + i
		full := overlay.Rect{X0: 0, Y0: rowY, X1: geo.Width, Y1: rowY + 1}
		ctrlW := lipgloss.Width(info.control)
		ctrlX := geo.BodyX + geo.InnerWidth - ctrlW
		hit := overlayRowHit{Rect: full, Idx: info.idx}
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
func (m *OS) settingsRow(item settingItem, selected bool, pal overlay.Palette, width int) (string, string, bool) {
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
		control = m.settingsStringControl(item, selected, bg, pal, width)
	default:
		control = overlay.Cycler(overlay.Truncate(item.value(m), max(width/2, 6)), selected, bg, pal)
	}

	labelColor := pal.Fg
	if !selected {
		labelColor = pal.FgDim
	}
	// The label yields to the control: the control is the part the row is for.
	label := overlay.Truncate(item.Label, max(width-lipgloss.Width(control)-3, 1))
	left := overlay.Style(bg).Foreground(pal.Accent).Bold(true).Render(marker) +
		overlay.Style(bg).Foreground(labelColor).Bold(selected).Render(label)

	gap := max(width-lipgloss.Width(left)-lipgloss.Width(control), 1)
	return left + overlay.Style(bg).Render(strings.Repeat(" ", gap)) + control, control, isBool
}

// settingsStringControl renders a free-text setting as a bracketed field. An
// unset value shows the marker in italic muted text; while the row is being
// edited it shows the live buffer with a trailing cursor.
func (m *OS) settingsStringControl(item settingItem, selected bool, bg color.Color, pal overlay.Palette, width int) string {
	editing := m.SettingsEditing && selected
	val := item.value(m)
	if editing {
		val = m.SettingsEditBuffer
	}

	fg := pal.Fg
	if !selected {
		fg = pal.FgDim
	}
	// An unset field says it is unset. It used to print its own placeholder
	// example in the value's own style, which reads as the value in force: the
	// panel told the user their focused border was #89b4fa while the border on
	// screen was the theme's. The example lives on the description line now.
	text, italic := val, false
	if val == "" && !editing {
		text, italic, fg = item.Unset, true, pal.FgMute
	}
	// The field never takes more than half the row, so the label it sits beside
	// keeps something to show even on a narrow panel.
	text = overlay.Truncate(text, min(30, max(width/2, 6)))
	if editing {
		cursor := "▏"
		if overlay.UseASCII() {
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
		overlay.Style(bg).Foreground(fg).Italic(italic).Render(text) +
		bracket.Render(" ]")
}
