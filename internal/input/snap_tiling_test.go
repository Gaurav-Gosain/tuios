package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// h and l are snap_left and snap_right in window-management mode. With tiling
// off they snap the focused window to a half of the screen. With tiling on
// they used to do nothing at all, silently, and tiling is the default: the two
// most reachable letters on the home row were dead keys for most users.
//
// Under tiling they move focus to the neighbour in that direction, which
// completes the row the other two vim pairs already have: H and L swap that
// way, alt+h and alt+l preselect there.
//
// NEGATIVE CONTROLS, each run by mutating the shipped code and watching the
// named row fail:
//
//   - snapOrFocus returning before SnapByDirection with tiling on, which is
//     the handler before this change: both tiled rows of
//     TestSnapKeysFocusANeighbourUnderTiling fail saying focus stayed on the
//     pane it started on.
//   - SnapByDirection erroring under tiling for every direction, which is the
//     executor before this change: the same two rows fail the same way, and
//     TestRunCommandSnapLeftFocusesUnderTiling fails on the error.
//
// One control turned out invalid and is recorded rather than claimed:
// focusTiledNeighbour asking the geometry in the scrolling layout fails
// nothing here, because FocusWindow already brings the strip's focused column
// along with it. The scrolling row pins that the strip follows, not which
// road got it there.

// sideBySide is two windows across a 100x30 screen, the left one focused.
func sideBySide(t *testing.T, tiled bool) *app.OS {
	t.Helper()
	cfg := config.DefaultConfig()
	o := app.NewOS(app.OSOptions{UserConfig: cfg, KeybindRegistry: config.NewKeybindRegistry(cfg)})
	o.Width, o.Height = 100, 30
	o.EffectiveWidth, o.EffectiveHeight = 100, 30
	o.Mode = app.WindowManagementMode
	o.Windows = []*terminal.Window{
		{ID: "left-window-0001", X: 0, Y: 0, Width: 50, Height: 28, Workspace: 1},
		{ID: "right-window-001", X: 50, Y: 0, Width: 50, Height: 28, Workspace: 1},
	}
	o.CurrentWorkspace, o.FocusedWindow = 1, 0
	o.AutoTiling = tiled
	return o
}

func pressWM(o *app.OS, key string) {
	HandleWindowManagementModeKey(tea.KeyPressMsg{Code: rune(key[0]), Text: key}, o)
}

func TestSnapKeysFocusANeighbourUnderTiling(t *testing.T) {
	t.Run("l focuses the pane to the right", func(t *testing.T) {
		o := sideBySide(t, true)
		pressWM(o, "l")
		if o.FocusedWindow != 1 {
			t.Fatalf("focus is on window %d after l, want 1", o.FocusedWindow)
		}
		pressWM(o, "h")
		if o.FocusedWindow != 0 {
			t.Fatalf("focus is on window %d after h, want 0", o.FocusedWindow)
		}
	})
	t.Run("a step with no neighbour does nothing", func(t *testing.T) {
		o := sideBySide(t, true)
		pressWM(o, "h")
		if o.FocusedWindow != 0 {
			t.Fatalf("focus is on window %d after h at the left edge, want 0", o.FocusedWindow)
		}
	})
	t.Run("in the scrolling layout the strip steps", func(t *testing.T) {
		o := sideBySide(t, true)
		o.ApplyLayoutModeName(config.LayoutModeScrolling)
		o.TileAllWindows()
		o.CompleteAllAnimations()
		pressWM(o, "l")
		if o.FocusedWindow != 1 {
			t.Fatalf("focus is on window %d after l, want 1", o.FocusedWindow)
		}
		if sl := o.GetOrCreateScrollingLayout(); sl.FocusedCol != 1 {
			t.Fatalf("the strip's focused column is %d after l, want 1", sl.FocusedCol)
		}
	})
	t.Run("with tiling off h still snaps", func(t *testing.T) {
		o := sideBySide(t, false)
		o.FocusedWindow = 1
		pressWM(o, "h")
		o.CompleteAllAnimations()
		if o.FocusedWindow != 1 {
			t.Fatalf("focus moved to window %d, want the snap to keep it on 1", o.FocusedWindow)
		}
		if w := o.Windows[1]; w.X != 0 {
			t.Fatalf("the window is at x=%d after h, want it snapped to the left edge", w.X)
		}
	})
}

// TestRunCommandSnapLeftFocusesUnderTiling: the tape command and run-command
// verb behind the same action give the same answer as the key.
func TestRunCommandSnapLeftFocusesUnderTiling(t *testing.T) {
	o := sideBySide(t, true)
	if err := o.SnapByDirection("right"); err != nil {
		t.Fatalf("SnapRight under tiling: %v", err)
	}
	if o.FocusedWindow != 1 {
		t.Fatalf("focus is on window %d after SnapRight, want 1", o.FocusedWindow)
	}
	if err := o.SnapByDirection("fullscreen"); err == nil {
		t.Fatal("SnapFullscreen under tiling did not say why it did nothing")
	}
}
