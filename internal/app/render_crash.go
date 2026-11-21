package app

import (
	"image/color"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

const crashPanelWidth = 76

var crashHints = []overlay.Hint{
	{Key: "c", Label: "copy report"},
	{Key: "g", Label: "open an issue"},
	{Key: "esc", Label: "go back"},
}

var crashLead = []string{
	"tuios reached a state it does not expect.",
	"Your panes and your session are still running. Press esc to go back to them.",
	"Send the report and this gets fixed.",
}

// RenderCrashScreen draws the whole screen for a crash report.
//
// It returns a block width by height cells, ready to be handed straight to
// tea.View, because that is where it goes: View returns this instead of calling
// composeView, so nothing in the normal render path runs. A caller with no
// size yet gets a small block rather than nothing.
func RenderCrashScreen(report *CrashReport, notice string, width, height int) string {
	pal := theme.UI()
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	panel := crashPanel(report, notice, width, height, pal)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, panel,
		lipgloss.WithWhitespaceStyle(lipgloss.NewStyle().Background(pal.Canvas)))
}

func crashPanel(report *CrashReport, notice string, width, height int, pal overlay.Palette) string {
	inner := max(overlay.FitWidth(crashPanelWidth, width), overlay.MinPanelWidth)

	overhead := 4 + 2 + overlay.HintRowCount(crashHints, inner)
	body := max(height-overhead, 1)

	p := overlay.Panel{
		Title: "tuios hit a bug",
		Width: inner,
		Body:  strings.Join(crashBody(report, notice, inner, body, pal), "\n"),
		Hints: crashHints,
	}
	content, _ := p.Render(pal)
	return content
}

func crashBody(report *CrashReport, notice string, width, rows int, pal overlay.Palette) []string {
	bg := pal.Surface
	dim := overlay.Style(bg).Foreground(pal.FgDim)
	mute := overlay.Style(bg).Foreground(pal.FgMute)
	label := overlay.Style(bg).Foreground(pal.FgMute)
	value := overlay.Style(bg).Foreground(pal.Fg)
	warn := overlay.Style(bg).Foreground(pal.Warn).Bold(true)
	ok := overlay.Style(bg).Foreground(pal.Success)

	var lines []string
	add := func(s string) {
		if len(lines) < rows {
			lines = append(lines, s)
		}
	}

	for _, s := range crashLead {
		for _, w := range wrapPlain(s, width) {
			add(dim.Render(w))
		}
	}
	add(overlay.Style(bg).Render(" "))

	if report == nil {
		add(warn.Render("No report was captured."))
		return padCrashBody(lines, rows, bg)
	}

	for _, w := range wrapPlain(report.Panic, width) {
		add(warn.Render(overlay.Truncate(w, width)))
	}
	add(overlay.Style(bg).Render(" "))

	labelW := 0
	for _, f := range report.Facts {
		labelW = max(labelW, lipgloss.Width(f.Label))
	}
	labelW = min(labelW, max(width/3, 1))
	room := rows - len(lines)
	if notice != "" {
		room--
	}
	shown := report.Facts
	if len(shown) > room {
		shown = shown[:max(room-1, 0)]
	}
	for _, f := range shown {
		l := overlay.Truncate(f.Label, labelW)
		l += strings.Repeat(" ", max(labelW-lipgloss.Width(l), 0))
		v := overlay.Truncate(f.Value, max(width-labelW-2, 1))
		add(label.Render(l+"  ") + value.Render(v))
	}
	if dropped := len(report.Facts) - len(shown); dropped > 0 {
		add(mute.Render(overlay.Truncate(
			strconv.Itoa(dropped)+" more details. Press c to copy the whole report.", width)))
	}

	if notice != "" {
		add(ok.Render(overlay.Truncate(notice, width)))
	}

	remaining := rows - len(lines)
	if remaining > 4 {
		add(overlay.Style(bg).Render(" "))
		add(overlay.Rule(width, bg, pal))
		trace, dropped := clipStack(report.Stack, rows-len(lines)-1)
		for t := range strings.SplitSeq(trace, "\n") {
			add(mute.Render(overlay.Truncate(strings.ReplaceAll(t, "\t", "  "), width)))
		}
		if dropped > 0 {
			add(mute.Render(overlay.Truncate(
				strconv.Itoa(dropped)+" more lines. Press c to copy the whole report.", width)))
		}
	} else if report.LogPath != "" {
		add(mute.Render(overlay.Truncate("Crash log: "+report.LogPath, width)))
	}

	return padCrashBody(lines, rows, bg)
}

func padCrashBody(lines []string, rows int, bg color.Color) []string {
	blank := overlay.Style(bg).Render(" ")
	for len(lines) < rows {
		lines = append(lines, blank)
	}
	if len(lines) > rows {
		lines = lines[:rows]
	}
	return lines
}
