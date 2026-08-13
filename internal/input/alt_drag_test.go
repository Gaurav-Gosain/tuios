package input

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

func altClickMsg(x, y int) tea.MouseClickMsg {
	return tea.MouseClickMsg{Button: tea.MouseLeft, X: x, Y: y, Mod: tea.ModAlt}
}

func altMotionMsg(x, y int) tea.MouseMotionMsg {
	return tea.MouseMotionMsg{Button: tea.MouseLeft, X: x, Y: y, Mod: tea.ModAlt}
}

func altReleaseMsg(x, y int) tea.MouseReleaseMsg {
	return tea.MouseReleaseMsg{Button: tea.MouseLeft, X: x, Y: y, Mod: tea.ModAlt}
}

func altRightClickMsg(x, y int) tea.MouseClickMsg {
	return tea.MouseClickMsg{Button: tea.MouseRight, X: x, Y: y, Mod: tea.ModAlt}
}

// withAltDrag sets the gesture on or off for a test and restores it after.
func withAltDrag(t *testing.T, on bool) {
	t.Helper()
	prev := config.AltDrag
	config.AltDrag = on
	t.Cleanup(func() { config.AltDrag = prev })
}

// TestAltDragMovesThePaneFromItsContent is the gesture the maintainer asked for:
// a plain left drag over a pane's content selects text, so moving the pane
// needed a modifier, and alt is the one every desktop window manager uses.
func TestAltDragMovesThePaneFromItsContent(t *testing.T) {
	withAltDrag(t, true)
	o, wa, wb := twoPaneBSP(t)
	left, _ := leftPaneOf(wa, wb)
	leftIdx := indexOf(o, left)
	cx, cy := contentCell(left)

	handleMouseClick(altClickMsg(cx, cy), o)

	if !o.Dragging {
		t.Fatal("alt+left press did not grab the pane; it must not wait for a threshold")
	}
	if o.DraggedWindowIndex != leftIdx {
		t.Errorf("dragging window %d, want the pane under the pointer (%d)", o.DraggedWindowIndex, leftIdx)
	}
	if o.FocusedWindow != leftIdx {
		t.Errorf("focused window %d, want %d", o.FocusedWindow, leftIdx)
	}
	if !left.IsBeingManipulated {
		t.Error("the grabbed pane is not marked as being manipulated")
	}
}

// TestAltDragLandsWhereItIsReleased follows the gesture to its end through the
// real press/motion/release path.
func TestAltDragLandsWhereItIsReleased(t *testing.T) {
	withAltDrag(t, true)
	o, wa, wb := twoPaneBSP(t)
	left, _ := leftPaneOf(wa, wb)
	cx, cy := contentCell(left)

	handleMouseClick(altClickMsg(cx, cy), o)
	handleMouseMotion(altMotionMsg(cx+20, cy+6), o)
	moved := left.X
	handleMouseRelease(altReleaseMsg(cx+20, cy+6), o)

	if o.Dragging {
		t.Error("the drag outlived the button")
	}
	assertGestureEnded(t, o, left)
	if moved == 0 && left.X == 0 {
		t.Error("the pane never moved during the drag")
	}
}

// TestAltDragGivesTypingBackWhenThePaneLands is the mode bargain: the gesture
// borrows window management to run in, because a guest with mouse tracking would
// otherwise be handed the motion, and gives the mode back on the drop. Moving a
// pane is not a request to stop typing.
func TestAltDragGivesTypingBackWhenThePaneLands(t *testing.T) {
	withAltDrag(t, true)
	o, wa, wb := twoPaneBSP(t)
	left, _ := leftPaneOf(wa, wb)
	cx, cy := contentCell(left)
	o.FocusedWindow = indexOf(o, left)
	o.Mode = app.TerminalMode

	handleMouseClick(altClickMsg(cx, cy), o)
	if o.Mode != app.WindowManagementMode {
		t.Fatalf("mode = %v during the drag, want window management", o.Mode)
	}
	handleMouseMotion(altMotionMsg(cx+15, cy+4), o)
	handleMouseRelease(altReleaseMsg(cx+15, cy+4), o)

	if o.Mode != app.TerminalMode {
		t.Errorf("mode = %v after the drop, want the terminal mode the gesture interrupted", o.Mode)
	}
}

// TestAltDragEndsCleanlyWhenInterrupted covers the gesture that never gets its
// release: the button comes up somewhere that returns early, or a frame arrives
// with nothing held. It must ride the existing sweeps rather than needing one of
// its own.
func TestAltDragEndsCleanlyWhenInterrupted(t *testing.T) {
	withAltDrag(t, true)

	t.Run("released over an overlay", func(t *testing.T) {
		o, wa, wb := twoPaneBSP(t)
		left, _ := leftPaneOf(wa, wb)
		cx, cy := contentCell(left)
		o.Mode = app.TerminalMode

		handleMouseClick(altClickMsg(cx, cy), o)
		handleMouseMotion(altMotionMsg(cx+10, cy), o)
		// The release lands on a branch that returns before the normal drop.
		o.ShowHelp = true
		handleMouseRelease(altReleaseMsg(cx+10, cy), o)

		if o.Dragging {
			t.Error("the drag survived a release that returned early")
		}
		if o.Mode != app.TerminalMode {
			t.Errorf("mode = %v, want the borrowed mode given back", o.Mode)
		}
	})

	// The gesture keeps no state of its own: it holds Dragging and the mode
	// borrow, which are exactly what the existing sweeps key on. That is what
	// lets a lost release end it without a new mechanism beside the old ones.
	t.Run("a release that never arrives", func(t *testing.T) {
		o, wa, wb := twoPaneBSP(t)
		left, _ := leftPaneOf(wa, wb)
		cx, cy := contentCell(left)
		o.Mode = app.TerminalMode

		handleMouseClick(altClickMsg(cx, cy), o)
		handleMouseMotion(altMotionMsg(cx+10, cy), o)

		o.EndStrayGesture()
		o.EndPointerGesture()

		if o.Dragging {
			t.Error("the drag survived the stray-gesture sweep")
		}
		assertGestureEnded(t, o, left)
		if o.Mode != app.TerminalMode {
			t.Errorf("mode = %v, want the borrowed mode given back", o.Mode)
		}
	})
}

// TestAltRightDragStillResizes pins the other half of the pair. It was already
// the ordinary right-press resize with alt keeping the menu away; what it did
// not do was give the mode back.
func TestAltRightDragStillResizes(t *testing.T) {
	withAltDrag(t, true)
	o, wa, wb := twoPaneBSP(t)
	left, _ := leftPaneOf(wa, wb)
	cx, cy := contentCell(left)
	o.FocusedWindow = indexOf(o, left)
	o.Mode = app.TerminalMode

	handleMouseClick(altRightClickMsg(cx, cy), o)
	if !o.Resizing {
		t.Fatal("alt+right press did not start a resize")
	}
	if o.ContextMenuActive() {
		t.Error("alt+right opened the context menu")
	}

	handleMouseMotion(tea.MouseMotionMsg{Button: tea.MouseRight, X: cx + 12, Y: cy + 4, Mod: tea.ModAlt}, o)
	handleMouseRelease(tea.MouseReleaseMsg{Button: tea.MouseRight, X: cx + 12, Y: cy + 4, Mod: tea.ModAlt}, o)

	assertGestureEnded(t, o, left)
	if o.Mode != app.TerminalMode {
		t.Errorf("mode = %v after the resize, want the terminal mode it interrupted", o.Mode)
	}
}

// TestCtrlDragStillMoves guards the alias. Alt is the newcomer's gesture; ctrl
// is the one already in the help and in people's fingers, and both stay.
func TestCtrlDragStillMoves(t *testing.T) {
	withAltDrag(t, true)
	o, wa, wb := twoPaneBSP(t)
	left, _ := leftPaneOf(wa, wb)
	cx, cy := contentCell(left)

	handleMouseClick(ctrlClickMsg(cx, cy), o)
	if !o.CtrlDragPending {
		t.Fatal("ctrl+left press no longer arms the move")
	}
	handleMouseMotion(ctrlMotionMsg(cx+20, cy+6), o)
	if !o.CtrlDragging || !o.Dragging {
		t.Fatal("ctrl+drag no longer commits past the threshold")
	}
}

// TestAltDragDisabledReachesTheGuestUnchanged is the opt-out. With the gesture
// off, alt+left over a pane whose app asked for the mouse belongs to that app
// again, and arrives with its modifier intact.
func TestAltDragDisabledReachesTheGuestUnchanged(t *testing.T) {
	withAltDrag(t, false)

	cfg := config.DefaultConfig()
	o, pty := osWithFocusedPane(t, cfg, app.TerminalMode)
	win := o.Windows[0]
	// Mouse bytes for a local pane go into the emulator rather than the pty, so
	// the pane is put in daemon mode with its write routed to the recording pty.
	win.DaemonMode = true
	win.DaemonWriteFunc = func(b []byte) error { _, err := pty.Write(b); return err }
	// Ask for mouse tracking (DECSET 1002) the way a guest does.
	win.Terminal.Write([]byte("\x1b[?1002h\x1b[?1006h")) // button tracking, SGR encoding
	if !win.Terminal.HasMouseMode() {
		t.Fatal("the fixture pane did not take mouse tracking")
	}

	cx, cy := contentCell(win)
	handleMouseClick(altClickMsg(cx, cy), o)

	if o.Dragging {
		t.Fatal("alt+left grabbed the pane with the gesture disabled")
	}
	if len(pty.got) == 0 {
		t.Fatal("alt+left was not forwarded to the guest that asked for the mouse")
	}
	// The modifier reaches the guest unchanged: SGR encodes alt as the +8 bit, so
	// a left press arrives as button 8 rather than button 0.
	if got := string(pty.got); !strings.HasPrefix(got, "\x1b[<8;") {
		t.Errorf("guest received %q, want an SGR left press carrying the alt bit", got)
	}
}

// TestAltDragEnabledIsTakenFromTheGuest is the cost of the default, stated so it
// cannot change unnoticed: with the gesture on it outranks a mouse-tracking app,
// exactly as ctrl+drag already does.
func TestAltDragEnabledIsTakenFromTheGuest(t *testing.T) {
	withAltDrag(t, true)

	o, pty := osWithFocusedPane(t, config.DefaultConfig(), app.TerminalMode)
	win := o.Windows[0]
	// Mouse bytes for a local pane go into the emulator rather than the pty, so
	// the pane is put in daemon mode with its write routed to the recording pty.
	win.DaemonMode = true
	win.DaemonWriteFunc = func(b []byte) error { _, err := pty.Write(b); return err }
	win.Terminal.Write([]byte("\x1b[?1002h\x1b[?1006h")) // button tracking, SGR encoding

	cx, cy := contentCell(win)
	handleMouseClick(altClickMsg(cx, cy), o)

	if !o.Dragging {
		t.Error("alt+left did not grab the pane over a mouse-tracking guest")
	}
	if len(pty.got) != 0 {
		t.Errorf("alt+left was also forwarded to the guest: %q", pty.got)
	}
}

// TestAltDragLeavesAZoomedPaneAlone matches the plain drag: a zoomed pane fills
// the screen and has nowhere to go.
func TestAltDragLeavesAZoomedPaneAlone(t *testing.T) {
	withAltDrag(t, true)
	o, wa, wb := twoPaneBSP(t)
	left, _ := leftPaneOf(wa, wb)
	left.Zoomed = true
	cx, cy := contentCell(left)

	handleMouseClick(altClickMsg(cx, cy), o)

	if o.Dragging {
		t.Error("alt+left grabbed a zoomed pane")
	}
}
