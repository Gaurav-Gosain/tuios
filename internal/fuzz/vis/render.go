package vis

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/charmbracelet/x/ansi"
)

// The frame. Two bands: the app under test and the action stream on Canvas at
// the left, the instruments on a Panel band at the right. That figure/ground
// split is the whole reason the harness is never mistaken for the app it is
// hammering: app content sits on Canvas inside a hairline, instruments sit on
// their own ground, and one accent chip is the only fill at rest.

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
		// Too narrow to carry the app beside the instruments. The instruments
		// alone are still the whole truth of the run, so they take the frame
		// rather than both halves being squeezed into something unreadable.
		lines = renderRail(s, o, pal, w, h)
	}

	if s.Done {
		lines = overlayCard(lines, s, o, pal, w, h)
	}
	return strings.Join(lines, "\n")
}

// renderLeft is the app viewport over the action stream. During minimisation
// the funnel takes the stream's rows, because generation has stopped and replay
// is what is happening.
func renderLeft(s Snapshot, o Options, pal overlay.Palette, w, h int) []string {
	var top []string
	if o.Screen != nil {
		top = renderViewport(s, o, pal, w)
	}
	// The stream keeps at least three rows; below that it says nothing.
	streamH := h - len(top)
	if streamH < 3 {
		top = top[:max(len(top)+streamH-3, 0)]
		streamH = h - len(top)
	}

	var stream []string
	if s.Phase == PhaseGenerating {
		stream = renderTape(s, pal, w, streamH)
	} else {
		stream = renderFunnel(s, pal, w, streamH)
	}
	return fit(append(top, stream...), w, h, pal.Canvas)
}

// renderViewport puts the app's own frame inside a hairline. The app keeps its
// own colours untouched: this display never restyles the thing it is testing,
// because a screenshot of a recoloured app is a screenshot of something that
// does not exist.
//
// The border and its title are the harness speaking, so they carry the phase.
// When a rule breaks they go Cherry, which says the harness found something
// rather than that the app drew something red.
func renderViewport(s Snapshot, o Options, pal overlay.Palette, w int) []string {
	screen := o.Screen()
	if screen == "" {
		return nil
	}
	g := glyphs()
	body := strings.Split(strings.TrimRight(screen, "\n"), "\n")
	// The app's own size, which the title reports. It is the size the engine
	// last resized the app to, so it moves when a resize action lands. Reporting
	// the width the frame happened to have room for instead would put a number
	// on screen that is about this display rather than about the app.
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
	size := itoa(appW) + g.times + itoa(appH)
	if inner < appW {
		// Saying the app is 80 wide while showing 60 of it is the kind of quiet
		// lie this display exists to avoid.
		size += " " + g.sep + " clipped"
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

// zip lays two columns of equal height side by side.
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

// fit forces a block to exact dimensions on its own ground. Every band is
// squared off this way, because a row one cell short is a transparent gap that
// shows whatever the terminal had there before.
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

// ghost re-renders a frame at mute weight so the end card has something honest
// behind it: the final state of the run it is reporting on, not a decoration.
// It is a redraw rather than an SGR rewrite because rewriting the escape codes
// of a frame is a capture shortcut that mangles the app's own colours.
func ghost(lines []string, pal overlay.Palette) []string {
	dim := ink(pal.Canvas, pal.FgMute)
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = dim.Render(ansi.Strip(l))
	}
	return out
}
