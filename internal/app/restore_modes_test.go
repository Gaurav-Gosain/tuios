package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// TestRestoreTerminalContentAppliesInputRoutingState is the client half of the
// daemon reattach fix: every attach path (initial attach, reattach, session
// switch, workspace switch prime) rebuilds the per-window emulator and then
// applies the daemon's snapshot through restoreTerminalContent. The snapshot
// carried the mouse modes all along, but the input layer reads cached getters
// that RestoreModes left stale, so wheel, motion and click were routed to
// scrollback and copy mode instead of the pane. The kitty keyboard flags were
// not carried at all.
func TestRestoreTerminalContentAppliesInputRoutingState(t *testing.T) {
	win := newTestWindow(t, "restore-modes", 82, 26)
	o := newTestOS(win)

	// The snapshot the daemon sends for a browser pane whose enable sequences
	// have long scrolled out of the bounded output buffer.
	state := &session.TerminalState{
		Width:  80,
		Height: 24,
		Modes: map[int]bool{
			1003: true, // any-motion mouse tracking
			1006: true, // SGR mouse encoding
			1016: true, // SGR-pixel mouse encoding
			2004: true, // bracketed paste
		},
		KittyKbdStack: []int{0, 31},
	}

	o.restoreTerminalContent(win, state)

	if !win.Terminal.HasMouseMode() {
		t.Fatal("window emulator reports HasMouseMode() = false after restore: mouse events will not reach the pane")
	}
	if !win.Terminal.HasAllMotionMode() {
		t.Fatal("window emulator lost any-motion tracking (?1003) after restore")
	}
	if got := win.Terminal.KittyKeyboardFlags(); got != 31 {
		t.Fatalf("KittyKeyboardFlags() = %d after restore, want 31: keys will be encoded in legacy form", got)
	}
}
