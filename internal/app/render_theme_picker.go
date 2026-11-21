package app

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

const (
	themePickerInnerWidth  = 52
	themePickerVisibleRows = 10
)

var themePickerHints = []overlay.Hint{
	{Key: "type", Label: "filter"},
	{Key: hintListMove, Label: "preview"},
	{Key: overlay.EnterGlyph, Label: "apply"},
	{Key: "esc", Label: "cancel"},
}

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
	{Key: hintListMove, Label: "move"},
	{Key: overlay.EnterGlyph, Label: "apply"},
	{Key: "esc", Label: "cancel"},
}

func (m *OS) themePickerLayout() (width, rows int, hints []overlay.Hint) {
	width = m.panelWidth(themePickerInnerWidth)
	if m.previewsLive() {
		rows, hints = m.panelBody(themePickerVisibleRows, 4, width, nil, themePickerHints)
	} else {
		rows, hints = m.panelBody(themePickerVisibleRows, 5, width, nil, themePickerServedHints)
	}
	return width, rows, hints
}

func (m *OS) renderThemePicker() (string, overlay.Geometry, []overlayRowHit) {
	items := m.themePickerItems()
	width, visible, hints := m.themePickerLayout()
	title := "Theme"
	if m.ThemePickerShade != themeShadeAll {
		title += " · " + themeShadeNames[m.ThemePickerShade]
	}
	return m.renderLivePicker(livePickerPanel{
		Title:   title,
		Items:   items,
		List:    &m.themeList,
		Width:   width,
		Visible: visible,
		Hints:   hints,
		Empty:   "No matching themes",
		Row:     m.themeRow,
		Footer: func(pal overlay.Palette) []string {
			bg := pal.Surface
			count := overlay.Style(bg).Render(" ")
			if len(items) > visible {
				info := lipgloss.Sprintf("%d of %d themes", m.themeList.Cursor+1, len(items))
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
				commands = commands[:len(commands)-1]
			}
			strip, _ := overlay.FitHintStrip(commands, width, bg, pal)
			lines := []string{count, strip}
			if m.previewsLive() || m.themeList.Cursor < 0 || m.themeList.Cursor >= len(items) {
				return lines
			}
			return append([]string{themeSpecimen(items[m.themeList.Cursor], width, pal)}, lines...)
		},
	})
}

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
	return pal.Mark(left+overlay.Style(bg).Render(strings.Repeat(" ", gap)), st) + swatch
}

func themeSwatchStrip(id string, bg color.Color) string {
	colors := theme.ThemeSwatch(id)
	var b strings.Builder
	for _, c := range colors {
		b.WriteString(lipgloss.NewStyle().Background(c).Render("  "))
	}
	return b.String() + overlay.Style(bg).Render(" ")
}

var themeSpecimenText = [...]struct {
	text string
	slot int
}{{"$ ", -1}, {"ls ", 12}, {"main ", 10}, {"ok ", 2}, {"warn ", 3}, {"error", 1}}

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
