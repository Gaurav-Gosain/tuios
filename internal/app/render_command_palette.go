package app

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// Command palette layout constants. These are the preferred sizes; a narrower
// or shorter screen gets a palette fitted to it (see overlay_fit.go).
const (
	paletteInnerWidth = 64
	paletteMaxVisible = 10

	// paletteCategoryWidth is the narrowest inner width that still shows the
	// [category] tag in front of a command name. Below it the tag is dropped so
	// the name and its shortcut keep the room.
	paletteCategoryWidth = 46
)

// paletteHints is the palette footer, shared by the renderer and the sizing
// helper so both measure the same panel.
var paletteHints = []overlay.Hint{
	{Key: "↑↓", Label: "move"},
	{Key: "⏎", Label: "run"},
	{Key: "esc", Label: "close"},
}

// paletteLayout returns the palette's fitted inner width and visible row count.
// The keyboard navigation uses the same numbers as the renderer so the
// selection cannot scroll out of the rows actually drawn.
func (m *OS) paletteLayout() (width, rows int, hints []overlay.Hint) {
	width = m.panelWidth(paletteInnerWidth)
	// Body lines that are not command rows: the search input, its rule, and the
	// match count.
	rows, hints = m.panelBody(paletteMaxVisible, 3, width, nil, paletteHints)
	return width, rows, hints
}

// renderCommandPalette draws the command palette on the shared panel grammar: a
// search input, a scrolling list of matching commands with category tags and
// shortcuts, and a highlight bar on the selection.
func (m *OS) renderCommandPalette() (string, overlay.Geometry, []overlayRowHit) {
	items := m.allPaletteItems()
	filtered := FilterCommandPalette(items, m.CommandPaletteQuery)

	pal := theme.UI()
	bg := pal.Surface

	width, visible, hints := m.paletteLayout()
	m.CommandPaletteScroll = scrollWindow(m.CommandPaletteScroll, m.CommandPaletteSelected, len(filtered), visible)

	var lines []string

	// Search input.
	cursor := overlay.Style(bg).Foreground(pal.Accent).Render("█")
	search := overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render("› ") +
		overlay.Style(bg).Foreground(pal.Fg).Render(m.CommandPaletteQuery) + cursor
	lines = append(lines, search, overlay.Rule(width, bg, pal))

	if len(filtered) == 0 {
		lines = append(lines, overlay.Style(bg).Foreground(pal.FgMute).Italic(true).Render("  No matching commands"))
		for len(lines) < visible+3 {
			lines = append(lines, overlay.Style(bg).Render(" "))
		}
	} else {
		start := m.CommandPaletteScroll
		end := min(start+visible, len(filtered))
		for i := start; i < end; i++ {
			lines = append(lines, paletteRow(filtered[i], i == m.CommandPaletteSelected, pal, width))
		}
		for len(lines) < visible+2 {
			lines = append(lines, overlay.Style(bg).Render(" "))
		}
		if len(filtered) > visible {
			info := fmt.Sprintf("%d of %d commands", len(filtered), len(items))
			lines = append(lines, overlay.Style(bg).Foreground(pal.FgMute).Italic(true).Render("  "+info))
		} else {
			lines = append(lines, overlay.Style(bg).Render(" "))
		}
	}

	panel := overlay.Panel{
		Glyph: "", // command
		Title: "Command Palette",
		Width: width,
		Body:  strings.Join(lines, "\n"),
		Hints: hints,
	}
	content, geo := panel.Render(pal)

	// Build hit rows for the visible entries.
	var rows []overlayRowHit
	if len(filtered) > 0 {
		start := m.CommandPaletteScroll
		end := min(start+visible, len(filtered))
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
func paletteRow(item CommandPaletteItem, selected bool, pal overlay.Palette, width int) string {
	bg := pal.Surface
	nameColor := pal.FgDim
	if selected {
		bg = pal.RowSel
		nameColor = pal.Fg
	}

	// On a narrow panel the category tag is the first thing to go: it is the
	// least useful part of the row and it costs the most.
	tag := ""
	tagW := 0
	if width >= paletteCategoryWidth {
		tag = overlay.Style(bg).Foreground(pal.FgMute).Render("["+item.Category+"]") +
			overlay.Style(bg).Render(" ")
		tagW = lipgloss.Width(item.Category) + 3
	}

	shortcut := ""
	shortcutW := 0
	if item.Shortcut != "" {
		shortcut = overlay.Style(bg).Foreground(pal.FgMute).Render(item.Shortcut)
		shortcutW = lipgloss.Width(item.Shortcut)
	}

	marker := "  "
	if selected {
		marker = "› "
	}
	name := overlay.Truncate(item.Name, max(width-2-tagW-shortcutW-1, 1))
	left := overlay.Style(bg).Foreground(pal.Accent).Bold(true).Render(marker) +
		tag + overlay.Style(bg).Foreground(nameColor).Bold(selected).Render(name)

	gap := max(width-lipgloss.Width(left)-shortcutW, 1)
	return left + overlay.Style(bg).Render(strings.Repeat(" ", gap)) + shortcut
}
