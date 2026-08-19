package app

import (
	"image/color"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The collapsed rail was designed by eye and shipped without a single one of
// its inks measured. Four of them were under the floor, and the two worst were
// invisible rather than merely dim, which is exactly the class of fault looking
// at a screenshot does not catch. These are the measurements, held the way the
// dock's workspace pills are held.
//
// Ratios are asserted rather than colours: the palette follows the terminal
// theme, so pinning a hex here would pass for exactly one theme.

// TestStripMarksClearTheContrastFloor covers everything on the strip that is a
// mark or a control, as opposed to a ground or a rule.
func TestStripMarksClearTheContrastFloor(t *testing.T) {
	pal := theme.UI()
	for _, tc := range []struct {
		name   string
		fg, bg color.Color
		before float64
	}{
		// The one mark that says the spine is cut. Under the floor, a strip with
		// more sessions than it has lines looked like a strip with exactly as
		// many sessions as lines.
		{"the tail mark", theme.Readable(pal.FgMute, pal.Panel), pal.Panel, 2.19},
		// The strip's only two clickable things were its least visible marks.
		{"a control at rest", theme.Readable(pal.FgMute, pal.Panel), pal.Panel, 2.19},
		{"a control on a hovered row", theme.Readable(pal.FgMute, pal.Surface), pal.Surface, 1.81},
		// A pane that has errored is the loudest thing the spine can say and was
		// the least readable severity it could draw.
		{"an errored session", theme.Readable(sidebarSeverityColor("errored", pal), pal.Panel), pal.Panel, 4.06},
		// Already cleared; pinned so a palette change cannot quietly drop it.
		{"a session wanting input", theme.Readable(sidebarSeverityColor("needs_input", pal), pal.Panel), pal.Panel, 6.49},
		// The accent follows the terminal theme, so it is legible only on the
		// themes that happen to be bright ones. This is the number the current
		// workspace pill was lifted from.
		{"the attached session's bar", theme.Readable(railFocusTint(pal.Accent, pal), pal.Panel), pal.Panel, 2.76},
	} {
		if got := theme.ContrastRatio(tc.fg, tc.bg); got < theme.ContrastFloor {
			t.Errorf("%s measures %.2f:1 against its ground, under the %.1f:1 floor (was %.2f:1)",
				tc.name, got, theme.ContrastFloor, tc.before)
		}
	}
}

// TestStripMarksKeepTheirHierarchy: the floor is a floor, not a flattening. A
// control and a tail mark are quieter than a session, and lifting them to be
// legible must not make them read as more sessions.
func TestStripMarksKeepTheirHierarchy(t *testing.T) {
	pal := theme.UI()
	quiet := theme.ContrastRatio(theme.Readable(pal.FgMute, pal.Panel), pal.Panel)
	resting := theme.ContrastRatio(stripRestingInk(false, pal), pal.Panel)
	if quiet >= resting {
		t.Errorf("a control measures %.2f:1 and a resting session %.2f:1; the quiet marks are no longer quieter",
			quiet, resting)
	}
}

// TestStripRulesAreVisibleFurniture. The text floor deliberately does not apply
// to a hairline: a rule held to 4.5:1 would be louder than the marks it frames.
// It still has to be on the screen, and at the notification rule's 1.06:1
// neither of these was. The band's edge is called the only boundary that
// survives a terminal with no background to give, and the group's divider is
// the whole reason the agents group does not read as more sessions.
func TestStripRulesAreVisibleFurniture(t *testing.T) {
	pal := theme.UI()
	// A rule has to beat the ground it is on by more than the ground beats the
	// canvas, or it is doing less work than the fill it sits in.
	band := theme.ContrastRatio(pal.Panel, pal.Canvas)
	for _, tc := range []struct {
		name   string
		fg, bg color.Color
	}{
		{"the band's edge", pal.FgMute, pal.Panel},
		{"the agents divider", pal.FgMute, pal.Panel},
	} {
		got := theme.ContrastRatio(tc.fg, tc.bg)
		if got <= band {
			t.Errorf("%s measures %.2f:1, no louder than the band's own %.2f:1 ground: it is not on the screen",
				tc.name, got, band)
		}
		if got >= theme.ContrastFloor {
			t.Errorf("%s measures %.2f:1, at or over the %.1f:1 text floor: furniture drawn as loud as the marks it frames",
				tc.name, got, theme.ContrastFloor)
		}
	}
}

// TestStripGroundsStayGrounds: the band and the hover fill are grounds, and a
// ground that measures like a message is a slab. These are the two numbers the
// strip's design rests on, so they are pinned as ceilings rather than floors.
func TestStripGroundsStayGrounds(t *testing.T) {
	pal := theme.UI()
	for _, tc := range []struct {
		name   string
		fg, bg color.Color
	}{
		{"the band against the canvas", pal.Panel, pal.Canvas},
		{"the hover fill against the band", stripRowBg(true, pal), stripRowBg(false, pal)},
	} {
		if got := theme.ContrastRatio(tc.fg, tc.bg); got > 1.6 {
			t.Errorf("%s measures %.2f:1, loud enough to read as a mark rather than as a ground", tc.name, got)
		}
	}
}
