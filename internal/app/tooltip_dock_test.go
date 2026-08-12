package app

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// dockTooltipAt hovers a session control and returns the label layer it drew.
// The delay is faked rather than waited out: the clock is arriving motion, and
// what is under test is what the frame does once it has elapsed.
func dockTooltipAt(t *testing.T, m *OS, a DockSessionAction) (*lipgloss.Layer, dockSessionHit) {
	t.Helper()
	m.renderDockString() // the rects a hover is tested against are recorded as they are drawn
	for _, h := range m.dockSessionHits {
		if h.Action != a {
			continue
		}
		if !m.DockSessionHoverAt(h.X0, h.Y) {
			t.Fatalf("the pointer on column %d of %v was not on a control", h.X0, a)
		}
		if !m.TooltipPending() {
			t.Fatalf("hovering %v armed no label", a)
		}
		m.Tooltip.At = time.Now().Add(-2 * tooltipDelay)
		return m.renderTooltip(), h
	}
	t.Fatalf("the dock drew no control for %v", a)
	return nil, dockSessionHit{}
}

// TestDockSessionTooltipNamesTheControl is the whole trade. The words came off
// the bar, so the glyph has to be able to say them again on demand.
func TestDockSessionTooltipNamesTheControl(t *testing.T) {
	for _, tc := range []struct {
		action DockSessionAction
		want   string
	}{
		{DockSessionLeave, dockSessionLeaveLabel},
		{DockSessionClose, dockSessionCloseLabel},
	} {
		t.Run(tc.want, func(t *testing.T) {
			m := dockSessionOS(t, 160, true)
			layer, _ := dockTooltipAt(t, m, tc.action)
			if layer == nil {
				t.Fatal("the hover drew no label")
			}
			if got := stripANSIForTrace(layer.GetContent()); !strings.Contains(got, tc.want) {
				t.Errorf("the label reads %q, want it to name %q", got, tc.want)
			}
		})
	}
}

// TestDockSessionTooltipNeverCoversItsOwnControl: the bar is one row, so a label
// drawn on it would sit on the very glyph the pointer is asking about. It goes
// to the hairline row instead, which is above the bar or below it depending on
// where the dock is.
func TestDockSessionTooltipNeverCoversItsOwnControl(t *testing.T) {
	for _, pos := range []string{"bottom", "top"} {
		t.Run(pos, func(t *testing.T) {
			prev := config.DockbarPosition
			config.DockbarPosition = pos
			t.Cleanup(func() { config.DockbarPosition = prev })

			m := dockSessionOS(t, 160, true)
			layer, hit := dockTooltipAt(t, m, DockSessionClose)
			if layer == nil {
				t.Fatal("the hover drew no label")
			}
			if layer.GetY() == hit.Y {
				t.Fatalf("the label is on row %d, the same row as the control it names", hit.Y)
			}
			want := hit.Y - 1
			if pos == "top" {
				want = hit.Y + 1
			}
			if got := layer.GetY(); got != want {
				t.Errorf("the label is on row %d, want %d so it opens away from the bar", got, want)
			}
		})
	}
}

// TestDockSessionTooltipStaysOnTheScreen: these two controls hold the bar's
// right-hand end, so a label anchored at the control's own first column is the
// case that runs off the edge. It gives ground rightward before it gives any
// leftward, at every width the controls are drawn at.
func TestDockSessionTooltipStaysOnTheScreen(t *testing.T) {
	for _, width := range []int{160, 100, 40, dockSessionIconMinWidth} {
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			m := dockSessionOS(t, width, true)
			layer, _ := dockTooltipAt(t, m, DockSessionClose)
			if layer == nil {
				t.Fatal("the hover drew no label")
			}
			x, w := layer.GetX(), lipgloss.Width(layer.GetContent())
			if x < 0 {
				t.Errorf("the label starts at x=%d, off the left of the screen", x)
			}
			if x+w > width {
				t.Errorf("the label runs to x=%d past the screen at %d", x+w, width)
			}
		})
	}
}

// TestDockSessionTooltipClearsOnTheWayOut: the label is gesture-scoped, so
// leaving the control drops it and stops the tick it was holding.
func TestDockSessionTooltipClearsOnTheWayOut(t *testing.T) {
	m := dockSessionOS(t, 160, true)
	layer, hit := dockTooltipAt(t, m, DockSessionClose)
	if layer == nil {
		t.Fatal("the hover drew no label")
	}
	if m.DockSessionHoverAt(0, hit.Y) {
		t.Fatal("column 0 of the bar reported a control under the pointer")
	}
	if m.Tooltip.Source != tooltipNone {
		t.Errorf("moving off the control left a %v label armed", m.Tooltip.Source)
	}
	if m.TooltipPending() {
		t.Error("the pointer is on nothing and a label is pending anyway")
	}
	if m.renderTooltip() != nil {
		t.Error("the pointer is on nothing and a label drew anyway")
	}
}

// TestDockSessionTooltipCostsNoIdleTick: the pending flag is the only thing that
// holds the maintenance tick open, and it closes on the frame that draws the
// label. A tooltip left up must not keep the app awake.
func TestDockSessionTooltipCostsNoIdleTick(t *testing.T) {
	m := dockSessionOS(t, 160, true)
	if m.TooltipPending() {
		t.Fatal("a fresh dock is already pending a label")
	}
	if _, _ = dockTooltipAt(t, m, DockSessionClose); m.TooltipPending() {
		t.Error("the label has been drawn and is still holding the tick open")
	}
}

// TestDockSessionTooltipsCanBeTurnedOff: one key covers both surfaces, and the
// hover highlight is not part of the bargain. A user who turned the labels off
// still gets the control they are about to click drawn as the one they are about
// to click.
func TestDockSessionTooltipsCanBeTurnedOff(t *testing.T) {
	prev := config.Tooltips
	config.Tooltips = false
	t.Cleanup(func() { config.Tooltips = prev })

	m := dockSessionOS(t, 160, true)
	m.renderDockString()
	for _, h := range m.dockSessionHits {
		if h.Action != DockSessionClose {
			continue
		}
		if !m.DockSessionHoverAt(h.X0, h.Y) {
			t.Fatal("turning labels off also turned the hover off")
		}
		if m.dockSessionHover != DockSessionClose {
			t.Fatalf("the hover highlight is on %v, want the control under the pointer", m.dockSessionHover)
		}
		if m.TooltipPending() {
			t.Error("labels are off and one is pending anyway")
		}
		m.Tooltip.At = time.Now().Add(-2 * tooltipDelay)
		if m.renderTooltip() != nil {
			t.Error("labels are off and one drew anyway")
		}
		return
	}
	t.Fatal("the dock drew no close control")
}

// TestBothSessionActionsAreStillNamedInWords is the discoverability half of
// taking the words off the bar. A keyboard user never hovers, so the two
// surfaces they do reach have to say what the two glyphs mean, and say it
// briefly: the help menu is already wider than it should be.
func TestBothSessionActionsAreStillNamedInWords(t *testing.T) {
	const maxLen = 24
	for action, want := range map[string]string{
		"prefix_detach":        "leave running",
		"prefix_close_session": "close session",
	} {
		desc, ok := config.ActionDescriptions[action]
		if !ok {
			t.Errorf("the help menu has no entry for %s", action)
			continue
		}
		if !strings.Contains(strings.ToLower(desc), want) {
			t.Errorf("the help entry for %s reads %q, want it to say %q in words", action, desc, want)
		}
		if len(desc) > maxLen {
			t.Errorf("the help entry for %s is %d characters (%q), want at most %d", action, len(desc), desc, maxLen)
		}
	}

	// The which-key sheet is the other one, and it only carries detach on the
	// run path that has something to detach from.
	words := map[bool][]string{true: {"Detach session", "Close session"}, false: {"Close session"}}
	for daemon, wants := range words {
		var sheet strings.Builder
		for _, b := range config.GetPrefixKeybindings("", daemon) {
			sheet.WriteString(b.Description + "\n")
		}
		for _, want := range wants {
			if !strings.Contains(sheet.String(), want) {
				t.Errorf("the which-key sheet (daemon=%v) never says %q:\n%s", daemon, want, sheet.String())
			}
		}
	}
}
