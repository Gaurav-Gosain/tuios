package app

import (
	"fmt"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The deferred half of a resize used to be ended only by a message arriving.
// These tests pin the property that replaced that: the deferral expires on its
// own, so no dropped, superseded or never-armed message can leave the layout
// permanently half-applied.

func newDeferralOS(t *testing.T, width, height, windows int) *OS {
	t.Helper()

	prevAnim := config.AnimationsEnabled
	config.AnimationsEnabled = false
	t.Cleanup(func() { config.AnimationsEnabled = prevAnim })

	m := &OS{
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		WorkspaceFocus:   make(map[int]int),
		PendingResizes:   make(map[string][2]int),
		Width:            width,
		Height:           height,
		AutoTiling:       true,
		UseBSPLayout:     true,
		FocusedWindow:    0,
	}
	for i := range windows {
		m.Windows = append(m.Windows, newDeferralWindow(t, fmt.Sprintf("win-%036d", i+1)))
	}
	m.TileAllWindows()
	return m
}

// newDeferralWindow builds a window with a live emulator. A bare
// terminal.Window is not enough here: Window.Resize returns early when Terminal
// is nil, so the real (non-deferred) layout path would leave such a window at
// its construction size and the test could not tell the two branches apart.
func newDeferralWindow(t *testing.T, id string) *terminal.Window {
	t.Helper()
	ptyDataChan := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ptyDataChan:
			case <-done:
				return
			}
		}
	}()
	t.Cleanup(func() { close(done) })

	win := terminal.NewDaemonWindow(id, "test", 0, 0, 10, 5, 0, "pty-"+id, ptyDataChan)
	if win == nil {
		t.Fatal("NewDaemonWindow returned nil")
	}
	win.Workspace = 1
	t.Cleanup(func() { win.Close() })
	return win
}

// tiledRects returns the geometry of the panes that should be partitioning the
// screen right now.
func tiledRects(m *OS) []*terminal.Window {
	var out []*terminal.Window
	for _, w := range m.Windows {
		if w.Workspace == m.CurrentWorkspace && !w.Minimized && !w.IsFloating {
			out = append(out, w)
		}
	}
	return out
}

func assertNoOverlap(t *testing.T, m *OS, when string) {
	t.Helper()
	wins := tiledRects(m)
	for i := range wins {
		for j := i + 1; j < len(wins); j++ {
			a, b := wins[i], wins[j]
			ox := min(a.X+a.Width, b.X+b.Width) - max(a.X, b.X)
			oy := min(a.Y+a.Height, b.Y+b.Height) - max(a.Y, b.Y)
			if ox > 0 && oy > 0 {
				t.Errorf("%s: %s (%d,%d %dx%d) overlaps %s (%d,%d %dx%d) by %dx%d",
					when, a.ID, a.X, a.Y, a.Width, a.Height,
					b.ID, b.X, b.Y, b.Width, b.Height, ox, oy)
			}
		}
	}
}

// TestDroppedSettleDoesNotWedgeTheLayout is the regression test for the
// reported defect: tiled mode showing panes at stale, overlapping positions.
//
// A terminal resize sets viewportResizing and arms a settle to clear it. If
// that settle never arrives - Update recovers a panic and returns a nil
// command, taking the settle with it, or the message is otherwise lost - the
// flag used to stay set for the rest of the session. Every later retile then
// took the "place directly, resize visually only" branch and recorded its work
// in PendingResizes for a drain that would never happen, so no pane ever got a
// real size again.
func TestDroppedSettleDoesNotWedgeTheLayout(t *testing.T) {
	m := newDeferralOS(t, 120, 40, 2)

	// A terminal resize arrives and its settle is dropped on the floor.
	m.Width, m.Height = 100, 30
	m.viewportResizing = true
	m.viewportResizeGen++
	m.noteResizeStep(time.Now())
	m.TileAllWindows()

	if !m.viewportResizing {
		t.Fatal("deferral should be live immediately after a resize")
	}

	// Nothing clears it. Time passes, as it does when the settle never comes.
	m.noteResizeStep(time.Now().Add(-2 * resizeDeferralTimeout))
	m.lastPointerAt = time.Now().Add(-2 * resizeDeferralTimeout)

	// The next thing the user does is open a window.
	w := newDeferralWindow(t, "win-00000000000000000000000000000new")
	m.Windows = append(m.Windows, w)
	m.AddWindowToBSPTree(w)

	if m.viewportResizing {
		t.Error("viewportResizing still set after a structural layout change")
	}
	if len(m.PendingResizes) != 0 {
		t.Errorf("PendingResizes still holds %d entries; the deferred work was never drained", len(m.PendingResizes))
	}
	assertNoOverlap(t, m, "after opening a window on a wedged deferral")

	// And the panes really cover the screen, rather than sitting wherever the
	// pre-resize layout had left them.
	for _, win := range tiledRects(m) {
		if win.Width <= 0 || win.Height <= 0 {
			t.Errorf("%s has degenerate size %dx%d", win.ID, win.Width, win.Height)
		}
		if win.X+win.Width > m.GetRenderWidth()+1 {
			t.Errorf("%s (x=%d w=%d) overruns the %d-column viewport", win.ID, win.X, win.Width, m.GetRenderWidth())
		}
	}
}

// TestResizeDeferralExpires pins the timeout itself: a deferral whose last
// resize event is older than resizeDeferralTimeout is not live, and asking
// drains what it was holding.
func TestResizeDeferralExpires(t *testing.T) {
	m := newDeferralOS(t, 120, 40, 2)

	m.viewportResizing = true
	m.noteResizeStep(time.Now())
	if !m.resizeDeferralActive() {
		t.Fatal("a deferral with a fresh resize step should be active")
	}

	m.PendingResizes["win-"+"000000000000000000000000000000001"] = [2]int{40, 20}
	m.noteResizeStep(time.Now().Add(-resizeDeferralTimeout - time.Millisecond))
	if m.resizeDeferralActive() {
		t.Error("a deferral whose last resize step is older than the timeout should be dead")
	}
	if m.viewportResizing {
		t.Error("expiring the deferral should have cleared viewportResizing")
	}
	if len(m.PendingResizes) != 0 {
		t.Error("expiring the deferral should have drained PendingResizes")
	}
}

// TestLostMouseReleaseDoesNotFreezePaneContent covers the other half of the
// same class: a drag whose release is lost (a browser pointer that left the
// tab) leaves IsBeingManipulated set, and that flag makes a pane render its
// cached frame forever.
func TestLostMouseReleaseDoesNotFreezePaneContent(t *testing.T) {
	m := newDeferralOS(t, 120, 40, 2)

	m.Dragging = true
	m.InteractionMode = true
	m.Windows[0].IsBeingManipulated = true

	// While the gesture is live the flag is left alone.
	m.clearStaleManipulation()
	if !m.Windows[0].IsBeingManipulated {
		t.Fatal("manipulation flag cleared while a drag was still in progress")
	}

	// The release never arrives, but the input layer eventually notices the
	// pointer is gone and the drag ends. Nothing clears the per-window flag.
	m.Dragging = false
	m.InteractionMode = false

	m.clearStaleManipulation()
	if m.Windows[0].IsBeingManipulated {
		t.Error("IsBeingManipulated still set with no drag or resize in progress")
	}
	if !m.Windows[0].ContentDirty {
		t.Error("the pane was not marked dirty, so it would keep serving its cached frame")
	}
}

// TestSettleClearsDeferralRegardlessOfFlagState guards the handler itself: the
// settle for the newest resize generation ends the deferral whatever state the
// bookkeeping flag happens to be in, and a settle from a superseded generation
// leaves the live one alone.
func TestSettleClearsDeferralRegardlessOfFlagState(t *testing.T) {
	m := newDeferralOS(t, 120, 40, 2)
	m.viewportResizeGen = 7
	m.viewportResizing = true
	m.noteResizeStep(time.Now())
	m.PendingResizes["win-"+"000000000000000000000000000000001"] = [2]int{40, 20}

	// A stale settle must not end a storm that is still in progress.
	_, _ = m.Update(ViewportResizeSettledMsg{Gen: 6})
	if !m.viewportResizing {
		t.Error("a superseded settle ended the live deferral")
	}
	if len(m.PendingResizes) == 0 {
		t.Error("a superseded settle drained the deferred work early")
	}

	// The current one always does.
	_, _ = m.Update(ViewportResizeSettledMsg{Gen: 7})
	if m.viewportResizing {
		t.Error("the current settle did not clear viewportResizing")
	}
	if len(m.PendingResizes) != 0 {
		t.Error("the current settle did not drain PendingResizes")
	}
}

// TestWindowSizeMsgAlwaysArmsAndTimestampsTheDeferral makes sure the resize
// handler records the timestamp the expiry depends on. Without it the deferral
// would be judged stale on the very next retile and the coalescing this whole
// mechanism exists for would be gone.
func TestWindowSizeMsgAlwaysArmsAndTimestampsTheDeferral(t *testing.T) {
	m := newDeferralOS(t, 120, 40, 2)

	before := time.Now()
	_, cmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if cmd == nil {
		t.Fatal("a resize must arm a settle")
	}
	if !m.viewportResizing {
		t.Fatal("a resize must start the deferral")
	}
	if m.viewportResizeAt.Before(before) {
		t.Fatal("a resize must record when it arrived")
	}
	if !m.resizeDeferralActive() {
		t.Fatal("the deferral should be live right after a resize")
	}
}
