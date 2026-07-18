package app

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// Command palette layout constants.
const (
	paletteInnerWidth = 64
	paletteMaxVisible = 10
)

// renderCommandPalette draws the command palette on the shared panel grammar: a
// search input, a scrolling list of matching commands with category tags and
// shortcuts, and a highlight bar on the selection.
func (m *OS) renderCommandPalette() (string, overlay.Geometry, []overlayRowHit) {
	items := GetCommandPaletteItems()
	filtered := FilterCommandPalette(items, m.CommandPaletteQuery)

	pal := theme.UI()
	bg := pal.Surface

	var lines []string

	// Search input.
	cursor := overlay.Style(bg).Foreground(pal.Accent).Render("█")
	search := overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render("› ") +
		overlay.Style(bg).Foreground(pal.Fg).Render(m.CommandPaletteQuery) + cursor
	lines = append(lines, search, overlay.Rule(paletteInnerWidth, bg, pal))

	if len(filtered) == 0 {
		lines = append(lines, overlay.Style(bg).Foreground(pal.FgMute).Italic(true).Render("  No matching commands"))
		for len(lines) < paletteMaxVisible+3 {
			lines = append(lines, overlay.Style(bg).Render(" "))
		}
	} else {
		start := m.CommandPaletteScroll
		end := min(start+paletteMaxVisible, len(filtered))
		for i := start; i < end; i++ {
			lines = append(lines, paletteRow(filtered[i], i == m.CommandPaletteSelected, pal))
		}
		for len(lines) < paletteMaxVisible+2 {
			lines = append(lines, overlay.Style(bg).Render(" "))
		}
		if len(filtered) > paletteMaxVisible {
			info := fmt.Sprintf("%d of %d commands", len(filtered), len(items))
			lines = append(lines, overlay.Style(bg).Foreground(pal.FgMute).Italic(true).Render("  "+info))
		} else {
			lines = append(lines, overlay.Style(bg).Render(" "))
		}
	}

	panel := overlay.Panel{
		Glyph: "", // command
		Title: "Command Palette",
		Width: paletteInnerWidth,
		Body:  strings.Join(lines, "\n"),
		Hints: []overlay.Hint{
			{Key: "↑↓", Label: "move"},
			{Key: "⏎", Label: "run"},
			{Key: "esc", Label: "close"},
		},
	}
	content, geo := panel.Render(pal)

	// Build hit rows for the visible entries.
	var rows []overlayRowHit
	if len(filtered) > 0 {
		start := m.CommandPaletteScroll
		end := min(start+paletteMaxVisible, len(filtered))
		for i := start; i < end; i++ {
			rowY := geo.BodyY + (i - start) + 2 // +2 for the search line and rule
			rows = append(rows, overlayRowHit{
				Rect: overlay.Rect{X0: 0, Y0: rowY, X1: geo.Width, Y1: rowY + 1},
				Idx:  i,
			})
		}
	}
	return content, geo, rows
}

// paletteRow renders one command row: a category tag, the name, and the
// shortcut, with a full-width highlight bar when selected.
func paletteRow(item CommandPaletteItem, selected bool, pal overlay.Palette) string {
	bg := pal.Surface
	nameColor := pal.FgDim
	if selected {
		bg = pal.RowSel
		nameColor = pal.Fg
	}

	catTag := overlay.Style(bg).Foreground(pal.FgMute).Render("[" + item.Category + "]")
	shortcut := ""
	if item.Shortcut != "" {
		shortcut = overlay.Style(bg).Foreground(pal.FgMute).Render(item.Shortcut)
	}

	marker := "  "
	if selected {
		marker = "› "
	}
	left := overlay.Style(bg).Foreground(pal.Accent).Bold(true).Render(marker) +
		catTag + overlay.Style(bg).Render(" ") +
		overlay.Style(bg).Foreground(nameColor).Bold(selected).Render(item.Name)

	gap := max(paletteInnerWidth-lipgloss.Width(left)-lipgloss.Width(shortcut), 1)
	return left + overlay.Style(bg).Render(strings.Repeat(" ", gap)) + shortcut
}
