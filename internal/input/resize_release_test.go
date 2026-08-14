package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// floatingResizeOS builds one focused floating pane whose guest has asked for
// mouse tracking, in terminal mode: the arrangement where a border drag has to
// share the pointer with the app underneath it.
func floatingResizeOS(t *testing.T) (*app.OS, *terminal.Window) {
	t.Helper()
	em := vt.NewEmulator(80, 24)
	t.Cleanup(func() { _ = em.Close() })
	if _, err := em.Write([]byte("\x1b[?1000h")); err != nil {
		t.Fatalf("enabling mouse mode: %v", err)
	}
	// Anything forwarded to the guest has to go somewhere or the write blocks.
	go func() {
		buf := make([]byte, 256)
		for {
			if _, err := em.Read(buf); err != nil {
				return
			}
		}
	}()

	win := &terminal.Window{ID: "resize-pane", Terminal: em, X: 2, Y: 2, Width: 82, Height: 26}
	o := &app.OS{
		Mode:                     app.TerminalMode,
		FocusedWindow:            0,
		Windows:                  []*terminal.Window{win},
		Width:                    120,
		Height:                   40,
		PendingResizes:           make(map[string][2]int),
		DraggedWindowIndex:       -1,
		ScrollbarDragWindowIndex: -1,
	}
	return o, win
}

// armBottomBorderResize presses the pane's bottom edge and drags it up two
// rows, leaving a border resize in progress with a PTY resize deferred.
func armBottomBorderResize(t *testing.T, o *app.OS, win *terminal.Window) {
	t.Helper()
	borderY := win.Y + win.Height - 1
	midX := win.X + win.Width/2
	handleMouseClick(tea.MouseClickMsg{Button: tea.MouseLeft, X: midX, Y: borderY}, o)
	if !o.Resizing || !o.BorderResizing {
		t.Fatalf("press on the bottom border did not arm a resize (resizing=%v border=%v)", o.Resizing, o.BorderResizing)
	}
	handleMouseMotion(tea.MouseMotionMsg{Button: tea.MouseLeft, X: midX, Y: borderY - 2}, o)
}

// assertGestureEnded is the whole contract: nothing about the gesture outlives
// the button. InteractionMode is left out because a finished resize holds it
// 150ms on purpose, so the guest's prompt redraws before content polling
// resumes; the paths that end a stray gesture clear it at once and say so.
func assertGestureEnded(t *testing.T, o *app.OS, win *terminal.Window) {
	t.Helper()
	if o.Resizing {
		t.Error("Resizing still set after release")
	}
	if o.BorderResizing {
		t.Error("BorderResizing still set after release")
	}
	if o.BorderResizeEdge != app.BorderEdgeNone {
		t.Errorf("BorderResizeEdge = %v after release, want BorderEdgeNone", o.BorderResizeEdge)
	}
	if win.IsBeingManipulated {
		t.Error("pane still marked IsBeingManipulated after release")
	}
	if len(o.PendingResizes) != 0 {
		t.Errorf("PendingResizes not flushed after release: %d left", len(o.PendingResizes))
	}
}

// TestReleaseEndsResizeWhoeverClaimsIt is the regression for the resize that got
// stuck. handleMouseRelease returns early down a dozen paths, and every one of
// them used to leave a live resize running: the "Resizing..." readout stayed on
// screen and the panes kept their resize borders with no button pressed.
func TestReleaseEndsResizeWhoeverClaimsIt(t *testing.T) {
	cases := []struct {
		name    string
		interru func(o *app.OS, win *terminal.Window)
	}{
		// The guest asked for mouse tracking, so the release is forwarded to it
		// and never reaches the cleanup.
		{"mouse-tracking guest", func(*app.OS, *terminal.Window) {}},
		{"overlay grabbed the pointer", func(o *app.OS, _ *terminal.Window) {
			o.OverlayDrag.Active = true
		}},
		{"scrollbar drag", func(o *app.OS, _ *terminal.Window) {
			o.ScrollbarDragging = true
			o.ScrollbarDragWindowIndex = 0
		}},
		{"text selection", func(o *app.OS, win *terminal.Window) {
			win.EnterCopyModeImplicit()
			o.Dragging = true
			o.DraggedWindowIndex = 0
		}},
		{"mode change", func(o *app.OS, _ *terminal.Window) {
			o.Mode = app.WindowManagementMode
			o.SidebarFocused = true
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o, win := floatingResizeOS(t)
			armBottomBorderResize(t, o, win)
			tc.interru(o, win)

			midX := win.X + win.Width/2
			handleMouseRelease(tea.MouseReleaseMsg{Button: tea.MouseLeft, X: midX, Y: win.Y + win.Height - 3}, o)
			assertGestureEnded(t, o, win)
		})
	}
}

// TestReleaseWithNoButtonHeldEndsResize covers the release that comes back
// naming no button at all, which is what a pointer leaving the surface the
// events come from reports. It ends the gesture like any other release.
func TestReleaseWithNoButtonHeldEndsResize(t *testing.T) {
	o, win := floatingResizeOS(t)
	armBottomBorderResize(t, o, win)

	handleMouseRelease(tea.MouseReleaseMsg{Button: tea.MouseNone, X: 0, Y: 0}, o)
	assertGestureEnded(t, o, win)
}

// TestResizeBorrowsWindowMode: for the length of the gesture the pointer
// belongs to the resize, not to the guest under it, so the app behaves as
// window management and gives the mode back when the button comes up. Same
// bargain a ctrl-drag move already strikes: resizing a pane is not a request to
// stop typing in it.
func TestResizeBorrowsWindowMode(t *testing.T) {
	t.Run("started in terminal mode", func(t *testing.T) {
		o, win := floatingResizeOS(t)
		armBottomBorderResize(t, o, win)
		if o.Mode != app.WindowManagementMode {
			t.Fatalf("mode during resize = %v, want window management", o.Mode)
		}

		midX := win.X + win.Width/2
		handleMouseRelease(tea.MouseReleaseMsg{Button: tea.MouseLeft, X: midX, Y: win.Y + win.Height - 3}, o)
		if o.Mode != app.TerminalMode {
			t.Errorf("mode after resize = %v, want the terminal mode it started in", o.Mode)
		}
	})

	// A gesture whose release is lost gives the mode back too, or the user is
	// stranded in window management having done nothing to ask for it.
	t.Run("release lost", func(t *testing.T) {
		o, win := floatingResizeOS(t)
		armBottomBorderResize(t, o, win)

		o.Update(tea.MouseMotionMsg{Button: tea.MouseNone, X: 0, Y: 0})
		if o.Mode != app.TerminalMode {
			t.Errorf("mode after a lost release = %v, want the terminal mode it started in", o.Mode)
		}
	})

	// Started in window management, it stays there: there is nothing to give back.
	t.Run("started in window mode", func(t *testing.T) {
		o, win := floatingResizeOS(t)
		o.Mode = app.WindowManagementMode
		armBottomBorderResize(t, o, win)

		midX := win.X + win.Width/2
		handleMouseRelease(tea.MouseReleaseMsg{Button: tea.MouseLeft, X: midX, Y: win.Y + win.Height - 3}, o)
		if o.Mode != app.WindowManagementMode {
			t.Errorf("mode after resize = %v, want window management", o.Mode)
		}
	})
}

// TestPointerLeavingThePaneEndsResize: motion reporting no button held while a
// resize is supposedly in progress means the release happened out of reach.
func TestPointerLeavingThePaneEndsResize(t *testing.T) {
	o, win := floatingResizeOS(t)
	armBottomBorderResize(t, o, win)

	o.Update(tea.MouseMotionMsg{Button: tea.MouseNone, X: 0, Y: 0})
	assertGestureEnded(t, o, win)
	if o.InteractionMode {
		t.Error("InteractionMode still set after the pointer left with no button held")
	}
}
