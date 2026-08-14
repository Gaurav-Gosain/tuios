package vis

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// The action tape. One cell is one action, laid out as a ring with a visible
// write head, so the sweep on screen is literally the generator's rate. This is
// the "machine hammering it" shot and it is not a metaphor: when the head moves
// an action executed, and when it stops the engine stopped.

// recent is how many trailing actions are drawn at the brighter tier. Two tiers
// is all the recency the tape claims, because a smooth decay ramp would imply a
// precision about age that the display is not measuring.
const recent = tapeCols

// renderTape draws the ring into h rows of w columns.
func renderTape(s Snapshot, pal overlay.Palette, w, h int) []string {
	bg := pal.Canvas
	g := glyphs()
	var out []string
	pad := ink(bg, bg).Render("  ")
	label := ink(bg, pal.FgMute)

	counter := "waiting"
	if s.Actions > 0 {
		counter = commas(s.Actions) + " " + g.sep + " " + rate(s.Rate, s.Elapsed, " actions/s")
	}
	out = append(out, row(w, bg, pad, label.Render("actions "), ink(bg, pal.FgDim).Render(counter)))

	cols := min(w-4, tapeCols)
	rows := min(h-1, tapeRows)
	if cols < 8 || rows < 1 {
		return fit(out, w, h, bg)
	}

	// The window shown is the newest rows*cols cells of the ring, so a short
	// frame drops the oldest rows rather than silently rescaling the tape.
	shown := rows * cols
	dim := ink(bg, pal.FgDim)
	mute := ink(bg, pal.FgMute)
	// The head swaps ink and ground. Reverse video is an attribute rather than a
	// colour, so it is the one mark on the tape that survives monochrome intact.
	head := ink(pal.Fg, pal.Canvas)
	alarm := bold(pal.Warn, pal.Canvas)

	for r := range rows {
		var b strings.Builder
		for c := range cols {
			// Oldest first, so the head lands where the newest action is.
			age := shown - (r*cols + c) - 1
			idx := ((s.Head-1-age)%tapeCap + tapeCap) % tapeCap
			v := s.Tape[idx]
			if v == 0 {
				b.WriteString(mute.Render(" "))
				continue
			}
			glyph := s.Classes[min(int(v)-1, len(s.Classes)-1)].Letter
			switch {
			case idx == s.ViolationCell:
				// The exact gesture that broke it, still in the stream.
				b.WriteString(alarm.Render(glyph))
			case age == 0 && !s.Violated:
				b.WriteString(head.Render(glyph))
			case age < recent:
				b.WriteString(dim.Render(glyph))
			default:
				b.WriteString(mute.Render(glyph))
			}
		}
		out = append(out, row(w, bg, pad, b.String()))
	}
	return fit(out, w, h, bg)
}

// renderFunnel is the minimisation, which is the best thing in the run to
// watch: the engine bisecting its own history down to the shortest sequence
// that still breaks the same rule. It takes the tape's rows because generation
// has stopped, and a tape that kept sweeping would be claiming otherwise.
func renderFunnel(s Snapshot, pal overlay.Palette, w, h int) []string {
	bg := pal.Canvas
	g := glyphs()
	var out []string
	pad := ink(bg, bg).Render("  ")
	label := ink(bg, pal.FgMute)
	value := ink(bg, pal.FgDim)

	head := []string{
		label.Render("shrink "),
		value.Render(commas(s.InitialLen)),
		label.Render(" " + g.arrow + " "),
		bold(bg, pal.Fg).Render(commas(s.BestLen)),
		label.Render("  " + g.sep + "  "),
		value.Render(commas(s.RunsSeen)),
		label.Render(" candidates  " + g.sep + "  "),
		ink(bg, pal.Warn).Render(g.fail),
		label.Render(" still fails  "),
		ink(bg, pal.FgMute).Render(g.hold),
		label.Render(" holds"),
	}
	out = append(out, row(w, bg, append([]string{pad}, head...)...))

	const figure = 6
	bars := max(w-figure-18, 8)
	drawn := 0
	for i, a := range s.Runs {
		if drawn >= h-1 {
			break
		}
		// The elision row sits exactly where the dropped candidates were, and
		// says how many they were. Dropping them silently would make the funnel
		// look shorter than the work that produced it.
		if s.Elided > 0 && i == funnelHead {
			out = append(out, row(w, bg, pad, ink(bg, pal.FgMute).Render(
				overlay.Truncate(g.sep+" "+commas(s.Elided)+" more candidates, each kept only if it still failed", w-4))))
			drawn++
			if drawn >= h-1 {
				break
			}
		}
		out = append(out, row(w, bg, pad, funnelRow(a, s, pal, figure, bars, i == len(s.Runs)-1)))
		drawn++
	}
	return fit(out, w, h, bg)
}

// funnelRow is one candidate at its real length. The bar is linear against the
// sequence minimisation started from and it is never rescaled, because the
// collapse to a sliver is the story: a bar that stretched back to full width
// would hide the very thing worth watching.
func funnelRow(a Attempt, s Snapshot, pal overlay.Palette, figure, cells int, newest bool) string {
	bg := pal.Canvas
	g := glyphs()

	size := commas(a.Size)
	fig := strings.Repeat(" ", max(figure-lipgloss.Width(size), 0)) + size

	full, part := 0, 0
	if s.InitialLen > 0 {
		eighths := a.Size * cells * 8 / s.InitialLen
		full, part = eighths/8, eighths%8
	}
	full = min(full, cells)
	bar := strings.Repeat(g.block, full)
	if part > 0 && full < cells {
		bar += g.eighths[part]
	}

	barInk := ink(bg, pal.FgMute)
	if a.Accepted {
		barInk = ink(bg, pal.FgDim)
	}
	if newest {
		// The candidate the engine tested most recently. It is marked because
		// it is where the funnel is now, and it is a fact rather than an
		// animation: the engine reports a candidate once it has a verdict.
		barInk = ink(bg, pal.Warning)
	}

	mark, markInk := g.hold, ink(bg, pal.FgMute)
	if a.Accepted {
		mark, markInk = g.fail, bold(bg, pal.Warn)
	}
	return strings.Join([]string{
		ink(bg, pal.FgDim).Render(fig),
		ink(bg, bg).Render(" "),
		barInk.Render(overlay.Fill(bar, cells, bg)),
		ink(bg, bg).Render(" "),
		markInk.Render(mark),
		ink(bg, pal.FgMute).Render(" " + a.Pass),
	}, "")
}
