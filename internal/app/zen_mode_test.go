package app

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// withZenMode saves and restores the zen-mode global, which is package state
// shared with every other test in the run.
func withZenMode(t *testing.T) {
	t.Helper()
	prev := config.Global.ZenMode
	t.Cleanup(func() { config.Global.ZenMode = prev })
}

// TestZenMouseTickMeltsAndConverges checks that once the melt frame is
// composed the state converges: the tick stops reporting work (so it drops
// back to the slow idle rate instead of spinning at 10fps).
func TestZenMouseTickMeltsAndConverges(t *testing.T) {
	withZenMode(t)
	config.Global.ZenMode = config.ZenModeMouse

	m := &OS{Settings: config.Global}
	m.zenHidden = false
	m.lastPointerAt = time.Now().Add(-(zenModeMouseIdleTimeout + time.Second))

	// Crossing: work due.
	if !m.tickNeedsWork() {
		t.Fatal("expected work at the crossing")
	}
	// A frame composed with the pointer idle records zenHidden=true, which is
	// what the tick compares against next time.
	m.zenHidden = m.zenBordersHidden(false)
	if m.zenHidden != true {
		t.Fatalf("zenHidden after idle frame = %v, want true", m.zenHidden)
	}
	if m.tickNeedsWork() {
		t.Fatal("tickNeedsWork = true after the melt converged; the tick would spin at 10fps forever")
	}
}

// TestZenMouseTickForcesRenderOnCrossing pins the melt half of the mouse-mode
// contract: the idle crossing has no event of its own (the pointer just stops
// moving), so the maintenance tick that lands on the far side of the reveal
// window must both detect the crossing (tickNeedsWork) and force a real frame
// (needsRender), or renderSkipped stays set, View() serves the cached frame,
// zenHidden never converges, and the tick does work at 10fps forever with
// nothing visible to show for it.
func TestZenMouseTickForcesRenderOnCrossing(t *testing.T) {
	withZenMode(t)
	config.Global.ZenMode = config.ZenModeMouse

	win := newTestWindow(t, "zen-tick-0001", 60, 34)
	m := newTestOS(win)
	m.Width, m.Height = 120, 40

	// The last composed frame had the borders visible (zenHidden=false) and
	// the pointer has sat still past the reveal window, so the melt is due.
	m.zenHidden = false
	m.lastPointerAt = time.Now().Add(-(zenModeMouseIdleTimeout + time.Second))

	if !m.tickNeedsWork() {
		t.Fatal("tickNeedsWork = false at the idle crossing; the melt would never be scheduled")
	}

	// A tick that crosses the threshold must draw, not skip. Before the fix
	// needsRender had no zen term, so this tick set renderSkipped and the
	// melt never appeared.
	if _, _ = m.Update(TickerMsg(time.Now())); m.renderSkipped {
		t.Fatal("the tick that crossed the zen idle threshold skipped the frame; the melt would never be drawn")
	}
}

// TestZenPointerRevealMarksDirty pins the reveal half: a pointer event that
// re-opens the reveal window must mark the affected windows dirty, because
// each window's CachedLayer still holds the borderless render and is reused
// until the window is dirty. Without this the borders would never come back.
func TestZenPointerRevealMarksDirty(t *testing.T) {
	withZenMode(t)
	config.Global.ZenMode = config.ZenModeMouse

	win := newTestWindow(t, "zen-reveal-0001", 60, 34)
	win2 := newTestWindow(t, "zen-reveal-0002", 60, 34)
	m := &OS{
		Settings:      config.Global,
		Windows:       []*terminal.Window{win, win2},
		FocusedWindow: 0,
	}

	// The melt already happened: the composed frame has the borders hidden.
	m.zenHidden = true
	m.lastPointerAt = time.Now().Add(-(zenModeMouseIdleTimeout + time.Second))

	// Clear dirty flags so we can see who the reveal marks.
	win.ClearDirtyFlags()
	win2.ClearDirtyFlags()

	m.notePointerEvent(time.Now())

	if !win2.Dirty {
		t.Error("the unfocused window was not marked dirty by the reveal; its cached borderless layer would be reused")
	}
	if win.Dirty {
		t.Error("the focused window should keep its border and not need a zen repaint")
	}
	if !m.pointerRecentlyMoved() {
		t.Error("pointerRecentlyMoved = false after notePointerEvent(now)")
	}
}
