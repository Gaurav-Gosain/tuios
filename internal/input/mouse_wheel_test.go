package input

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// windowWithScrollback builds a window whose emulator has more history than
// fits on screen, without spawning a PTY.
func windowWithScrollback(t *testing.T) *terminal.Window {
	t.Helper()
	em := vt.NewEmulator(20, 5)
	t.Cleanup(func() { _ = em.Close() })
	for i := range 40 {
		_, _ = em.Write(fmt.Appendf(nil, "line %d\r\n", i))
	}
	if em.ScrollbackLen() == 0 {
		t.Fatal("emulator produced no scrollback; the test cannot exercise scrolling")
	}
	return &terminal.Window{Terminal: em, Width: 22, Height: 7}
}

// The wheel step used to be hardcoded to 3 lines everywhere, so users on
// high-resolution wheels or large monitors had no way to make scrollback move
// faster. It now follows appearance.scroll_lines.
func TestMouseWheelUsesConfiguredScrollLines(t *testing.T) {
	prev := config.Global.ScrollLines
	t.Cleanup(func() { config.Global.ScrollLines = prev })

	for _, step := range []int{1, 3, 10} {
		t.Run(fmt.Sprintf("step %d", step), func(t *testing.T) {
			config.Global.ScrollLines = step
			win := windowWithScrollback(t)
			o := &app.OS{
				Settings:      config.Global,
				Mode:          app.TerminalMode,
				FocusedWindow: 0,
				Windows:       []*terminal.Window{win},
			}

			handleMouseWheel(tea.MouseWheelMsg{Button: tea.MouseWheelUp}, o)
			if win.ScrollbackOffset != step {
				t.Fatalf("after one wheel-up ScrollbackOffset = %d, want %d", win.ScrollbackOffset, step)
			}

			handleMouseWheel(tea.MouseWheelMsg{Button: tea.MouseWheelDown}, o)
			if win.ScrollbackOffset != 0 {
				t.Errorf("after wheel-down ScrollbackOffset = %d, want 0", win.ScrollbackOffset)
			}
		})
	}
}

// Scrolling must still stop at the ends of the buffer whatever the step is.
func TestMouseWheelClampsAtScrollbackBounds(t *testing.T) {
	prev := config.Global.ScrollLines
	t.Cleanup(func() { config.Global.ScrollLines = prev })
	config.Global.ScrollLines = 50

	win := windowWithScrollback(t)
	o := &app.OS{
		Settings:      config.Global,
		Mode:          app.TerminalMode,
		FocusedWindow: 0,
		Windows:       []*terminal.Window{win},
	}

	limit := win.ScrollbackLen()
	for range 5 {
		handleMouseWheel(tea.MouseWheelMsg{Button: tea.MouseWheelUp}, o)
	}
	if win.ScrollbackOffset != limit {
		t.Errorf("ScrollbackOffset = %d, want it clamped to scrollback length %d", win.ScrollbackOffset, limit)
	}

	for range 5 {
		handleMouseWheel(tea.MouseWheelMsg{Button: tea.MouseWheelDown}, o)
	}
	if win.ScrollbackOffset != 0 {
		t.Errorf("ScrollbackOffset = %d, want 0", win.ScrollbackOffset)
	}
}

// A trackpad reports a little sideways drift on almost every vertical scroll,
// and the terminal forwards that as a left or right wheel button. The
// scrolling layout used to answer those on their own, so scrolling back
// through a pane walked the whole strip sideways. The horizontal wheel now
// moves the viewport under the same modifier the vertical one needs.
//
// The strip needs more columns than fit, or every scroll clamps to zero and
// the test would pass against code that does nothing.
//
// Control: move the two horizontal cases back outside the modifier check in
// handleMouseWheel, and the unmodified subtest reports the strip moving.
func TestTheHorizontalWheelNeedsTheSameModifierAsTheVertical(t *testing.T) {
	newFleet := func(t *testing.T) *app.OS {
		t.Helper()
		wins := make([]*terminal.Window, 6)
		for i := range wins {
			wins[i] = windowWithScrollback(t)
		}
		o := &app.OS{
			Settings:           config.Global,
			Mode:               app.WindowManagementMode,
			UseScrollingLayout: true,
			AutoTiling:         true,
			FocusedWindow:      0,
			Windows:            wins,
			EffectiveWidth:     40,
			EffectiveHeight:    20,
		}
		o.TileAllWindows()
		return o
	}

	// Right from home has room whenever the strip is wider than the view.
	if sl := newFleet(t).GetOrCreateScrollingLayout(); sl == nil {
		t.Fatal("no scrolling layout")
	}

	for _, tc := range []struct {
		name  string
		mod   tea.KeyMod
		moves bool
	}{
		{"unmodified", 0, false},
		{"alt", tea.ModAlt, true},
		{"shift", tea.ModShift, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := newFleet(t)
			before := o.GetOrCreateScrollingLayout().ViewportX
			_, _ = handleMouseWheel(tea.MouseWheelMsg{Button: tea.MouseWheelRight, Mod: tc.mod}, o)
			after := o.GetOrCreateScrollingLayout().ViewportX
			if (after != before) != tc.moves {
				t.Fatalf("wheel right with mod %v: viewport %d -> %d, moved=%v, want moved=%v",
					tc.mod, before, after, after != before, tc.moves)
			}
			if !tc.moves {
				return
			}
			// And back again, which only has room because we just moved.
			_, _ = handleMouseWheel(tea.MouseWheelMsg{Button: tea.MouseWheelLeft, Mod: tc.mod}, o)
			if back := o.GetOrCreateScrollingLayout().ViewportX; back != before {
				t.Fatalf("wheel left with mod %v did not undo it: %d -> %d, want %d", tc.mod, after, back, before)
			}
		})
	}
}

// railWheelOS builds a full OS with the rail on and draws one frame, which is
// what places the rail's sections and records the bands the wheel is routed
// against.
func railWheelOS(t *testing.T) *app.OS {
	t.Helper()
	cfg := config.DefaultConfig()
	o := app.NewOS(app.OSOptions{
		UserConfig:      cfg,
		KeybindRegistry: config.NewKeybindRegistry(cfg),
	})
	withSidebarGlobals(t, "left")
	o.Settings = config.Global
	o.Width, o.Height = 160, 40
	o.EffectiveWidth, o.EffectiveHeight = 160, 40
	o.Windows = []*terminal.Window{
		{ID: "a", CustomName: "editor", X: 0, Y: 0, Width: 60, Height: 30, Workspace: 1},
		{ID: "b", CustomName: "logs", X: 0, Y: 0, Width: 60, Height: 30, Workspace: 1},
		{ID: "c", CustomName: "shell", X: 0, Y: 0, Width: 60, Height: 30, Workspace: 1},
	}
	o.CurrentWorkspace, o.FocusedWindow = 1, 0
	_ = o.View()
	if !o.SidebarActive() {
		t.Fatal("the rail is not up, so nothing below tests what it says it tests")
	}
	return o
}

// railScrolls sweeps one wheel button down every row of the rail and reports
// whether any section's offset moved. Sweeping rather than aiming at one band
// keeps the test off the unexported placement table, and the files section is
// covered by the same sweep as the rest.
func railScrolls(o *app.OS, button tea.MouseButton) bool {
	o.SidebarScrollS, o.SidebarScrollT, o.SidebarScrollA, o.SidebarScrollF = 0, 0, 0, 0
	for y := range o.Height {
		_, _ = handleMouseWheel(tea.MouseWheelMsg{X: 1, Y: y, Button: button}, o)
	}
	return o.SidebarScrollS|o.SidebarScrollT|o.SidebarScrollA|o.SidebarScrollF != 0
}

// A trackpad reports a little sideways drift on almost every vertical scroll and
// the terminal forwards it as a left or right wheel button. The rail's sections
// are columns of rows and none of them scrolls sideways, so the drift has to be
// dropped. It used to be read as "not up", which is down: every stray drift
// event jumped the list under the pointer forward by scroll_lines rows, which is
// what made the files section unusable with a trackpad.
//
// Control: route the wheel on msg.Button == tea.MouseWheelUp again in
// handleMouseWheel and the two horizontal subtests report the rail scrolling.
func TestSidewaysDriftDoesNotScrollTheRail(t *testing.T) {
	for _, tc := range []struct {
		name   string
		button tea.MouseButton
		want   bool
	}{
		{"down", tea.MouseWheelDown, true},
		{"up", tea.MouseWheelUp, false}, // already at the top, so nothing moves
		{"left", tea.MouseWheelLeft, false},
		{"right", tea.MouseWheelRight, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := railWheelOS(t)
			if got := railScrolls(o, tc.button); got != tc.want {
				t.Fatalf("a %v sweep over the rail scrolled=%v, want %v (offsets s=%d t=%d a=%d f=%d)",
					tc.button, got, tc.want,
					o.SidebarScrollS, o.SidebarScrollT, o.SidebarScrollA, o.SidebarScrollF)
			}
		})
	}
}

// A trackpad's sideways drift arrives interleaved with the vertical events of
// the same gesture, and the strip is the one place both axes move the same
// thing. Answering both moved the strip back and forth inside one gesture,
// which is the stutter the report was about. The gesture follows the axis it is
// mostly on and the drift on the other one is dropped.
//
// Control: delete the WheelAxisAccepts guard in handleMouseWheel and the strip
// ends up two steps short, because the two drift events walk it back.
func TestSidewaysDriftDoesNotFightTheStrip(t *testing.T) {
	prev := config.Global.NiriScrollCells
	t.Cleanup(func() { config.Global.NiriScrollCells = prev })
	config.Global.NiriScrollCells = config.NiriScrollCellsDefault

	wins := make([]*terminal.Window, 6)
	for i := range wins {
		wins[i] = windowWithScrollback(t)
	}
	o := &app.OS{
		Settings:           config.Global,
		Mode:               app.WindowManagementMode,
		UseScrollingLayout: true,
		AutoTiling:         true,
		FocusedWindow:      0,
		Windows:            wins,
		EffectiveWidth:     40,
		EffectiveHeight:    20,
	}
	o.TileAllWindows()

	// Six events down the strip with two drift events through them, and the
	// drift on one side at a time: a left and a right in the same gesture cancel
	// out, so a test that mixed them would pass against the old code too. The
	// strip is long enough that none of this clamps, or the drift would be
	// hidden by the end of the layout rather than dropped.
	for _, drift := range []tea.MouseButton{tea.MouseWheelLeft, tea.MouseWheelRight} {
		o.GetOrCreateScrollingLayout().ViewportX = 0
		gesture := []tea.MouseButton{
			tea.MouseWheelDown, tea.MouseWheelDown, drift,
			tea.MouseWheelDown, tea.MouseWheelDown, drift,
			tea.MouseWheelDown, tea.MouseWheelDown,
		}
		for _, b := range gesture {
			_, _ = handleMouseWheel(tea.MouseWheelMsg{Button: b, Mod: tea.ModShift}, o)
		}

		want := 6 * config.NiriScrollCellsDefault
		if got := o.GetOrCreateScrollingLayout().ViewportX; got != want {
			t.Fatalf("six scrolls with %v drift through them left the strip at %d, want %d", drift, got, want)
		}
	}
}

// A wheel with shift held arrives entirely as left and right on macOS, where
// the window server swaps the axes before the terminal sees the event. The
// majority rule has to let that gesture through, or the strip would be dead for
// everyone on a mouse.
func TestAPurelyHorizontalGestureStillMovesTheStrip(t *testing.T) {
	prev := config.Global.NiriScrollCells
	t.Cleanup(func() { config.Global.NiriScrollCells = prev })
	config.Global.NiriScrollCells = config.NiriScrollCellsDefault

	wins := make([]*terminal.Window, 6)
	for i := range wins {
		wins[i] = windowWithScrollback(t)
	}
	o := &app.OS{
		Settings:           config.Global,
		Mode:               app.WindowManagementMode,
		UseScrollingLayout: true,
		AutoTiling:         true,
		FocusedWindow:      0,
		Windows:            wins,
		EffectiveWidth:     40,
		EffectiveHeight:    20,
	}
	o.TileAllWindows()

	for range 3 {
		_, _ = handleMouseWheel(tea.MouseWheelMsg{Button: tea.MouseWheelRight, Mod: tea.ModShift}, o)
	}
	want := 3 * config.NiriScrollCellsDefault
	if got := o.GetOrCreateScrollingLayout().ViewportX; got != want {
		t.Fatalf("three horizontal scrolls left the strip at %d, want %d", got, want)
	}
}

// The step is a setting rather than a share of the screen. See
// config.NiriScrollCellsDefault.
//
// Control: put the step back to viewW/5 in ScrollingScrollViewport and every
// subtest reports the same distance, because none of them depends on the
// setting any more.
func TestTheStripStepFollowsItsSetting(t *testing.T) {
	prev := config.Global.NiriScrollCells
	t.Cleanup(func() { config.Global.NiriScrollCells = prev })

	for _, cells := range []int{1, 8, 20} {
		t.Run(fmt.Sprintf("%d cells", cells), func(t *testing.T) {
			config.Global.NiriScrollCells = cells
			wins := make([]*terminal.Window, 6)
			for i := range wins {
				wins[i] = windowWithScrollback(t)
			}
			o := &app.OS{
				Settings:           config.Global,
				Mode:               app.WindowManagementMode,
				UseScrollingLayout: true,
				AutoTiling:         true,
				FocusedWindow:      0,
				Windows:            wins,
				EffectiveWidth:     40,
				EffectiveHeight:    20,
			}
			o.TileAllWindows()

			_, _ = handleMouseWheel(tea.MouseWheelMsg{Button: tea.MouseWheelDown, Mod: tea.ModShift}, o)
			if got := o.GetOrCreateScrollingLayout().ViewportX; got != cells {
				t.Fatalf("one scroll with niri_scroll_cells=%d moved the strip %d cells", cells, got)
			}
		})
	}
}
