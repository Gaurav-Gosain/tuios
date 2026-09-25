package app

import (
	"strings"
	"testing"
)

// TestScreensaverStartsOverTheRealComposedScreen is the only end-to-end cover
// of the start path, and it exists because every failure in that path is
// silent: startScreensaver returns false and the saver simply never appears,
// with nothing said about why. A nil capture, an effect that will not build, a
// canvas that disagrees with the grid, all look identical from outside.
//
// It also pins the thing the canvas sizing was fixed for: the composed grid and
// the render size must agree, and the effect must be built over the grid's own
// dimensions.
//
// Negative control: making composedGrid return nil, or building the canvas at a
// size the capture does not have, fails this.
func TestScreensaverStartsOverTheRealComposedScreen(t *testing.T) {
	const cols, rows = 60, 18
	win := newTestWindow(t, "probe-0001", cols, rows)
	m := newTestOS(win)
	m.Width, m.Height = cols, rows
	m.EffectiveWidth, m.EffectiveHeight = cols, rows
	m.UserConfig = enabledScreensaverConfig(t, 10)

	win.LockIO()
	_, _ = win.Terminal.Write([]byte("\x1b[32mhello from a pane\x1b[0m\r\n$ "))
	win.UnlockIO()
	win.MarkContentDirty()

	grid := m.composedGrid(0, 0, m.GetRenderWidth(), m.GetRenderHeight())
	if grid == nil {
		t.Fatal("the composed screen came back empty, so the saver has nothing to animate")
	}
	if grid.Cols != cols || grid.Rows != rows {
		t.Fatalf("the composed grid is %dx%d, want the render size %dx%d",
			grid.Cols, grid.Rows, cols, rows)
	}

	if !m.startScreensaver(m.UserConfig.Screensaver) {
		t.Fatal("the saver refused to start over a real screen")
	}
	if !m.screensaver.active {
		t.Error("the saver started but does not report itself active")
	}
	if m.screensaver.name == "" {
		t.Error("the saver started without naming its effect")
	}
	if m.screensaver.canvasWidth != cols || m.screensaver.canvasHeight != rows {
		t.Errorf("the effect was built on a %dx%d canvas, want the capture's %dx%d",
			m.screensaver.canvasWidth, m.screensaver.canvasHeight, cols, rows)
	}

	// The frame must cover the screen. A short one lets panes show through the
	// bottom of the animation.
	lines := strings.Split(m.screensaver.frame, "\n")
	if len(lines) != rows {
		t.Errorf("the frame is %d rows, want %d", len(lines), rows)
	}

	// And the saver draws alone: everything under it is hidden anyway, and a
	// panel drawn over an animation of that same panel only looks like part of
	// the animation.
	layers := m.renderOverlays()
	if len(layers) != 1 {
		t.Errorf("renderOverlays returned %d layers while the saver was up, want 1", len(layers))
	}

	// A pane filling the screen must not take the fullscreen fast path, which
	// skips overlays entirely and would never draw the saver at all. The
	// geometry has to be set up for the fast path to be eligible in the first
	// place, or this asserts nothing.
	win.X, win.Y = 0, m.GetTopMargin()
	win.Width, win.Height = m.GetRenderWidth(), m.GetUsableHeight()
	m.stopScreensaver()
	if _, fast := m.fullscreenFastWindow(); !fast {
		t.Fatal("setup: the fast path is not eligible even with the saver down, so the check below proves nothing")
	}
	m.screensaver.active = true
	if _, fast := m.fullscreenFastWindow(); fast {
		t.Error("the fullscreen fast path is still eligible while the saver is up")
	}
}
