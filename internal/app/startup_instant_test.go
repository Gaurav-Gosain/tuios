package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The [startup] layout is placed at once, not slid into. A pane sliding into
// its tile keeps its old size until the slide ends, and a command run in
// that time reads the old size.
func TestStartupTilingDoesNotAnimate(t *testing.T) {
	w := newTestWindow(t, "first", 40, 12)
	m := newTestOS(w)
	w.Workspace = 1
	m.CurrentWorkspace = 1
	m.Width, m.Height = 100, 30
	m.UserConfig = config.DefaultConfig()
	m.UserConfig.Startup.Tiled = true
	if m.Settings.GetAnimationDuration() <= 0 {
		t.Skip("the test settings have no animation to leave out")
	}
	m.applyStartupTiling()
	if !m.AutoTiling {
		t.Fatal("startup tiling did not turn tiling on")
	}
	if len(m.Animations) != 0 {
		t.Fatalf("startup tiling started %d animations", len(m.Animations))
	}
	if w.Width <= 40 {
		t.Fatalf("the pane kept its old width %d", w.Width)
	}
}
