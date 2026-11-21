package vis

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/charmbracelet/x/ansi"
)

// Render draws one frame from one snapshot. It is pure, which is what lets the
// tests assert on the drawn output rather than on the state behind it. Two of
// the display's bugs were only ever visible in the drawn frame.
func Render(s Snapshot, o Options) string {
	pal := palette(o.Mono)
	w, h := max(o.Width, 1), max(o.Height, 1)

	rail := renderRail(s, o, pal, railWidth, h)

	var lines []string
	if leftW := w - railWidth; leftW >= 24 {
		left := renderLeft(s, o, pal, leftW, h)
		lines = zip(left, rail)
	} else {
		lines = renderRail(s, o, pal, w, h)
	}

	if s.Done {
		lines = overlayCard(lines, s, o, pal, w, h)
	}
	return strings.Join(lines, "\n")
}

func renderLeft(s Snapshot, o Options, pal overlay.Palette, w, h int) []string {
	streamH := min(tapeRows+1, h)
	var top []string
	if o.Screen != nil {
		top = renderViewport(s, o, pal, w, h-streamH)
	}
	streamH = h - len(top)

	var stream []string
	if s.Phase == PhaseGenerating {
		stream = renderTape(s, pal, w, streamH)
	} else {
		stream = renderFunnel(s, pal, w, streamH)
	}
	return fit(append(top, stream...), w, h, pal.Canvas)
}

func renderViewport(s Snapshot, o Options, pal overlay.Palette, w, h int) []string {
	screen := o.Screen()
	if screen == "" || h < 3 {
		return nil
	}
	g := glyphs()
	body := strings.Split(strings.TrimRight(screen, "\n"), "\n")
	appW, appH := 0, len(body)
	for _, l := range body {
		appW = max(appW, lipgloss.Width(l))
	}
	inner := min(appW, w-2)
	if inner < 1 {
		return nil
	}

	edge := pal.FgMute
	if s.Violated {
		edge = pal.Warn
	}
	rows := h - 2
	if len(body) > rows {
		body = body[:max(rows, 0)]
	}

	size := itoa(appW) + g.times + itoa(appH)
	if inner < appW || len(body) < appH {
		size += " " + g.sep + " showing " + itoa(inner) + g.times + itoa(len(body))
	}
	title := " under test " + g.sep + " " + size + " "
	title = overlay.Truncate(title, max(inner-2, 1))

	bar := ink(pal.Canvas, edge)
	fill := max(inner-lipgloss.Width(title)-1, 0)
	out := []string{row(w, pal.Canvas,
		bar.Render(g.tl+g.h), bar.Bold(s.Violated).Render(title), bar.Render(strings.Repeat(g.h, fill)+g.tr))}
	for _, l := range body {
		out = append(out, row(w, pal.Canvas,
			bar.Render(g.v),
			overlay.Fill(ansi.Truncate(l, inner, ""), inner, pal.Canvas),
			bar.Render(g.v)))
	}
	out = append(out, row(w, pal.Canvas, bar.Render(g.bl+strings.Repeat(g.h, inner)+g.br)))
	return out
}

func zip(left, right []string) []string {
	out := make([]string, 0, max(len(left), len(right)))
	for i := range max(len(left), len(right)) {
		var l, r string
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		out = append(out, l+r)
	}
	return out
}

func fit(lines []string, w, h int, bg color.Color) []string {
	out := make([]string, 0, h)
	for i := range h {
		if i < len(lines) {
			out = append(out, overlay.Fill(ansi.Truncate(lines[i], w, ""), w, bg))
			continue
		}
		out = append(out, overlay.Fill("", w, bg))
	}
	return out
}

func ghost(lines []string, pal overlay.Palette) []string {
	dim := ink(pal.Canvas, pal.FgMute)
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = dim.Render(ansi.Strip(l))
	}
	return out
}
