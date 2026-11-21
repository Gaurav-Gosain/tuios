package app

import (
	"testing"
)

// TestWidthPercentOnBoundaryPane sizes the rightmost pane to 80% of the
// content region: its right edge is the screen boundary, so the left divider
// moves and the left pane yields the difference.
func TestWidthPercentOnBoundaryPane(t *testing.T) {
	m, left, right := twoPaneSideBySide(t)

	m.SetFocusedWindowWidthPercent(80)

	if want := 120 * 80 / 100; right.Width != want {
		t.Errorf("boundary pane width = %d, want %d (80%% of 120)", right.Width, want)
	}
	if left.Width != 120-right.Width {
		t.Errorf("left pane width = %d, want %d (the rest of the row)", left.Width, 120-right.Width)
	}
	if left.X+left.Width != right.X {
		t.Errorf("panes no longer adjacent: left ends %d, right starts %d", left.X+left.Width, right.X)
	}
}

// TestPercentGuardrails pins the guards: out-of-range percentages and tiling
// off are no-ops, and a percentage equal to the current size changes nothing.
// The accepted range is 10..90, matching the resize_width_N/resize_height_N
// actions: there is no resize_width_100, so 100 is out of range too.
func TestPercentGuardrails(t *testing.T) {
	m, _, right := twoPaneSideBySide(t)
	m.FocusedWindow = 1
	beforeRight := right.Width

	m.SetFocusedWindowWidthPercent(5)
	m.SetFocusedWindowWidthPercent(95)
	m.SetFocusedWindowWidthPercent(100)
	if right.Width != beforeRight {
		t.Errorf("out-of-range percent moved the window: %d -> %d", beforeRight, right.Width)
	}

	m.SetFocusedWindowWidthPercent(50)
	if right.Width != 60 {
		t.Errorf("50%% on a 50%% pane moved it: %d", right.Width)
	}

	m.AutoTiling = false
	m.SetFocusedWindowWidthPercent(80)
	if right.Width != 60 {
		t.Errorf("percent resize moved a pane with tiling off: %d", right.Width)
	}
}
