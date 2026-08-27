package app

import (
	"image/color"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	tfx "github.com/Gaurav-Gosain/tuiffects"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// Effect picker layout constants, matching the theme and glyph pickers so the
// three panels are one object with three previews.
const (
	// Six cells wider than its two siblings. This is the only one of the three
	// whose detail block is prose: the engine's own sentence about the effect
	// is what answers the names, and at 52 the longest of them needed a third
	// line to land.
	effectPickerInnerWidth  = 58
	effectPickerVisibleRows = 10
	// effectPickerDetailRows is the description box under the list. Two lines
	// hold every description the engine carries at this width, and the box is
	// padded to that height whatever the text needs, so the panel does not
	// change size as the selection moves.
	effectPickerDetailRows = 2
	// effectOpeningColumn is the width of the right-hand column holding the
	// opening time. Five cells hold "45.5s" and everything shorter.
	effectOpeningColumn = 5
)

// effectPickerHints is the footer, shared by the renderer and the sizing helper
// so both measure the same panel.
var effectPickerHints = []overlay.Hint{
	{Key: "type", Label: "filter"},
	{Key: "↑↓", Label: "preview"},
	{Key: "⏎", Label: "apply"},
	{Key: "esc", Label: "cancel"},
}

// effectPickerLayout returns the fitted inner width and visible row count.
func (m *OS) effectPickerLayout() (width, rows int, hints []overlay.Hint) {
	width = m.panelWidth(effectPickerInnerWidth)
	// Body lines that are not effect rows: the search input, its rule, the
	// description box and the line under it saying how long the effect hides
	// the screen.
	rows, hints = m.panelBody(effectPickerVisibleRows, 3+effectPickerDetailRows, width, nil, effectPickerHints)
	return width, rows, hints
}

// renderEffectPicker draws the searchable effect picker, returning the panel,
// geometry, and per-row hit rects. The preview itself is not in here: it is a
// full-screen layer under the panel, placed by renderOverlays.
func (m *OS) renderEffectPicker() (string, overlay.Geometry, []overlayRowHit) {
	items := m.effectPickerItems()
	pal := theme.UI()
	bg := pal.Surface

	if len(items) > 0 {
		m.EffectPickerSelected = clampInt(m.EffectPickerSelected, 0, len(items)-1)
	} else {
		m.EffectPickerSelected = 0
	}
	width, visible, hints := m.effectPickerLayout()
	m.EffectPickerScroll = scrollWindow(m.EffectPickerScroll, m.EffectPickerSelected, len(items), visible)

	var lines []string

	cursor := overlay.Style(bg).Foreground(pal.Accent).Render("█")
	search := overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render("› ") +
		overlay.Style(bg).Foreground(pal.Fg).Render(m.EffectPickerQuery) + cursor
	lines = append(lines, search, overlay.Rule(width, bg, pal))

	start := m.EffectPickerScroll
	end := min(start+visible, len(items))
	shown := 0
	for i := start; i < end; i++ {
		lines = append(lines, m.effectRow(items[i], i == m.EffectPickerSelected, pal, width))
		shown++
	}
	if len(items) == 0 {
		lines = append(lines, overlay.Style(bg).Foreground(pal.FgMute).Italic(true).
			Render("  No matching effects"))
		shown++
	}
	for shown < visible {
		lines = append(lines, overlay.Style(bg).Render(" "))
		shown++
	}

	lines = append(lines, m.effectPickerDetail(items, pal, width)...)

	panel := overlay.Panel{
		Glyph: "󰤄", // sleep
		Title: "Screen saver effect",
		Width: width,
		Body:  strings.Join(lines, "\n"),
		Hints: hints,
	}
	content, geo := panel.Render(pal)

	var rows []overlayRowHit
	for i := start; i < end; i++ {
		rowY := geo.BodyY + (i - start) + 2 // +2 for the search line and its rule
		rows = append(rows, overlayRowHit{
			Rect: overlay.Rect{X0: 0, Y0: rowY, X1: geo.Width, Y1: rowY + 1},
			Idx:  i,
		})
	}
	return content, geo, rows
}

// effectRow renders one effect: its name on the left and how long it takes to
// give the screen back on the right, with a highlight bar when selected.
//
// The time is on the row rather than only in the detail line because it is the
// one property that separates these thirty-six from each other in a way a user
// cares about before choosing. wipe hands the screen back in under a second and
// swarm takes three quarters of a minute, and nothing in either name says so.
func (m *OS) effectRow(name string, selected bool, pal overlay.Palette, width int) string {
	bg := pal.Surface
	nameColor := pal.FgDim
	marker := "  "
	if selected {
		bg = pal.RowSel
		nameColor = pal.Fg
		marker = "› "
	}

	opening, known := effectOpeningSeconds(name)
	timing := ""
	timingColor := pal.FgMute
	if known {
		timing = effectOpeningLabel(opening)
		if effectIsSlow(name) {
			// The flag, not a filter. A slow opener is still a choice someone
			// may want; what it must not be is a surprise.
			timingColor = pal.Warning
		}
	}
	timingW := lipgloss.Width(timing)
	// On a panel too narrow for a name and a time, the name wins.
	if timingW+effectOpeningColumn+8 > width {
		timing, timingW = "", 0
	}

	label := overlay.Truncate(name, max(width-2-timingW-2, 1))
	left := overlay.Style(bg).Foreground(pal.Accent).Bold(true).Render(marker) +
		overlay.Style(bg).Foreground(nameColor).Bold(selected).Render(label)

	right := overlay.Style(bg).Foreground(timingColor).Render(timing)
	gap := max(width-lipgloss.Width(left)-timingW, 1)
	return left + overlay.Style(bg).Render(strings.Repeat(" ", gap)) + right
}

// effectOpeningLabel is a duration in the row's right-hand column. Under ten
// seconds it keeps a decimal, because the difference between 0.9 and 2.0 is the
// difference between two effects and rounding hides it.
func effectOpeningLabel(seconds float64) string {
	if seconds >= 10 {
		return strconv.Itoa(int(seconds+0.5)) + "s"
	}
	return strconv.FormatFloat(seconds, 'f', 1, 64) + "s"
}

// effectOpeningWords is a duration for the sentence under the list. It rounds
// to whole seconds: the decimal earns its place in a column of numbers to
// compare and reads as false precision in a sentence.
func effectOpeningWords(seconds float64) string {
	whole := int(seconds + 0.5)
	if whole < 1 {
		whole = 1
	}
	if whole == 1 {
		return "1 second"
	}
	return strconv.Itoa(whole) + " seconds"
}

// effectPickerDetail is the block under the list: what the selected effect
// does, and how long it takes before the screen is readable again.
//
// The description is the engine's own. It is the answer to the question the
// names raise: nothing about "errorcorrect" or "orbittingvolley" says what
// arrives on screen, and a list of thirty-six such names is barely better than
// the cycler it replaced.
func (m *OS) effectPickerDetail(items []string, pal overlay.Palette, width int) []string {
	bg := pal.Surface
	if m.EffectPickerSelected < 0 || m.EffectPickerSelected >= len(items) {
		return blankLines(bg, effectPickerDetailRows+1)
	}
	name := items[m.EffectPickerSelected]

	description, status := m.effectDetailText(name)
	lines := settingsDescription(description, width, effectPickerDetailRows, pal)

	style := overlay.Style(bg).Foreground(pal.FgMute).Italic(true)
	if m.effectPreview.resized || effectIsSlow(name) {
		// The flag, not a filter. A slow opener is still a choice someone may
		// want; what it must not be is a surprise.
		style = overlay.Style(bg).Foreground(pal.Warning)
	}
	return append(lines, style.Render("  "+overlay.Truncate(status, max(width-2, 1))))
}

// effectDetailText is the description and the status line for one effect.
func (m *OS) effectDetailText(name string) (description, status string) {
	if m.effectPreview.resized {
		// The capture is stale and the animation is gone. Say why, rather than
		// leaving a still frame that looks like an effect that does nothing.
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
	opening, known := effectOpeningSeconds(name)
	switch {
	case !known:
		return description, ""
	case opening < 0.5:
		return description, "The screen stays visible from the start."
	default:
		return description, "The screen comes back after about " + effectOpeningWords(opening) + "."
	}
}

// effectIsSlow reports whether an effect hides the screen long enough to be
// worth flagging.
func effectIsSlow(name string) bool {
	opening, known := effectOpeningSeconds(name)
	return known && opening >= effectSlowOpening
}

// blankLines is n surface-filled spacer rows.
func blankLines(bg color.Color, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = overlay.Style(bg).Render(" ")
	}
	return out
}
