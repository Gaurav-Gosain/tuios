package app

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// Theme picker layout constants. These are the preferred sizes; a narrower or
// shorter screen gets a panel fitted to it (see overlay_fit.go).
const (
	themePickerInnerWidth  = 52
	themePickerVisibleRows = 10
)

// themePickerHints is the theme picker footer, shared by the renderer and the
// sizing helper so both measure the same panel. A served client does not
// preview by applying (see previewsLive), so its arrows only move.
var themePickerHints = []overlay.Hint{
	{Key: "type", Label: "filter"},
	{Key: "↑↓", Label: "preview"},
	{Key: overlay.EnterGlyph, Label: "apply"},
	{Key: "esc", Label: "cancel"},
}

// themePickerCommands are the ctrl chords, on a strip of their own above the
// footer: one footer row holds only four hints at this width.
var themePickerCommands = []overlay.Hint{
	{Key: "^e", Label: "edit"},
	{Key: "^o", Label: "new"},
	{Key: "^f", Label: "dark/light"},
	{Key: "^r", Label: "shuffle", Priority: overlay.HintOptional},
	{Key: "^y", Label: "copy id", Priority: overlay.HintOptional},
	{Key: "^d", Label: "delete"},
}

var themePickerServedHints = []overlay.Hint{
	{Key: "type", Label: "filter"},
	{Key: "↑↓", Label: "move"},
	{Key: overlay.EnterGlyph, Label: "apply"},
	{Key: "esc", Label: "cancel"},
}

// themePickerLayout returns the fitted inner width and visible row count.
func (m *OS) themePickerLayout() (width, rows int, hints []overlay.Hint) {
	width = m.panelWidth(themePickerInnerWidth)
	// Body lines that are not theme rows: the search input, its rule, and the
	// count line, the commands strip, and on a served client the specimen
	// line.
	if m.previewsLive() {
		rows, hints = m.panelBody(themePickerVisibleRows, 4, width, nil, themePickerHints)
	} else {
		rows, hints = m.panelBody(themePickerVisibleRows, 5, width, nil, themePickerServedHints)
	}
	return width, rows, hints
}

// renderThemePicker draws the searchable theme picker with a live color-swatch
// preview per theme, returning the panel, geometry, and per-row hit rects.
func (m *OS) renderThemePicker() (string, overlay.Geometry, []overlayRowHit) {
	items := m.themePickerItems()
	width, visible, hints := m.themePickerLayout()
	title := "Theme"
	if m.ThemePickerShade != themeShadeAll {
		title += " · " + themeShadeNames[m.ThemePickerShade]
	}
	return renderLivePicker(livePickerPanel{
		Title:    title,
		Query:    m.ThemePickerQuery,
		Items:    items,
		Selected: &m.ThemePickerSelected,
		Scroll:   &m.ThemePickerScroll,
		Width:    width,
		Visible:  visible,
		Hints:    hints,
		Empty:    "No matching themes",
		Row:      m.themeRow,
		Footer: func(pal overlay.Palette) []string {
			bg := pal.Surface
			count := overlay.Style(bg).Render(" ")
			if len(items) > visible {
				info := lipgloss.Sprintf("%d of %d themes", m.ThemePickerSelected+1, len(items))
				count = overlay.Style(bg).Foreground(pal.FgMute).Italic(true).Render("  " + info)
			}
			if id := m.ThemePickerDelete; id != "" {
				q := "Delete " + id + "? Press y to delete."
				if _, edited := theme.ThemeFileOf(id); edited {
					q = "Remove your edit of " + id + "? Press y to remove."
				}
				count = overlay.Style(bg).Foreground(pal.Warning).Render("  " + overlay.Truncate(q, max(width-2, 1)))
			}
			if id := m.ThemePickerPair; id != "" {
				q := "Use " + id + " as: d dark, l light, b both"
				count = overlay.Style(bg).Foreground(pal.Accent).Render("  " + overlay.Truncate(q, max(width-2, 1)))
			}
			commands := themePickerCommands
			if !m.previewsLive() || m.ConfigReadOnly {
				// No file can be deleted from here.
				commands = commands[:len(commands)-1]
			}
			strip, _ := overlay.FitHintStrip(commands, width, bg, pal)
			lines := []string{count, strip}
			if m.previewsLive() || m.ThemePickerSelected < 0 || m.ThemePickerSelected >= len(items) {
				return lines
			}
			return append([]string{themeSpecimen(items[m.ThemePickerSelected], width, pal)}, lines...)
		},
	})
}

// themeRow renders one theme entry: a name on the left and a color-swatch
// preview on the right, with a highlight bar when selected.
func (m *OS) themeRow(id string, selected bool, pal overlay.Palette, width int) string {
	st := overlay.RowState{Cursor: selected, Focused: true}
	bg := pal.Ground(st, pal.Surface)
	nameColor := pal.FgDim
	marker := "  "
	if selected {
		nameColor = pal.Fg
		marker = "› "
	}

	swatch := themeSwatchStrip(id, bg)
	swatchW := lipgloss.Width(swatch)
	// On a panel too narrow for a name and a swatch strip, the name wins: the
	// swatch is a preview of something the row already names.
	if swatchW+8 > width {
		swatch, swatchW = "", 0
	}

	label := id
	switch yours, edited := theme.ThemeFileOf(id); {
	case edited:
		label += " (edited)"
	case yours:
		label += " (yours)"
	}
	name := overlay.Truncate(label, max(width-2-swatchW-2, 1))
	left := overlay.Style(bg).Foreground(pal.Accent).Bold(true).Render(marker) +
		overlay.Style(bg).Foreground(nameColor).Bold(selected).Render(name)

	gap := max(width-lipgloss.Width(left)-swatchW, 1)
	// The swatch is a preview of colour, so at 16 colours only the name takes
	// the cursor's reverse video and the swatch keeps what it is showing.
	return pal.Mark(left+overlay.Style(bg).Render(strings.Repeat(" ", gap)), st) + swatch
}

// themeSwatchStrip renders a theme's preview colors as adjacent two-cell blocks.
func themeSwatchStrip(id string, bg color.Color) string {
	colors := theme.ThemeSwatch(id)
	var b strings.Builder
	for _, c := range colors {
		b.WriteString(lipgloss.NewStyle().Background(c).Render("  "))
	}
	// A trailing surface cell separates the strip from the panel edge cleanly.
	return b.String() + overlay.Style(bg).Render(" ")
}

// themeSpecimenText is a short prompt line, each word in the slot a shell
// would draw it in.
var themeSpecimenText = [...]struct {
	text string
	slot int
}{{"$ ", -1}, {"ls ", 12}, {"main ", 10}, {"ok ", 2}, {"warn ", 3}, {"error", 1}}

// themeSpecimen is the preview a served client gets in place of applying the
// highlighted theme: a prompt line in that theme's own colours, on its own
// background. "none" is the terminal's own colours, so it shows those words.
func themeSpecimen(id string, width int, pal overlay.Palette) string {
	slots, fg, bg, _, ok := theme.Colors(id)
	if !ok {
		return overlay.Style(pal.Surface).Foreground(pal.FgDim).
			Render(overlay.Truncate("  Your terminal's own colours", width))
	}
	var b strings.Builder
	b.WriteString(overlay.Style(pal.Surface).Render("  "))
	used := 2
	for _, w := range themeSpecimenText {
		ink := fg
		if w.slot >= 0 {
			ink = slots[w.slot]
		}
		b.WriteString(lipgloss.NewStyle().Background(bg).Foreground(ink).Render(w.text))
		used += len(w.text)
	}
	if used > width {
		return overlay.Style(pal.Surface).Render(strings.Repeat(" ", width))
	}
	return b.String() + overlay.Style(pal.Surface).Render(strings.Repeat(" ", width-used))
}
