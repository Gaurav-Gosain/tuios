package input

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// twoPaneBSP builds a tiled BSP workspace of two daemon panes split vertically,
// with shared borders on so a separator cell sits between them.
func twoPaneBSP(t *testing.T) (*OS2, *terminal.Window, *terminal.Window) {
	t.Helper()
	const cols, rows = 120, 40

	m := &app.OS{
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		WorkspaceFocus:   make(map[int]int),
		Width:            cols,
		Height:           rows,
		AutoTiling:       true,
		UseBSPLayout:     true,
		// The layout reads the model's session-settled value, not the global;
		// seed it from the global the caller just set. NewOS does the same.
		SharedBorders:        config.SharedBorders,
		FocusedWindow:        0,
		PendingResizes:       make(map[string][2]int),
		WorkspaceHasCustom:   map[int]bool{},
		WorkspaceLayouts:     map[int][]app.WindowLayout{},
		WorkspaceMasterRatio: map[int]float64{},
	}
	for i := range 2 {
		id := fmt.Sprintf("border-%d", i)
		ptyData := make(chan struct{}, 1)
		done := make(chan struct{})
		go func() {
			for {
				select {
				case <-ptyData:
				case <-done:
					return
				}
			}
		}()
		t.Cleanup(func() { close(done) })
		win := terminal.NewDaemonWindow(id, "test", 0, 0, cols, rows, 0, "pty-"+id, ptyData)
		if win == nil {
			t.Fatal("NewDaemonWindow returned nil")
		}
		t.Cleanup(win.Close)
		win.Workspace = 1
		win.Tiled = true
		m.Windows = append(m.Windows, win)
	}
	m.TileAllWindows()
	tree := m.WorkspaceTrees[m.CurrentWorkspace]
	if tree == nil {
		t.Fatal("no BSP tree")
	}
	for intID, rect := range tree.ApplyLayout(m.GetBSPBounds(), sharedGap()) {
		if win := m.GetWindowByIntID(intID); win != nil {
			win.X, win.Y, win.Width, win.Height = rect.X, rect.Y, rect.W, rect.H
		}
	}
	return (*OS2)(m), m.Windows[0], m.Windows[1]
}

// OS2 is app.OS under a local alias so the test can pass it to the unexported
// handlers without importing app cosmetics.
type OS2 = app.OS

func leftPaneOf(a, b *terminal.Window) (left, right *terminal.Window) {
	if a.X < b.X {
		return a, b
	}
	return b, a
}

func clickMsg(x, y int) tea.MouseClickMsg {
	return tea.MouseClickMsg{Button: tea.MouseLeft, X: x, Y: y}
}
func motionMsg(x, y int) tea.MouseMotionMsg {
	return tea.MouseMotionMsg{Button: tea.MouseLeft, X: x, Y: y}
}
func releaseMsg(x, y int) tea.MouseReleaseMsg {
	return tea.MouseReleaseMsg{Button: tea.MouseLeft, X: x, Y: y}
}

// TestBorderDragMovesSharedDivider is the regression for TASK 2: pressing the
// separator cell between two tiled panes and dragging moves the shared divider,
// widening one pane and narrowing its neighbor, with the PTY resize deferred to
// release.
func TestBorderDragMovesSharedDivider(t *testing.T) {
	prev := config.SharedBorders
	config.SharedBorders = true
	defer func() { config.SharedBorders = prev }()

	o, wa, wb := twoPaneBSP(t)
	left, right := leftPaneOf(wa, wb)

	// The separator column sits one past the left pane's right edge.
	dividerX := left.X + left.Width
	rowY := left.Y + left.Height/2

	beforeLeftW, beforeRightW := left.Width, right.Width

	handleMouseClick(clickMsg(dividerX, rowY), o)
	if !o.BorderResizing || o.BorderResizeEdge != app.BorderEdgeRight {
		t.Fatalf("press on divider did not arm a right-edge border resize (resizing=%v edge=%v)", o.BorderResizing, o.BorderResizeEdge)
	}

	// Drag the divider left by 10 cells; the left pane must shrink and the right
	// pane must grow.
	handleMouseMotion(motionMsg(dividerX-10, rowY), o)
	if left.Width >= beforeLeftW {
		t.Fatalf("left pane did not shrink: %d -> %d", beforeLeftW, left.Width)
	}
	if right.Width <= beforeRightW {
		t.Fatalf("right pane did not grow: %d -> %d", beforeRightW, right.Width)
	}

	handleMouseRelease(releaseMsg(dividerX-10, rowY), o)
	if o.BorderResizing {
		t.Fatal("border resize flag not cleared on release")
	}
	if len(o.PendingResizes) != 0 {
		t.Fatalf("pending PTY resizes not flushed on release: %d left", len(o.PendingResizes))
	}
}

// TestBorderDragIgnoresContent proves the gesture is border-only: a press on a
// pane's content never arms a border resize.
func TestBorderDragIgnoresContent(t *testing.T) {
	prev := config.SharedBorders
	config.SharedBorders = true
	defer func() { config.SharedBorders = prev }()

	o, wa, wb := twoPaneBSP(t)
	left, _ := leftPaneOf(wa, wb)

	// A cell well inside the left pane's content.
	cx := left.X + left.Width/2
	cy := left.Y + left.Height/2
	handleMouseClick(clickMsg(cx, cy), o)
	if o.BorderResizing {
		t.Fatal("a content press wrongly armed a border resize")
	}
}

// sharedGap is the column the layout keeps between panes for the divider, on
// the same terms app.OS.separatorGap does. These tests drive the layout
// directly, so they answer the question the app would answer for them.
func sharedGap() int {
	if config.SharedBorders {
		return 1
	}
	return 0
}
