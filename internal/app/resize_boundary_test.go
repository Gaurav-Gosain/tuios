package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// twoPaneSideBySide builds a tiling OS with two daemon panes splitting the row:
// a left pane whose right edge is a shared divider and a right pane whose right
// edge is the screen boundary. This is the exact layout in which the primary
// width keys (< and >) were silently no-ops for the rightmost pane.
func twoPaneSideBySide(t *testing.T) (m *OS, left, right *terminal.Window) {
	t.Helper()
	const width, height = 120, 40
	left = newTestWindow(t, "left-000000000000000000000000000001", 60, height)
	right = newTestWindow(t, "right-00000000000000000000000000001", 60, height)
	left.X, left.Y, left.Width, left.Height = 0, 0, 60, height
	right.X, right.Y, right.Width, right.Height = 60, 0, 60, height
	left.Tiled, right.Tiled = true, true
	m = &OS{
		Windows:              []*terminal.Window{left, right},
		FocusedWindow:        1, // focus the RIGHT (boundary) pane
		WorkspaceFocus:       map[int]int{},
		WorkspaceHasCustom:   map[int]bool{},
		WorkspaceLayouts:     map[int][]WindowLayout{},
		WorkspaceMasterRatio: map[int]float64{},
		NumWorkspaces:        9,
		Width:                width,
		Height:               height,
		AutoTiling:           true,
	}
	return m, left, right
}

// TestResizeWidthMovesLeftDividerForBoundaryPane is the regression for TASK 1:
// the reported "resize does nothing in daemon mode" was the rightmost pane,
// whose right edge is the screen boundary. > (grow) and < (shrink) move the
// right edge, so the boundary gate dropped both. The fix falls back to the left
// divider so the primary keys resize every pane; grow still grows.
func TestResizeWidthMovesLeftDividerForBoundaryPane(t *testing.T) {
	m, left, right := twoPaneSideBySide(t)

	beforeRightW, beforeLeftW := right.Width, left.Width

	m.ResizeFocusedWindowWidth(4) // >
	if right.Width <= beforeRightW {
		t.Fatalf("grow was a no-op on the boundary pane: right width %d -> %d", beforeRightW, right.Width)
	}
	if left.Width >= beforeLeftW {
		t.Fatalf("neighbor should have yielded width: left %d -> %d", beforeLeftW, left.Width)
	}
	if left.X+left.Width != right.X {
		t.Fatalf("panes no longer adjacent: left ends %d, right starts %d", left.X+left.Width, right.X)
	}

	grownLeftW := left.Width
	m.ResizeFocusedWindowWidth(-4) // <
	if left.Width <= grownLeftW {
		t.Fatalf("shrink was a no-op on the boundary pane: left width %d -> %d", grownLeftW, left.Width)
	}
}

// TestResizeWidthNonBoundaryPaneStillMovesRightEdge guards the unchanged case:
// a pane whose right edge is a real divider keeps resizing via that edge.
func TestResizeWidthNonBoundaryPaneStillMovesRightEdge(t *testing.T) {
	m, left, right := twoPaneSideBySide(t)
	m.FocusedWindow = 0 // focus the LEFT (non-boundary) pane

	beforeLeftW, beforeRightW := left.Width, right.Width
	m.ResizeFocusedWindowWidth(4) // >
	if left.Width <= beforeLeftW {
		t.Fatalf("grow did not widen the left pane: %d -> %d", beforeLeftW, left.Width)
	}
	if right.Width >= beforeRightW {
		t.Fatalf("right neighbor should have yielded width: %d -> %d", beforeRightW, right.Width)
	}
}
