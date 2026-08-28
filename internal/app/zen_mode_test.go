package app

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
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

// Disabled is the default and the historic behaviour: every window keeps its
// border regardless of focus or pointer activity.
func TestZenBordersHiddenDisabled(t *testing.T) {
	withZenMode(t)
	config.Global.ZenMode = config.ZenModeDisabled

	m := &OS{Settings: config.Global}
	if m.zenBordersHidden(false) {
		t.Error("zenBordersHidden(false) = true with zen_mode disabled, want false")
	}
	if m.zenBordersHidden(true) {
		t.Error("zenBordersHidden(true) = true with zen_mode disabled, want false")
	}
}

// Always hides every unfocused border but keeps the focused window's frame, so
// the user always retains an anchor for where their keystrokes land.
func TestZenBordersHiddenAlways(t *testing.T) {
	withZenMode(t)
	config.Global.ZenMode = config.ZenModeAlways

	m := &OS{Settings: config.Global}
	if !m.zenBordersHidden(false) {
		t.Error("zenBordersHidden(false) = false with zen_mode always, want true")
	}
	if m.zenBordersHidden(true) {
		t.Error("zenBordersHidden(true) = true with zen_mode always, want false")
	}
}

// Mouse reveals every border while the pointer is moving and hides the
// unfocused ones once it sits still past the reveal window.
func TestZenBordersHiddenMouse(t *testing.T) {
	withZenMode(t)
	config.Global.ZenMode = config.ZenModeMouse

	m := &OS{Settings: config.Global}

	// No pointer event ever: treated as idle, so unfocused borders hide.
	if !m.zenBordersHidden(false) {
		t.Error("zenBordersHidden(false) = false with no pointer activity, want true")
	}

	// A recent motion reveals every border, focused or not.
	m.lastPointerAt = time.Now()
	if m.zenBordersHidden(false) {
		t.Error("zenBordersHidden(false) = true with a recent motion, want false")
	}
	if m.zenBordersHidden(true) {
		t.Error("zenBordersHidden(true) = true with a recent motion, want false")
	}

	// A pointer event older than the reveal window is idle again: unfocused
	// borders melt away, the focused one stays.
	m.lastPointerAt = time.Now().Add(-(zenModeMouseIdleTimeout + time.Second))
	if !m.zenBordersHidden(false) {
		t.Error("zenBordersHidden(false) = false after the idle window, want true")
	}
	if m.zenBordersHidden(true) {
		t.Error("zenBordersHidden(true) = true after the idle window, want false")
	}
}

// pointerRecentlyMoved answers strictly inside the reveal window.
func TestPointerRecentlyMoved(t *testing.T) {
	m := &OS{Settings: config.Global}

	if m.pointerRecentlyMoved() {
		t.Error("pointerRecentlyMoved() = true with no pointer event, want false")
	}

	m.lastPointerAt = time.Now()
	if !m.pointerRecentlyMoved() {
		t.Error("pointerRecentlyMoved() = false with a fresh event, want true")
	}

	m.lastPointerAt = time.Now().Add(-(zenModeMouseIdleTimeout + time.Millisecond))
	if m.pointerRecentlyMoved() {
		t.Error("pointerRecentlyMoved() = true past the idle window, want false")
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

// TestZenRenderWindowBoxKeepsContentPlacement pins the no-jump half of the
// zen contract: hiding a window's border must not move its content. A window
// that owns its border draws content at Width-2 by Height-2 placed at the
// window origin, so returning the bare content would jump the text one cell
// up-left when the border melts and back when it returns. renderWindowBoxZen
// keeps the frame cells reserved (blank border + blank title row), so the
// content keeps its exact position.
func TestZenRenderWindowBoxKeepsContentPlacement(t *testing.T) {
	withZenMode(t)
	config.Global.ZenMode = config.ZenModeDisabled

	win := newTestWindow(t, "zen-layout-0001", 40, 12)
	m := &OS{Settings: config.Global, Windows: []*terminal.Window{win}, FocusedWindow: 0}

	// Paint some content into the emulator so there is something to compare.
	win.LockIO()
	_, _ = win.Terminal.Write([]byte("HELLO-ZEN"))
	win.UnlockIO()

	// Same unfocused window, same emulator state: only the zen policy flips.
	// That is the exact transition the melt performs, so the content must
	// land on the same cell in both frames.
	bordered := m.renderWindowBox(win, 0, false, lipgloss.Color("1"))
	m.Settings.ZenMode = config.ZenModeAlways
	zen := m.renderWindowBox(win, 0, false, lipgloss.Color("1"))

	// Compare the visual cells, not the raw bytes: the bordered frame carries
	// ANSI color codes on its frame glyphs while the zen blank frame is plain
	// spaces, so a byte-offset search would read the escape sequences as
	// columns and report a shift that is not on the screen.
	bLines := strings.Split(stripANSIForTrace(bordered), "\n")
	zLines := strings.Split(stripANSIForTrace(zen), "\n")
	if len(bLines) != len(zLines) {
		t.Fatalf("zen frame height %d != bordered frame height %d; content would shift vertically",
			len(zLines), len(bLines))
	}
	zWidth := lipgloss.Width(zen)
	if zWidth != win.Width {
		t.Fatalf("zen frame width %d != window width %d; content would shift horizontally",
			zWidth, win.Width)
	}

	// Find where the content lands in both frames and require the same cell.
	bx, by := findInFrame(bLines, "HELLO-ZEN")
	zx, zy := findInFrame(zLines, "HELLO-ZEN")
	if bx != zx || by != zy {
		t.Fatalf("content moved: bordered at (%d,%d), zen at (%d,%d); the melt must not shift the layout",
			bx, by, zx, zy)
	}
}

// findInFrame returns the visual (x, y) of the first occurrence of needle in
// the frame, or (-1,-1) if absent. It measures the column with lipgloss.Width
// rather than a byte offset, because the bordered frame's left glyph (│) is a
// multi-byte UTF-8 rune: a byte index would count it as 3 columns and report a
// shift that is not on the screen.
func findInFrame(lines []string, needle string) (int, int) {
	for y, line := range lines {
		if idx := strings.Index(line, needle); idx >= 0 {
			return lipgloss.Width(line[:idx]), y
		}
	}
	return -1, -1
}
