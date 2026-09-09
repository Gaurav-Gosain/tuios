package input

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// Dragging a tiled pane by its title bar is the one direct-manipulation gesture
// a new install meets first, since tiling ships on. The pane has to follow the
// pointer while the button is down, in every layout, and the drop has to put it
// somewhere the layout agrees with: its neighbour's slot when it lands on a
// neighbour, its own slot otherwise.
//
// These drive OS.Update, as drag_announce_test.go does, so the press arms what
// a real press arms and the motion passes through the same routing.

// dragLayouts are the three tiling layouts a title drag has to behave the same
// in. The scrolling strip used to be the odd one out: it held its pane still
// until the drop, so the drag showed nothing at all until the button came up.
var dragLayouts = []struct {
	name      string
	bsp, roll bool
}{
	{config.LayoutModeBSP, true, false},
	{config.LayoutModeMasterStack, false, false},
	{config.LayoutModeScrolling, false, true},
}

// twoTiledPanes builds a tiled workspace of two daemon panes in the named layout
// and lands every placement animation, so each pane sits in its slot. It returns
// the pane on the left and the pane on the right.
func twoTiledPanes(t *testing.T, bsp, scrolling bool) (*app.OS, *terminal.Window, *terminal.Window) {
	t.Helper()
	const cols, rows = 120, 40
	m := &app.OS{
		Settings:             config.Global,
		Mode:                 app.WindowManagementMode,
		NumWorkspaces:        9,
		CurrentWorkspace:     1,
		WorkspaceFocus:       make(map[int]int),
		Width:                cols,
		Height:               rows,
		AutoTiling:           true,
		UseBSPLayout:         bsp,
		UseScrollingLayout:   scrolling,
		SharedBorders:        config.Global.SharedBorders,
		FocusedWindow:        0,
		DraggedWindowIndex:   -1,
		PendingResizes:       make(map[string][2]int),
		WorkspaceHasCustom:   map[int]bool{},
		WorkspaceLayouts:     map[int][]app.WindowLayout{},
		WorkspaceMasterRatio: map[int]float64{},
	}
	for i := range 2 {
		id := fmt.Sprintf("drag-%d", i)
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
		win := terminal.NewDaemonWindow(id, "test", 0, 0, cols, rows, 0, "pty-"+id, ptyData, config.DefaultScrollbackLines)
		if win == nil {
			t.Fatal("NewDaemonWindow returned nil")
		}
		t.Cleanup(win.Close)
		win.Workspace = 1
		m.Windows = append(m.Windows, win)
	}
	m.TileAllWindows()
	m.CompleteAllAnimations()
	left, right := leftPaneOf(m.Windows[0], m.Windows[1])
	if left.X+left.Width > right.X {
		t.Fatalf("the fixture did not tile: left %v right %v", rectOf(left), rectOf(right))
	}
	return m, left, right
}

func rectOf(w *terminal.Window) [4]int { return [4]int{w.X, w.Y, w.Width, w.Height} }

// titleCell is the middle of a pane's top row, clear of the buttons at either
// end. Nothing has rendered, so no button cells are recorded and any cell of
// the row is a drag handle.
func titleCell(w *terminal.Window) (int, int) { return w.X + w.Width/2, w.Y }

// TestATitleDragMovesTheTiledPaneInEveryLayout is the regression for the
// scrolling strip, with the other two layouts as the control that says what
// "moves" means: the pane's rectangle follows the pointer by exactly the
// pointer's travel, and its neighbour does not move at all.
func TestATitleDragMovesTheTiledPaneInEveryLayout(t *testing.T) {
	app.SetInputHandler(HandleInput)
	for _, l := range dragLayouts {
		t.Run(l.name, func(t *testing.T) {
			m, left, right := twoTiledPanes(t, l.bsp, l.roll)
			leftBefore, slot := rectOf(left), rectOf(right)
			cx, cy := titleCell(right)

			m.Update(clickMsg(cx, cy))
			for step := 1; step <= 10; step++ {
				m.Update(motionMsg(cx-step, cy+step))
			}

			if right.X != slot[0]-10 || right.Y != slot[1]+10 {
				t.Errorf("after ten cells of motion the pane is at (%d,%d), want (%d,%d): "+
					"the title drag is not moving the pane", right.X, right.Y, slot[0]-10, slot[1]+10)
			}
			if rectOf(left) != leftBefore {
				t.Errorf("the neighbour moved during the drag: %v -> %v", leftBefore, rectOf(left))
			}

			// Released over its own slot: nothing to swap with, so it snaps back.
			m.Update(releaseMsg(cx-10, cy+10))
			m.CompleteAllAnimations()
			if rectOf(right) != slot {
				t.Errorf("dropped in its own slot the pane sits at %v, want its slot %v", rectOf(right), slot)
			}
			if rectOf(left) != leftBefore {
				t.Errorf("the drop moved the neighbour: %v -> %v", leftBefore, rectOf(left))
			}
			if m.Dragging || m.DraggedWindowIndex != -1 {
				t.Errorf("the gesture is still live after the release: dragging=%v index=%d", m.Dragging, m.DraggedWindowIndex)
			}
		})
	}
}

// TestATitleDropOnTheNeighbourSwapsInEveryLayout is the positive half: a drop
// that lands on the other pane exchanges the two slots.
func TestATitleDropOnTheNeighbourSwapsInEveryLayout(t *testing.T) {
	app.SetInputHandler(HandleInput)
	for _, l := range dragLayouts {
		t.Run(l.name, func(t *testing.T) {
			m, left, right := twoTiledPanes(t, l.bsp, l.roll)
			leftSlot, rightSlot := rectOf(left), rectOf(right)
			cx, cy := titleCell(right)
			tx, ty := left.X+left.Width/2, left.Y+left.Height/2

			m.Update(clickMsg(cx, cy))
			steps := max(abs(tx-cx), abs(ty-cy))
			for i := 1; i <= steps; i++ {
				m.Update(motionMsg(cx+(tx-cx)*i/steps, cy+(ty-cy)*i/steps))
			}
			m.Update(releaseMsg(tx, ty))
			m.CompleteAllAnimations()

			if rectOf(right) != leftSlot || rectOf(left) != rightSlot {
				t.Errorf("dropped on the neighbour the panes sit at %v and %v, want them swapped into %v and %v",
					rectOf(right), rectOf(left), leftSlot, rightSlot)
			}
		})
	}
}

// TestADragKeepsThePaneItGrabbedWhenFocusMoves pins the pane the motion moves
// to the pane the press grabbed. Focus used to be what the motion read, and a
// peer's state sync carries the peer's focus, so a sync landing mid-drag handed
// the rest of the gesture to whichever pane the peer had focused. The focus
// change is made directly here; the sync path is covered in internal/app and
// end to end.
func TestADragKeepsThePaneItGrabbedWhenFocusMoves(t *testing.T) {
	app.SetInputHandler(HandleInput)
	m, left, right := twoTiledPanes(t, true, false)
	leftBefore, slot := rectOf(left), rectOf(right)
	cx, cy := titleCell(right)

	m.Update(clickMsg(cx, cy))
	for step := 1; step <= 5; step++ {
		m.Update(motionMsg(cx-step, cy+step))
	}
	for i, w := range m.Windows {
		if w == left {
			m.FocusedWindow = i
		}
	}
	for step := 6; step <= 10; step++ {
		m.Update(motionMsg(cx-step, cy+step))
	}

	if right.X != slot[0]-10 || right.Y != slot[1]+10 {
		t.Errorf("the grabbed pane stopped following the pointer once focus moved: at (%d,%d), want (%d,%d)",
			right.X, right.Y, slot[0]-10, slot[1]+10)
	}
	if rectOf(left) != leftBefore {
		t.Errorf("the newly focused pane was dragged instead: %v -> %v", leftBefore, rectOf(left))
	}
	m.Update(releaseMsg(cx-10, cy+10))
	m.CompleteAllAnimations()
	if rectOf(right) != slot || rectOf(left) != leftBefore {
		t.Errorf("the drop left the layout at %v and %v, want %v and %v", rectOf(right), rectOf(left), slot, leftBefore)
	}
}
