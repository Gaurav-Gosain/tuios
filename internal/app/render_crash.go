package app

import (
	"image/color"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// Drawing the crash overlay.
//
// Every function here is a free function over a *CrashReport and a screen size.
// None of them takes an *OS, and that is not a style preference: this code runs
// after the model has failed, sometimes after the compositor itself has failed,
// so a read of the model here is a read of the thing under suspicion. The
// palette comes from theme.UI(), which is a package function with a full
// fallback ramp and no receiver, and the width comes from overlay.FitWidth,
// which is arithmetic. A nil report and a zero-sized screen both draw something
// rather than panic; see the tests.

// crashPanelWidth is the inner width the overlay would like. It holds a stack
// frame's file:line without wrapping on any screen wide enough to have shown
// tuios in the first place, and FitWidth gives back what a narrower one can.
const crashPanelWidth = 76

// crashHints is the footer. Three keys, in the order a user needs them: read
// it, send it, leave.
var crashHints = []overlay.Hint{
	{Key: "c", Label: "copy report"},
	{Key: "g", Label: "open an issue"},
	{Key: "esc", Label: "go back"},
}

// crashLead is what the overlay says before any of the detail.
//
// It has one job, and it is not to explain the bug. Someone reading this has
// just had a program stop doing what they asked, and the first thing they need
// to know is whether they have lost anything. They have not: the panes are
// processes, the daemon holds the session, and a recovered panic drops one
// event or one frame. So the answer comes first, the offer to report comes
// second, and there is no apology, because an apology is a line of text between
// the user and the answer.
var crashLead = []string{
	"tuios reached a state it does not expect.",
	"Your panes and your session are still running. Press esc to go back to them.",
	"Send the report and this gets fixed.",
}

// RenderCrashScreen draws the whole screen for a crash report.
//
// It returns a block width by height cells, ready to be handed straight to
// tea.View, because that is where it goes: View returns this instead of calling
// composeFrame, so nothing in the normal render path runs. A caller with no
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

// crashPanel builds the panel itself, sized to the screen it has to fit in.
func crashPanel(report *CrashReport, notice string, width, height int, pal overlay.Palette) string {
	inner := overlay.FitWidth(crashPanelWidth, width)
	if inner < overlay.MinPanelWidth {
		inner = overlay.MinPanelWidth
	}

	// The panel's own furniture: a top pad, the title chip, a blank, a bottom
	// pad, and, because there are hints, a blank plus a rule plus the strip.
	// Budgeting here rather than letting the body overflow is what keeps the
	// footer on screen, and the footer is where the two keys that matter are.
	overhead := 4 + 2 + overlay.HintRowCount(crashHints, inner)
	body := max(height-overhead, 1)

	p := overlay.Panel{
		Glyph: "",
		Title: "tuios hit a bug",
		Width: inner,
		Body:  strings.Join(crashBody(report, notice, inner, body, pal), "\n"),
		Hints: crashHints,
	}
	content, _ := p.Render(pal)
	return content
}

// crashBody lays out the lead, the facts and as much of the trace as rows
// allows.
//
// The order is a priority order, and the trace is last on purpose. The lead
// tells the user they have lost nothing, the facts are what a maintainer needs
// to place the report, and the trace is the part that is also on the clipboard,
// in the crash log and in the issue. So a short screen loses trace lines, then
// facts, and never loses the lead.
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
		// Not a state the program can reach through NoteCrash, but the renderer
		// is a public function over a pointer and the honest answer to a nil one
		// is a panel that says so rather than one that panics drawing it.
		add(warn.Render("No report was captured."))
		return padCrashBody(lines, rows, bg)
	}

	// The panic value, which is the line that tells two crashes apart.
	for _, w := range wrapPlain(report.Panic, width) {
		add(warn.Render(overlay.Truncate(w, width)))
	}
	add(overlay.Style(bg).Render(" "))

	// The facts, as a two-column block. The label column is sized to the widest
	// label so the values line up, which is what makes a block of a dozen rows
	// scannable rather than a wall.
	//
	// A short screen shows fewer of them and says how many it left out. It never
	// silently stops: a block that ends at "Layout" on a 24-row terminal and at
	// "System" on a 40-row one reads as a report that has different contents on
	// different screens, and the one thing a user must be able to trust here is
	// that pressing c sends the whole of it. The facts are the first thing cut
	// because they are the part the maintainer reads from the issue rather than
	// from a photograph of somebody's terminal.
	labelW := 0
	for _, f := range report.Facts {
		labelW = max(labelW, lipgloss.Width(f.Label))
	}
	labelW = min(labelW, max(width/3, 1))
	// One row is held back for the notice when there is one, so pressing c on a
	// short screen does not push the confirmation off the bottom.
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

	// Whatever room is left goes to the trace. Two rows of it says nothing, so
	// below that the block is dropped for the line that says where to find it.
	remaining := rows - len(lines)
	if remaining > 4 {
		add(overlay.Style(bg).Render(" "))
		add(overlay.Rule(width, bg, pal))
		trace, dropped := clipStack(report.Stack, rows-len(lines)-1)
		for _, t := range strings.Split(trace, "\n") {
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

// padCrashBody fills a body out to exactly rows lines, so the panel is the height it was
// budgeted for and sits where it was placed.
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
