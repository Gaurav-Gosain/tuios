package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestFullscreenFastPathYieldsToShowkeys pins the fix for the keycast lag: the
// fullscreen fast path (buildFullscreenFrame) bypasses the compositor and so
// never draws the showkeys overlay, which is a compositor layer. A lone
// fullscreen window is the common terminal-mode case, so if the fast path stays
// eligible while the overlay has keys, a captured keypress is never rendered
// until an unrelated redraw disqualifies the fast path.
//
// The test builds exactly the geometry fullscreenFastWindow accepts, confirms it
// is eligible with an empty history (nothing to draw, keep the optimization),
// then shows that a single captured keypress makes it ineligible so the frame
// falls through to GetCanvas -> renderOverlays and the keycast is drawn.
func TestFullscreenFastPathYieldsToShowkeys(t *testing.T) {
	// Fix the render-affecting globals so the eligibility check is deterministic
	// regardless of what an earlier test or config load left behind.
	defer withConfig(&config.DockbarPosition, "bottom")()
	defer withConfig(&config.ShowClock, false)()
	defer withConfig(&config.SharedBorders, false)()

	win := newTestWindow(t, "showkeys-fast-0001", 80, 23)
	m := newTestOS(win)
	m.Width, m.Height = 80, 24

	// Place the window so it exactly fills the content area above the dock, which
	// is what fullscreenFastWindow requires.
	win.X = 0
	win.Y = m.GetTopMargin()
	win.Width = m.GetRenderWidth()
	win.Height = m.GetUsableHeight()

	// Baseline: with no keys to draw the fast path stays eligible. If this fails
	// the geometry setup is wrong and the rest of the test would be meaningless.
	if _, ok := m.fullscreenFastWindow(); !ok {
		t.Fatalf("setup: fullscreen window should be fast-path eligible with no overlay keys")
	}

	// The overlay is enabled and a real key is captured, exactly as HandleKeyPress
	// does at the top of the input path.
	m.ShowKeys = true
	m.KeyHistoryMaxSize = 10
	m.CaptureKeyEvent(tea.KeyPressMsg{Code: 'Q', Text: "Q"})
	if len(m.RecentKeys) == 0 {
		t.Fatalf("setup: CaptureKeyEvent did not record the keypress")
	}

	// With a key to show, the fast path must yield so the compositor draws the
	// keycast. This is the whole fix.
	if _, ok := m.fullscreenFastWindow(); ok {
		t.Fatalf("fullscreen fast path stayed eligible while the showkeys overlay had keys; the keycast would not be drawn")
	}
}

// withConfig sets a package-level config global for the duration of a test and
// returns a restore func to defer. Keeps the eligibility check independent of
// ambient config state.
func withConfig[T any](p *T, v T) func() {
	old := *p
	*p = v
	return func() { *p = old }
}
