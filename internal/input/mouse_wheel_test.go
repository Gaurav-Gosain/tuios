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
