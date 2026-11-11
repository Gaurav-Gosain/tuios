package app

import (
	"reflect"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The tiling switch's doors are tested on screen in
// tiling_switch_screen_test.go. What stays here is the stacking arithmetic,
// which no screen shows directly: the z order that raising a pane gives.

// TestRecalcZOrderKeepsTheStackingOrder: raising a window moves that window
// and nothing else. Three panes stacked A, C, B from the bottom: raising A
// leaves C under B.
func TestRecalcZOrderKeepsTheStackingOrder(t *testing.T) {
	m := &OS{
		Windows: []*terminal.Window{
			{ID: "A"}, {ID: "B"}, {ID: "C"},
		},
		WorkspaceFocus: map[int]int{},
	}
	zs := func() []int {
		out := make([]int, len(m.Windows))
		for i, w := range m.Windows {
			out[i] = w.Z
		}
		return out
	}
	m.FocusedWindow = 2
	m.RecalcZOrder()
	m.FocusedWindow = 1
	m.RecalcZOrder()
	if got := zs(); !reflect.DeepEqual(got, []int{0, 2, 1}) {
		t.Fatalf("after raising C then B: %v, want [0 2 1]", got)
	}
	m.FocusedWindow = 0
	m.RecalcZOrder()
	if got := zs(); !reflect.DeepEqual(got, []int{2, 1, 0}) {
		t.Errorf("raising A reordered B and C: %v, want [2 1 0]", got)
	}

	// A floating pane stays above every tiled pane, focused or not, and the
	// floating band keeps its own order too.
	m.Windows = append(m.Windows, &terminal.Window{ID: "F1", IsFloating: true}, &terminal.Window{ID: "F2", IsFloating: true})
	m.FocusedWindow = 4
	m.RecalcZOrder()
	m.FocusedWindow = 3
	m.RecalcZOrder()
	m.FocusedWindow = 0
	m.RecalcZOrder()
	if got := zs(); !reflect.DeepEqual(got, []int{2, 1, 0, 4, 3}) {
		t.Errorf("with two floats: %v, want [2 1 0 4 3]", got)
	}
}

// TestFloatingWindowsStayBelowEveryOverlay: however many panes are open, a
// floating pane is drawn above the separators and below the dock and every
// overlay, and the floating band keeps the panes' relative order.
func TestFloatingWindowsStayBelowEveryOverlay(t *testing.T) {
	overlays := map[string]int{
		"dock":              config.ZIndexDock,
		"help":              config.ZIndexHelp,
		"time":              config.ZIndexTime,
		"logs":              config.ZIndexLogs,
		"which-key":         config.ZIndexWhichKey,
		"scrollback":        config.ZIndexScrollbackBrowser,
		"palette":           config.ZIndexCommandPalette,
		"session switcher":  config.ZIndexSessionSwitcher,
		"layout picker":     config.ZIndexLayoutPicker,
		"overlay panels":    config.ZIndexOverlayBase,
		"context menu":      config.ZIndexContextMenu,
		"notifications":     config.ZIndexNotifications,
		"screensaver":       config.ZIndexScreensaver,
		"floating band top": config.ZIndexFloatingTop + 1,
	}
	prev := -1
	for z := range 2000 {
		floating := &terminal.Window{Z: z, IsFloating: true}
		got := windowLayerZ(floating, false)
		if got <= config.ZIndexSeparators || got <= config.ZIndexAnimating {
			t.Fatalf("Z=%d: a floating pane at %d is under the separators (%d) or an animating pane (%d)", z, got, config.ZIndexSeparators, config.ZIndexAnimating)
		}
		for name, top := range overlays {
			if got >= top {
				t.Fatalf("Z=%d: a floating pane at %d reaches the %s (%d)", z, got, name, top)
			}
		}
		if got < prev {
			t.Fatalf("Z=%d: the floating band is not monotonic (%d after %d)", z, got, prev)
		}
		prev = got
		// A float being dragged keeps its place in the band rather than
		// dropping to the animating layer under the other floats.
		floating.IsBeingManipulated = true
		if moving := windowLayerZ(floating, true); moving != got {
			t.Fatalf("Z=%d: a dragged float moves from %d to %d", z, got, moving)
		}
		if z < config.ZIndexSeparators {
			tiled := &terminal.Window{Z: z}
			if tz := windowLayerZ(tiled, false); tz >= config.ZIndexSeparators {
				t.Fatalf("Z=%d: a tiled pane at %d covers the separators", z, tz)
			}
		}
	}
	// The first two floats are in distinct layers, so a click can raise one
	// above the other.
	if windowLayerZ(&terminal.Window{Z: 1, IsFloating: true}, false) <= windowLayerZ(&terminal.Window{Z: 0, IsFloating: true}, false) {
		t.Error("two floats share a layer")
	}
}
