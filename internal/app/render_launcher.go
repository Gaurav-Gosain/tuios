package app

import (
	"fmt"
	"image/color"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/Gaurav-Gosain/tuios/pkg/applist"
)

const (
	launcherInnerWidth = 64
	launcherMaxVisible = 12

	launcherDetailWidth = 46
)

var launcherHints = []overlay.Hint{
	{Key: hintListMove, Label: "move"},
	{Key: overlay.EnterGlyph, Label: "run"},
	{Key: "⇥", Label: "type it out"},
	{Key: "esc", Label: "close"},
}

func (m *OS) launcherLayout() (width, rows int, hints []overlay.Hint) {
	width = m.panelWidth(launcherInnerWidth)
	rows, hints = m.panelBody(launcherMaxVisible, 2, width, nil, launcherHints)
	return width, rows, hints
}

func (m *OS) launcherIconWidth() int {
	if !m.launcherGraphicsReady() {
		return 0
	}
	return launcherIconCols
}

func (m *OS) renderLauncher() (string, overlay.Geometry, []overlayRowHit) {
	items := m.launcherItems()
	filtered := FilterLauncherItems(items, m.launcherList.Query.String(), m.launchHistory)

	pal := m.overlayUI()
	bg := pal.Surface

	width, visible, hints := m.launcherLayout()
	iconW := m.launcherIconWidth()
	m.launcherList.Fit(len(filtered), visible)

	var lines []string

	search := launcherPicker().searchLine(m, width)
	count := ""
	if len(filtered) > visible {
		count = fmt.Sprintf("%d of %d programs", len(filtered), len(items))
	}
	lines = append(lines, searchWithCount(search, count, width, bg, pal), overlay.Rule(width, bg, pal))

	start, end := 0, 0
	if len(filtered) == 0 {
		if msg := m.launcherEmptyLine(); msg != "" {
			lines = append(lines, overlay.Empty{Message: msg, Hint: overlay.Hint{Key: "esc", Label: "close"}}.Lines(width, visible, bg, pal)...)
		}
		for len(lines) < visible+2 {
			lines = append(lines, overlay.Style(bg).Render(" "))
		}
	} else {
		start = m.launcherList.Scroll
		end = min(start+visible, len(filtered))
		for i := start; i < end; i++ {
			lines = append(lines, launcherRow(filtered[i], i == m.launcherList.Cursor, pal, width, iconW))
		}
		for len(lines) < visible+2 {
			lines = append(lines, overlay.Style(bg).Render(" "))
		}
	}

	panel := overlay.Panel{
		Title: "Run a program",
		Width: width,
		Body:  strings.Join(lines, "\n"),
		Hints: hints,
	}
	content, geo := panel.Render(pal)

	var rows []overlayRowHit
	m.launcherIconCells = m.launcherIconCells[:0]
	for i := start; i < end; i++ {
		rowY := geo.BodyY + (i - start) + 2
		rows = append(rows, overlayRowHit{
			Rect: overlay.Rect{X0: 0, Y0: rowY, X1: geo.Width, Y1: rowY + 1},
			Idx:  i,
		})
		if iconW > 0 {
			m.launcherIconCells = append(m.launcherIconCells, launcherIconPlacement{
				Name: filtered[i].Entry.Icon,
				X:    geo.BodyX + 2,
				Y:    rowY,
			})
		}
	}
	return content, geo, rows
}

// LauncherVisibleIcons is the icon name of every row currently drawn, for the
// command that decodes them. Only the drawn rows: a list of several thousand
// programs would otherwise decode hundreds of pictures nobody is looking at.
func (m *OS) LauncherVisibleIcons() []string {
	if !m.launcherGraphicsReady() {
		return nil
	}
	filtered := m.filteredLauncherItems()
	_, visible, _ := m.launcherLayout()
	start := scrollWindow(m.launcherList.Scroll, m.launcherList.Cursor, len(filtered), visible)
	end := min(start+visible, len(filtered))
	out := make([]string, 0, max(end-start, 0))
	for i := start; i < end; i++ {
		if icon := filtered[i].Entry.Icon; icon != "" {
			out = append(out, icon)
		}
	}
	return out
}

func (m *OS) launcherEmptyLine() string {
	if len(m.LauncherItems) == 0 {
		if !overlay.ShowLoading(m.LauncherOpenedAt, time.Now()) {
			return ""
		}
		return "Scanning for programs" + overlay.Ellipsis()
	}
	return "No program matches"
}

func launcherRow(item LauncherItem, selected bool, pal overlay.Palette, width, iconW int) string {
	st := overlay.RowState{Cursor: selected, Focused: true}
	bg := pal.Ground(st, pal.Surface)
	nameColor := pal.FgDim
	if selected {
		nameColor = pal.Fg
	}

	detail := ""
	detailW := 0
	if width >= launcherDetailWidth {
		if d := launcherDetail(item.Entry); d != "" {
			d = overlay.Truncate(d, width/3)
			detail = overlay.Style(bg).Foreground(pal.FgDim).Render(d)
			detailW = lipgloss.Width(d)
		}
	}

	marker := "  "
	if selected {
		marker = "› "
	}
	icon := overlay.Style(bg).Render(strings.Repeat(" ", iconW))

	name := overlay.Truncate(item.Entry.Label(), max(width-2-iconW-detailW-1, 1))
	left := overlay.Style(bg).Foreground(theme.Readable(pal.Accent, bg)).Bold(true).Render(marker) +
		icon + launcherRowName(name, item.Match, bg, nameColor, selected, pal)

	gap := max(width-lipgloss.Width(left)-detailW, 1)
	return pal.Row(left+overlay.Style(bg).Render(strings.Repeat(" ", gap))+detail, width, st, pal.Surface)
}

func launcherDetail(e applist.Entry) string {
	if e.Detail != "" {
		return e.Detail
	}
	return e.Dir
}

func launcherRowName(name string, match []int, bg, nameColor color.Color, selected bool, pal overlay.Palette) string {
	return paletteRowName(name, "", false, 0, match, bg, nameColor, selected, pal)
}
