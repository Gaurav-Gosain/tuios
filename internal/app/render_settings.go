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

	// settingsDescLines is how many lines the description box holds when the
	// screen has the room. Two covers every description in the set at the
	// panel's maximum width, and the box is a fixed height so the panel does
	// not jump as the selection crosses from a short description to a long one.
	settingsDescLines = 2
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
// fit, the footer, and how many lines the description box gets.
//
// On a screen too short to hold the minimum body, the footer goes first
// (panelBody), then the description box loses a line at a time and finally goes
// altogether: it restates the selected row, whereas a body below three rows
// stops being a list.
func (m *OS) settingsLayout(tabs []string, itemCount int) (width, rows int, hints []overlay.Hint, descLines int) {
	width = m.panelWidth(settingsMaxInnerWidth)
	preferred := max(itemCount, settingsVisibleRows)
	rh := m.GetRenderHeight()

	for n := settingsDescLines; n > 0; n-- {
		// Body lines that are not setting rows: the blank above the box, then
		// the box itself.
		extra := n + 1
		rows, hints = m.panelBody(preferred, extra, width, tabs, settingsHints)
		if rh <= 0 || rows+extra+panelChrome(0, width, tabs, hints) <= rh {
			return width, rows, hints, n
		}
	}
	rows, hints = m.panelBody(preferred, 0, width, tabs, settingsHints)
	return width, rows, hints, 0
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

	width, visible, hints, descLines := m.settingsLayout(tabs, len(cat.Items))
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

	if descLines > 0 {
		desc := ""
		if len(cat.Items) > 0 {
			desc = cat.Items[m.SettingsSelected].Desc
		}
		if len(cat.Items) > visible {
			// Say where in the category the selection is, so a scrolled-off setting
			// is discoverable rather than simply missing.
			desc = lipgloss.Sprintf("(%d/%d) ", m.SettingsSelected+1, len(cat.Items)) + desc
		}
		lines = append(lines, overlay.Style(bg).Render(" "))
		lines = append(lines, settingsDescription(desc, width, descLines, pal)...)
	}

	// A session that cannot write the config file says so in the title rather
	// than only in a notification, which is gone by the time the second setting
	// is changed.
	title := "Settings"
	if m.ConfigReadOnly {
		title = "Settings (this session only)"
	}

	panel := overlay.Panel{
		Glyph:     "", // gear
		Title:     title,
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

// settingsDescription lays the selected row's help text into a box n lines tall.
//
// It wraps rather than truncates. The panel has the room across two lines for
// every description it carries, and a sentence cut off at the first line is a
// sentence the user has to go and find in the config file; the mark said only
// that something was missing. A description longer than the box still has its
// last line truncated with the usual mark, so nothing ever paints past the
// panel edge.
//
// The box is padded out to n lines whatever the text needs, so the panel keeps
// its height as the selection moves and does not re-centre itself under the
// pointer.
func settingsDescription(desc string, width, n int, pal overlay.Palette) []string {
	// FgDim, not FgMute: this is the only line that says what a setting does,
	// and the quiet tier is furniture rather than text.
	style := overlay.Style(pal.Surface).Foreground(pal.FgDim).Italic(true)
	// The "  " indent counts against the inner width, same as the marker does on
	// a setting row, so the wrap target leaves room for it.
	avail := width - 2
	wrapped := wrapPlain(desc, avail)
	out := make([]string, 0, n)
	for i := range n {
		text := ""
		switch {
		case i == n-1 && len(wrapped) > n:
			// More than the box holds: the last line carries what is left of the
			// text up to the mark, so the cut is visible rather than silent.
			text = overlay.Truncate(strings.Join(wrapped[i:], " "), avail)
		case i < len(wrapped):
			text = wrapped[i]
		}
		out = append(out, style.Render("  "+text))
	}
	return out
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
	left := overlay.Style(bg).Foreground(theme.ReadableAt(pal.Accent, bg, theme.MarkFloor)).Bold(true).Render(marker) +
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
		text, italic, fg = item.Unset, true, pal.FgDim
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
