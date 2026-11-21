package vis

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

const recent = tapeCols

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

	shown := rows * cols
	dim := ink(bg, pal.FgDim)
	mute := ink(bg, pal.FgMute)
	head := ink(pal.Fg, pal.Canvas)
	alarm := bold(pal.Warn, pal.Canvas)

	for r := range rows {
		var b strings.Builder
		for c := range cols {
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
