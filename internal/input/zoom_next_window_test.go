package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// TestNextWindowWhileZoomedZoomsTheNextPane is the user's report: prefix+z,
// then alt+n. The focus moved to the next pane, the zoomed pane kept the whole
// region, and the cursor was drawn at the position the focused pane would have
// had, so keys went to a pane nobody could see. The pane alt+n lands on now
// takes the zoom.
func TestNextWindowWhileZoomedZoomsTheNextPane(t *testing.T) {
	o := twoPaneWM(t)
	o.Mode = app.TerminalMode
	a, b := o.Windows[0], o.Windows[1]

	o.ToggleZoom()
	if !a.Zoomed {
		t.Fatal("setup: prefix+z did not zoom the focused pane")
	}
	box := [4]int{a.X, a.Y, a.Width, a.Height}

	o, _ = HandleKeyPress(tea.KeyPressMsg{Code: 'n', Mod: tea.ModAlt, Text: "n"}, o)

	if got := focusedID(o); got != "b" {
		t.Fatalf("alt+n focused %q, want b", got)
	}
	if !b.Zoomed || a.Zoomed {
		t.Fatalf("the focused pane is not the zoomed one: a.Zoomed=%t b.Zoomed=%t", a.Zoomed, b.Zoomed)
	}
	if got := [4]int{b.X, b.Y, b.Width, b.Height}; got != box {
		t.Errorf("the focused pane holds %v, want the zoom box %v", got, box)
	}
	// Where the pane alt+n left goes is the tiler's answer, and the fixture's
	// panes have no emulator for it to size; app's TestZoomFollowsFocusUnderTiling
	// covers that with live panes.
}
