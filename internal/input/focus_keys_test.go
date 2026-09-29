package input

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// Issue #231: h and l moved focus between tiled panes in window mode, and j
// and k did nothing, because nothing bound them. Every pane above or below
// the focused one was out of reach from the home row.
//
// NEGATIVE CONTROLS, each run by mutating the shipped code:
//
//   - focus_down and focus_up dropped from the default layout keybinds: every
//     j and k row of TestWindowModeHJKLReachEveryPane and
//     TestJKInAScrollingColumn fails saying focus stayed put.
//   - handleTerminalFocusDirection answering only left and right in the
//     scrolling layout, which is what it did before: both rows of
//     TestJKInAScrollingColumn and TestAltUpDownInAScrollingColumn fail.

// spiral is four panes laid out the way the BSP tiler lays out four windows
// on a 160x46 screen.
//
//	+--------+--------+
//	|        |   1    |
//	|   0    +---+----+
//	|        | 2 | 3  |
//	+--------+---+----+
func spiral(t *testing.T, tiled bool) *app.OS {
	t.Helper()
	cfg := config.DefaultConfig()
	o := app.NewOS(app.OSOptions{UserConfig: cfg, KeybindRegistry: config.NewKeybindRegistry(cfg)})
	o.Width, o.Height = 160, 48
	o.EffectiveWidth, o.EffectiveHeight = 160, 48
	o.Mode = app.WindowManagementMode
	o.CurrentWorkspace = 1
	o.Windows = []*terminal.Window{
		{ID: "pane-0", X: 0, Y: 0, Width: 80, Height: 46, Workspace: 1},
		{ID: "pane-1", X: 80, Y: 0, Width: 80, Height: 23, Workspace: 1},
		{ID: "pane-2", X: 80, Y: 23, Width: 40, Height: 23, Workspace: 1},
		{ID: "pane-3", X: 120, Y: 23, Width: 40, Height: 23, Workspace: 1},
	}
	o.FocusedWindow = 0
	o.AutoTiling = tiled
	return o
}

func TestWindowModeHJKLReachEveryPane(t *testing.T) {
	steps := []struct {
		from int
		key  string
		want int
	}{
		{0, "l", 1},
		{1, "j", 2},
		{2, "l", 3},
		{3, "k", 1},
		{1, "h", 0},
		{3, "h", 2},
		{2, "k", 1},
		{2, "h", 0},
		// Edges stay put.
		{0, "j", 0},
		{0, "k", 0},
		{1, "k", 1},
		{3, "j", 3},
	}
	for _, tiled := range []bool{true, false} {
		for _, s := range steps {
			if !tiled && (s.key == "h" || s.key == "l") {
				continue // with tiling off h and l snap
			}
			o := spiral(t, tiled)
			o.FocusedWindow = s.from
			pressWM(o, s.key)
			if o.FocusedWindow != s.want {
				t.Errorf("tiled=%v: %s from pane %d focused pane %d, want %d",
					tiled, s.key, s.from, o.FocusedWindow, s.want)
			}
		}
	}
}

// scrollingColumn is two windows stacked in one column of the scrolling
// layout, the top one focused.
func scrollingColumn(t *testing.T) *app.OS {
	t.Helper()
	o := sideBySide(t, true)
	o.ApplyLayoutModeName(config.LayoutModeScrolling)
	o.TileAllWindows()
	o.CompleteAllAnimations()
	o.FocusWindow(0)
	o.ScrollingConsumeWindow()
	o.TileAllWindows()
	o.CompleteAllAnimations()
	top, bottom := o.Windows[0], o.Windows[1]
	if top.X != bottom.X || bottom.Y <= top.Y {
		t.Fatalf("the windows are not stacked in one column: (%d,%d) and (%d,%d)",
			top.X, top.Y, bottom.X, bottom.Y)
	}
	o.FocusWindow(0)
	return o
}

func TestJKInAScrollingColumn(t *testing.T) {
	o := scrollingColumn(t)
	pressWM(o, "j")
	if o.FocusedWindow != 1 {
		t.Fatalf("j focused window %d, want the lower window 1", o.FocusedWindow)
	}
	sl := o.GetOrCreateScrollingLayout()
	if col := sl.Columns[sl.FocusedCol]; col.WindowIDs[col.Active] != o.GetWindowIntID(o.Windows[1].ID) {
		t.Fatalf("the column did not follow focus to the lower window: %+v", col)
	}
	pressWM(o, "k")
	if o.FocusedWindow != 0 {
		t.Fatalf("k focused window %d, want the upper window 0", o.FocusedWindow)
	}
}

func TestAltUpDownInAScrollingColumn(t *testing.T) {
	o := scrollingColumn(t)
	o.Mode = app.TerminalMode
	o, _ = HandleKeyPress(altArrow("down"), o)
	if o.FocusedWindow != 1 {
		t.Fatalf("alt+down focused window %d, want the lower window 1", o.FocusedWindow)
	}
	o, _ = HandleKeyPress(altArrow("up"), o)
	if o.FocusedWindow != 0 {
		t.Fatalf("alt+up focused window %d, want the upper window 0", o.FocusedWindow)
	}
}
