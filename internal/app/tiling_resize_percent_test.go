package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// Percentage resizing of the focused pane (issue #29).
//
// The width percentage is measured against the content region and the height
// percentage against the usable height, driving the same edge logic the plain
// width/height keys use, so the boundary-pane fallback (move the far edge)
// applies to the rightmost pane and the bottommost pane too.

// TestWidthPercentOnBoundaryPane sizes the rightmost pane to 80% of the
// content region: its right edge is the screen boundary, so the left divider
// moves and the left pane yields the difference.
func TestWidthPercentOnBoundaryPane(t *testing.T) {
	m, left, right := twoPaneSideBySide(t) // both 60 wide, content 120
	// Focus is already the right (boundary) pane.

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

// TestWidthPercentOnNonBoundaryPane sizes the left pane to 30% of the content
// region: its right edge is a real divider, so the divider moves and the right
// pane yields the difference.
func TestWidthPercentOnNonBoundaryPane(t *testing.T) {
	m, left, right := twoPaneSideBySide(t)
	m.FocusedWindow = 0 // focus the LEFT (non-boundary) pane

	m.SetFocusedWindowWidthPercent(30)

	if want := 120 * 30 / 100; left.Width != want {
		t.Errorf("left pane width = %d, want %d (30%% of 120)", left.Width, want)
	}
	if right.Width != 120-left.Width {
		t.Errorf("right pane width = %d, want %d (the rest of the row)", right.Width, 120-left.Width)
	}
	if left.X+left.Width != right.X {
		t.Errorf("panes no longer adjacent: left ends %d, right starts %d", left.X+left.Width, right.X)
	}
}

// twoPanesStacked builds a tiling OS with two daemon panes splitting the column:
// a top pane whose bottom edge is a shared divider and a bottom pane whose
// bottom edge is the screen boundary. The usable height is the render height
// minus the dock margins, so it is measured at runtime rather than assumed.
func twoPanesStacked(t *testing.T) (m *OS, top, bottom *terminal.Window) {
	t.Helper()
	const width, height = 120, 40
	top = newTestWindow(t, "top-000000000000000000000000000000001", width, 20)
	bottom = newTestWindow(t, "bottom-0000000000000000000000000000001", width, 20)
	top.X, top.Y, top.Width, top.Height = 0, 0, width, 20
	bottom.X, bottom.Y, bottom.Width, bottom.Height = 0, 20, width, 20
	top.Tiled, bottom.Tiled = true, true
	m = &OS{
		Settings:             config.Global,
		Windows:              []*terminal.Window{top, bottom},
		FocusedWindow:        1, // focus the BOTTOM (boundary) pane
		WorkspaceFocus:       map[int]int{},
		WorkspaceHasCustom:   map[int]bool{},
		WorkspaceLayouts:     map[int][]WindowLayout{},
		WorkspaceMasterRatio: map[int]float64{},
		NumWorkspaces:        9,
		Width:                width,
		Height:               height,
		AutoTiling:           true,
	}
	return m, top, bottom
}

// TestHeightPercentMovesTheDivider verifies the direction of the vertical
// percentage resize on the bottommost (boundary) pane: growing the percentage
// moves the shared divider up, so the top pane yields rows, and the two panes
// keep filling the usable height without overlap.
func TestHeightPercentMovesTheDivider(t *testing.T) {
	m, top, bottom := twoPanesStacked(t) // both 20 tall
	usable := m.GetUsableHeight()
	beforeTop := top.Height

	// Grow the bottom pane toward 60% of the usable height.
	m.SetFocusedWindowHeightPercent(60)

	if top.Height >= beforeTop {
		t.Errorf("top pane did not yield rows: height %d -> %d", beforeTop, top.Height)
	}
	if bottom.Y >= beforeTop {
		t.Errorf("bottom pane did not move up: Y %d -> %d", beforeTop, bottom.Y)
	}
	if top.Height+bottom.Height != usable {
		t.Errorf("panes no longer fill the usable height: %d+%d = %d, want %d",
			top.Height, bottom.Height, top.Height+bottom.Height, usable)
	}
	if bottom.Y != top.Height {
		t.Errorf("panes no longer adjacent: top ends %d, bottom starts %d", top.Height, bottom.Y)
	}
}

// TestHeightPercentShrinksBack sizes the bottom pane below its current share,
// so the divider moves down and the top pane grows again.
func TestHeightPercentShrinksBack(t *testing.T) {
	m, top, bottom := twoPanesStacked(t)
	usable := m.GetUsableHeight()

	m.SetFocusedWindowHeightPercent(60) // move the divider up first
	afterGrowTop := top.Height

	m.SetFocusedWindowHeightPercent(40) // then move it back down
	if top.Height <= afterGrowTop {
		t.Errorf("top pane did not regain rows: %d -> %d", afterGrowTop, top.Height)
	}
	if top.Height+bottom.Height != usable {
		t.Errorf("panes no longer fill the usable height: %d+%d = %d, want %d",
			top.Height, bottom.Height, top.Height+bottom.Height, usable)
	}
}

// TestPercentGuardrails pins the guards: out-of-range percentages and tiling
// off are no-ops, and a percentage equal to the current size changes nothing.
func TestPercentGuardrails(t *testing.T) {
	m, _, right := twoPaneSideBySide(t)
	m.FocusedWindow = 1
	beforeRight := right.Width

	// Out of the 10..100 range: ignored.
	m.SetFocusedWindowWidthPercent(5)
	m.SetFocusedWindowWidthPercent(105)
	if right.Width != beforeRight {
		t.Errorf("out-of-range percent moved the window: %d -> %d", beforeRight, right.Width)
	}

	// Already at 50%: no movement.
	m.SetFocusedWindowWidthPercent(50)
	if right.Width != 60 {
		t.Errorf("50%% on a 50%% pane moved it: %d", right.Width)
	}

	// Tiling off: ignored.
	m.AutoTiling = false
	m.SetFocusedWindowWidthPercent(80)
	if right.Width != 60 {
		t.Errorf("percent resize moved a pane with tiling off: %d", right.Width)
	}
}
