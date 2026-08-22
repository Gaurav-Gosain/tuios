package app

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/Gaurav-Gosain/tuios/pkg/applist"
)

// Launcher layout constants. These are the preferred sizes; a narrower or
// shorter screen gets a launcher fitted to it (see overlay_fit.go).
const (
	launcherInnerWidth = 64
	launcherMaxVisible = 12

	// launcherDetailWidth is the narrowest inner width that still shows a row's
	// right-hand detail (the directory the program was found in). Below it the
	// detail is dropped so the name keeps the room, which is the same trade the
	// palette makes with its category tag.
	launcherDetailWidth = 46
)

// launcherHints is the launcher footer, shared by the renderer and the sizing
// helper so both measure the same panel.
//
// Run and type sit side by side because they are one choice made per launch.
// Naming both in the footer is the only place the second one can be
// discovered, and it is the reason it does not need to be a setting.
var launcherHints = []overlay.Hint{
	{Key: "↑↓", Label: "move"},
	{Key: "⏎", Label: "run"},
	{Key: "⇥", Label: "type it out"},
	{Key: "esc", Label: "close"},
}

// launcherLayout returns the launcher's fitted inner width and visible row
// count. The keyboard navigation uses the same numbers as the renderer so the
// selection cannot scroll out of the rows actually drawn.
func (m *OS) launcherLayout() (width, rows int, hints []overlay.Hint) {
	width = m.panelWidth(launcherInnerWidth)
	// Body lines that are not program rows: the search input, its rule, and the
	// match count.
	rows, hints = m.panelBody(launcherMaxVisible, 3, width, nil, launcherHints)
	return width, rows, hints
}

// renderLauncher draws the launcher on the shared panel grammar: a search
// input, a scrolling list of matching programs, and a highlight bar on the
// selection.
func (m *OS) renderLauncher() (string, overlay.Geometry, []overlayRowHit) {
	items := m.launcherItems()
	filtered := FilterLauncherItems(items, m.LauncherQuery, m.launchHistory)

	pal := theme.UI()
	bg := pal.Surface

	width, visible, hints := m.launcherLayout()
	m.LauncherScroll = scrollWindow(m.LauncherScroll, m.LauncherSelected, len(filtered), visible)

	var lines []string

	// The accent is the theme's and the panel's ground is not, so both are
	// measured against it. The cursor is a block, so the mark floor is enough.
	cursor := overlay.Style(bg).Foreground(theme.ReadableAt(pal.Accent, bg, theme.MarkFloor)).Render("█")
	search := overlay.Style(bg).Foreground(theme.Readable(pal.AccentBright, bg)).Bold(true).Render("› ") +
		overlay.Style(bg).Foreground(pal.Fg).Render(m.LauncherQuery) + cursor
	lines = append(lines, search, overlay.Rule(width, bg, pal))

	if len(filtered) == 0 {
		lines = append(lines, overlay.Style(bg).Foreground(pal.FgDim).Italic(true).Render(m.launcherEmptyLine()))
		for len(lines) < visible+3 {
			lines = append(lines, overlay.Style(bg).Render(" "))
		}
	} else {
		start := m.LauncherScroll
		end := min(start+visible, len(filtered))
		for i := start; i < end; i++ {
			lines = append(lines, launcherRow(filtered[i], i == m.LauncherSelected, pal, width))
		}
		for len(lines) < visible+2 {
			lines = append(lines, overlay.Style(bg).Render(" "))
		}
		if len(filtered) > visible {
			info := fmt.Sprintf("%d of %d programs", len(filtered), len(items))
			lines = append(lines, overlay.Style(bg).Foreground(pal.FgDim).Italic(true).Render("  "+info))
		} else {
			lines = append(lines, overlay.Style(bg).Render(" "))
		}
	}

	panel := overlay.Panel{
		Glyph: "", // rocket
		Title: "Run a Program",
		Width: width,
		Body:  strings.Join(lines, "\n"),
		Hints: hints,
	}
	content, geo := panel.Render(pal)

	var rows []overlayRowHit
	if len(filtered) > 0 {
		start := m.LauncherScroll
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

// launcherEmptyLine says why the list is empty, which is two different things.
// Before the first scan lands there is nothing to match against yet, and saying
// "no programs match" then is simply wrong.
func (m *OS) launcherEmptyLine() string {
	if len(m.LauncherItems) == 0 {
		return "  Scanning $PATH…"
	}
	return "  No program matches"
}

// launcherRow renders one program row: the name, and the directory it was found
// in, with a full-width highlight bar when selected.
func launcherRow(item LauncherItem, selected bool, pal overlay.Palette, width int) string {
	bg := pal.Surface
	nameColor := pal.FgDim
	if selected {
		bg = pal.RowSel
		nameColor = pal.Fg
	}

	detail := ""
	detailW := 0
	if width >= launcherDetailWidth {
		if d := launcherDetail(item.Entry); d != "" {
			detail = overlay.Style(bg).Foreground(pal.FgDim).Render(d)
			detailW = lipgloss.Width(d)
		}
	}

	marker := "  "
	if selected {
		marker = "› "
	}
	name := overlay.Truncate(item.Entry.Name, max(width-2-detailW-1, 1))
	left := overlay.Style(bg).Foreground(theme.Readable(pal.Accent, bg)).Bold(true).Render(marker) +
		launcherRowName(name, item.Match, bg, nameColor, selected, pal)

	gap := max(width-lipgloss.Width(left)-detailW, 1)
	return left + overlay.Style(bg).Render(strings.Repeat(" ", gap)) + detail
}

// launcherDetail is a row's right-hand meta slot: the directory the program was
// found in, which is the only thing that tells a shadowed name apart from the
// one that won.
func launcherDetail(e applist.Entry) string {
	return e.Dir
}

// launcherRowName renders a name with the characters the query matched in the
// accent. It is the palette's own name renderer with no agent-state glyph to
// splice in, kept shared so the two lists highlight a match identically.
func launcherRowName(name string, match []int, bg, nameColor color.Color, selected bool, pal overlay.Palette) string {
	return paletteRowName(name, "", match, bg, nameColor, selected, pal)
}
