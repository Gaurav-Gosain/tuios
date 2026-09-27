package overlay

import (
	"image/color"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Empty is a designed empty or loading state: what a list region shows when it
// has no rows. It says why the region is empty and offers the one thing to do
// next, centred in the room the rows would have taken, so an empty list reads
// as meant rather than broken.
type Empty struct {
	// Message is the headline, in FgDim.
	Message string
	// Detail is quieter lines under the headline, in FgMute, each already cut
	// to the region's width by the caller. Most states have none.
	Detail []string
	// Hint is the one key worth pressing next, drawn the way a footer draws
	// it. The zero Hint draws nothing.
	Hint Hint
}

// LoadingDelay is how long a load runs before its loading state is drawn.
// Most loads finish sooner, and an indicator that flashes for a frame reads as
// a flicker rather than as progress. The half second is the delay the Textual
// command palette settled on for the same reason.
const LoadingDelay = 500 * time.Millisecond

// ShowLoading reports whether a load that started at started has run long
// enough, at now, for its loading state to be drawn. A zero start is no load.
func ShowLoading(started, now time.Time) bool {
	return !started.IsZero() && now.Sub(started) >= LoadingDelay
}

// Lines draws e centred in a region width cells wide and rows rows tall on bg,
// and returns exactly rows lines. When the region is too short for all of it,
// the detail goes first and then the hint, since the headline is what says why
// the region is empty.
func (e Empty) Lines(width, rows int, bg color.Color, pal Palette) []string {
	width, rows = max(width, 1), max(rows, 1)
	blank := Style(bg).Render(strings.Repeat(" ", width))
	center := func(s string) string {
		w := lipgloss.Width(s)
		if w > width {
			s, w = ansi.Truncate(s, width, Ellipsis()), width
		}
		left := (width - w) / 2
		return Fill(Style(bg).Render(strings.Repeat(" ", left))+s, width, bg)
	}

	msg := center(Style(bg).Foreground(pal.FgDim).Render(e.Message))
	detail := make([]string, 0, len(e.Detail))
	for _, d := range e.Detail {
		if d == "" {
			detail = append(detail, blank)
			continue
		}
		detail = append(detail, center(Style(bg).Foreground(pal.FgMute).Render(d)))
	}
	var hint []string
	if e.Hint.Key != "" {
		strip, _ := renderHints(fitHints([]Hint{e.Hint}, width, footerSep), footerSep, bg, pal)
		hint = []string{blank, center(strip)}
	}

	block := append(append([]string{msg}, detail...), hint...)
	if len(block) > rows {
		block = append([]string{msg}, hint...)
	}
	if len(block) > rows {
		block = []string{msg}
	}
	out := make([]string, 0, rows)
	top := (rows - len(block)) / 2
	for range top {
		out = append(out, blank)
	}
	out = append(out, block...)
	for len(out) < rows {
		out = append(out, blank)
	}
	return out
}
