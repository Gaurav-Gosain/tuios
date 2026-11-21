package app

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	tfx "github.com/Gaurav-Gosain/tuiffects"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

const (
	effectPickerInnerWidth  = 58
	effectPickerVisibleRows = 10
	effectPickerDetailRows  = 2
	effectOpeningColumn     = 6
)

var effectPickerHints = []overlay.Hint{
	{Key: "type", Label: "filter"},
	{Key: hintListMove, Label: "preview"},
	{Key: overlay.EnterGlyph, Label: "apply"},
	{Key: "esc", Label: "cancel"},
}

func (m *OS) effectPickerLayout() (width, rows int, hints []overlay.Hint) {
	width = m.panelWidth(effectPickerInnerWidth)
	rows, hints = m.panelBody(effectPickerVisibleRows, 3+effectPickerDetailRows, width, nil, effectPickerHints)
	return width, rows, hints
}

func (m *OS) renderEffectPicker() (string, overlay.Geometry, []overlayRowHit) {
	items := m.effectPickerItems()
	pal := m.overlayUI()
	bg := pal.Surface

	width, visible, hints := m.effectPickerLayout()
	m.effectList.Fit(len(items), visible)

	var lines []string
	lines = append(lines, effectPicker().searchLine(m, width), overlay.Rule(width, bg, pal))

	start := m.effectList.Scroll
	end := min(start+visible, len(items))
	shown := 0
	for i := start; i < end; i++ {
		lines = append(lines, m.effectRow(items[i], i == m.effectList.Cursor, pal, width))
		shown++
	}
	if len(items) == 0 {
		lines = append(lines, overlay.Empty{Message: "No matching effects", Hint: overlay.Hint{Key: "esc", Label: "close"}}.Lines(width, visible, bg, pal)...)
		shown = visible
	}
	for shown < visible {
		lines = append(lines, overlay.Style(bg).Render(" "))
		shown++
	}

	lines = append(lines, m.effectPickerDetail(items, pal, width)...)

	panel := overlay.Panel{
		Title: "Screen saver effect",
		Width: width,
		Body:  strings.Join(lines, "\n"),
		Hints: hints,
	}
	content, geo := panel.Render(pal)

	var rows []overlayRowHit
	for i := start; i < end; i++ {
		rowY := geo.BodyY + (i - start) + 2
		rows = append(rows, overlayRowHit{
			Rect: overlay.Rect{X0: 0, Y0: rowY, X1: geo.Width, Y1: rowY + 1},
			Idx:  i,
		})
	}
	return content, geo, rows
}

func (m *OS) effectRow(name string, selected bool, pal overlay.Palette, width int) string {
	st := overlay.RowState{Cursor: selected, Focused: true}
	bg := pal.Ground(st, pal.Surface)
	nameColor := pal.FgDim
	marker := "  "
	if selected {
		nameColor = pal.Fg
		marker = "› "
	}

	band := effectOpeningBandOf(name, &m.Settings)
	timing := effectOpeningWord(band)
	timingColor := pal.FgMute
	if band == effectOpeningLong {
		timingColor = pal.Warning
	}
	timingW := lipgloss.Width(timing)
	if timingW+effectOpeningColumn+8 > width {
		timing, timingW = "", 0
	}

	label := overlay.Truncate(name, max(width-2-timingW-2, 1))
	left := overlay.Style(bg).Foreground(pal.Accent).Bold(true).Render(marker) +
		overlay.Style(bg).Foreground(nameColor).Bold(selected).Render(label)

	right := overlay.Style(bg).Foreground(timingColor).Render(timing)
	gap := max(width-lipgloss.Width(left)-timingW, 1)
	return pal.Row(left+overlay.Style(bg).Render(strings.Repeat(" ", gap))+right, width, st, pal.Surface)
}

func effectOpeningWord(band effectOpeningBand) string {
	switch band {
	case effectOpeningNone:
		return "none"
	case effectOpeningShort:
		return "short"
	case effectOpeningMedium:
		return "medium"
	case effectOpeningLong:
		return "long"
	default:
		return ""
	}
}

func effectOpeningSentence(band effectOpeningBand) string {
	switch band {
	case effectOpeningNone:
		return "The screen stays visible from the start."
	case effectOpeningShort:
		return "The wait is short. The time depends on your screen."
	case effectOpeningMedium:
		return "The wait is medium. The time depends on your screen."
	case effectOpeningLong:
		return "The wait is long. The time depends on your screen."
	default:
		return ""
	}
}

func (m *OS) effectPickerDetail(items []string, pal overlay.Palette, width int) []string {
	bg := pal.Surface
	if m.effectList.Cursor < 0 || m.effectList.Cursor >= len(items) {
		return blankLines(bg, effectPickerDetailRows+1)
	}
	name := items[m.effectList.Cursor]

	description, status := m.effectDetailText(name)
	lines := settingsDescription(description, width, effectPickerDetailRows, pal)

	style := overlay.Style(bg).Foreground(pal.FgMute).Italic(true)
	if m.effectPreview.resized || effectOpeningBandOf(name, &m.Settings) == effectOpeningLong {
		style = overlay.Style(bg).Foreground(pal.Warning)
	}
	return append(lines, style.Render("  "+overlay.Truncate(status, max(width-2, 1))))
}

func (m *OS) effectDetailText(name string) (description, status string) {
	if m.effectPreview.resized {
		status = "The screen size changed. The preview stopped."
	}

	if name == config.ScreensaverRandomEffect {
		description = "A different effect runs each time."
		if status == "" {
			status = "tuios picks the effect."
			if running := m.effectPreview.running; running != "" {
				status = "The preview shows " + running + "."
			}
		}
		return description, status
	}

	if d, ok := tfx.Lookup(name); ok {
		description = d.Description
	}
	if status != "" {
		return description, status
	}
	return description, effectOpeningSentence(effectOpeningBandOf(name, &m.Settings))
}

func blankLines(bg color.Color, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = overlay.Style(bg).Render(" ")
	}
	return out
}
