package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

func ctrlClickMsg(x, y int) tea.MouseClickMsg {
	return tea.MouseClickMsg{Button: tea.MouseLeft, X: x, Y: y, Mod: tea.ModCtrl}
}
func ctrlMotionMsg(x, y int) tea.MouseMotionMsg {
	return tea.MouseMotionMsg{Button: tea.MouseLeft, X: x, Y: y, Mod: tea.ModCtrl}
}
func ctrlReleaseMsg(x, y int) tea.MouseReleaseMsg {
	return tea.MouseReleaseMsg{Button: tea.MouseLeft, X: x, Y: y, Mod: tea.ModCtrl}
}
func ctrlShiftClickMsg(x, y int) tea.MouseClickMsg {
	return tea.MouseClickMsg{Button: tea.MouseLeft, X: x, Y: y, Mod: tea.ModCtrl | tea.ModShift}
}

func indexOf(o *OS2, w *terminal.Window) int {
	for i := range o.Windows {
		if o.Windows[i].ID == w.ID {
			return i
		}
	}
	return -1
}

// contentCell returns a cell well inside a pane's content area.
func contentCell(w *terminal.Window) (int, int) {
	return w.X + w.Width/2, w.Y + w.Height/2
}

// TestCtrlDragArmsThenMoves proves a ctrl+left press on pane content arms the
// gesture without moving, and a drag past the threshold commits to the shared
// window-drag path with the grabbed pane focused.
func TestCtrlDragArmsThenMoves(t *testing.T) {
	o, wa, wb := twoPaneBSP(t)
	left, _ := leftPaneOf(wa, wb)
	leftIdx := indexOf(o, left)
	cx, cy := contentCell(left)

	handleMouseClick(ctrlClickMsg(cx, cy), o)
	if !o.CtrlDragPending || o.CtrlDragIndex != leftIdx {
		t.Fatalf("ctrl press did not arm the drag (pending=%v idx=%d want %d)", o.CtrlDragPending, o.CtrlDragIndex, leftIdx)
	}
	if o.Dragging {
		t.Fatal("ctrl press moved immediately; it must wait for the threshold")
	}

	handleMouseMotion(ctrlMotionMsg(cx+10, cy), o)
	if o.CtrlDragPending {
		t.Fatal("drag past threshold did not commit (still pending)")
	}
	if !o.Dragging || !o.CtrlDragging {
		t.Fatalf("commit did not start a drag (dragging=%v ctrlDragging=%v)", o.Dragging, o.CtrlDragging)
	}
	if o.DraggedWindowIndex != leftIdx || o.FocusedWindow != leftIdx {
		t.Fatalf("wrong window grabbed: dragged=%d focused=%d want %d", o.DraggedWindowIndex, o.FocusedWindow, leftIdx)
	}
}

// TestCtrlClickNoDragDoesNotMultifocus proves a plain ctrl+press released below
// the threshold neither moves nor multi-selects: multi-select is now
// ctrl+shift+click, so a bare ctrl-click can no longer select by accident.
func TestCtrlClickNoDragDoesNotMultifocus(t *testing.T) {
	o, wa, wb := twoPaneBSP(t)
	left, _ := leftPaneOf(wa, wb)
	cx, cy := contentCell(left)

	handleMouseClick(ctrlClickMsg(cx, cy), o)
	handleMouseRelease(ctrlReleaseMsg(cx, cy), o)

	if o.Dragging || o.CtrlDragPending || o.CtrlDragging {
		t.Fatalf("sub-threshold ctrl press left a drag armed (dragging=%v pending=%v ctrlDragging=%v)", o.Dragging, o.CtrlDragPending, o.CtrlDragging)
	}
	if o.MultifocusSet[left.ID] {
		t.Fatal("a plain ctrl+click must not multi-select; that moved to ctrl+shift+click")
	}
}

// TestCtrlShiftClickMultifocus proves multi-select now lives on ctrl+shift+click
// and never arms a move.
func TestCtrlShiftClickMultifocus(t *testing.T) {
	o, wa, wb := twoPaneBSP(t)
	left, _ := leftPaneOf(wa, wb)
	cx, cy := contentCell(left)

	handleMouseClick(ctrlShiftClickMsg(cx, cy), o)

	if o.CtrlDragPending || o.Dragging {
		t.Fatal("ctrl+shift+click must not arm a move")
	}
	if !o.MultifocusSet[left.ID] {
		t.Fatal("ctrl+shift+click did not toggle multi-select")
	}
}

// TestCtrlDragDropsWhenCtrlReleased proves a committed ctrl-drag drops the
// instant a mouse event arrives without ctrl held, reusing the release path.
func TestCtrlDragDropsWhenCtrlReleased(t *testing.T) {
	o, wa, wb := twoPaneBSP(t)
	left, _ := leftPaneOf(wa, wb)
	cx, cy := contentCell(left)

	handleMouseClick(ctrlClickMsg(cx, cy), o)
	handleMouseMotion(ctrlMotionMsg(cx+10, cy), o)
	if !o.CtrlDragging {
		t.Fatal("ctrl-drag did not commit before the drop test")
	}

	// A motion whose modifiers no longer include ctrl finalizes the drop.
	handleMouseMotion(motionMsg(cx+12, cy), o)
	if o.CtrlDragging || o.Dragging {
		t.Fatalf("ctrl release mid-drag did not drop (ctrlDragging=%v dragging=%v)", o.CtrlDragging, o.Dragging)
	}
	if o.DraggedWindowIndex != -1 {
		t.Fatalf("drop did not clear the dragged index: %d", o.DraggedWindowIndex)
	}
}
