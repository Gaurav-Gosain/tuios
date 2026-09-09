package app

import (
	"testing"
)

// A pane the pointer is dragging belongs to the pointer until the drop. Two
// things used to take it back mid-gesture, both arriving with a peer's state
// sync: the sync adopted the daemon's rectangle for the pane, which is the slot
// it left, and the staleness check then read the displaced pane as a layout in
// need of recomputing and retiled it back into the slot as well. The e2e suite
// drives the whole gesture; these pin the two mechanisms one at a time.

// holdDrag puts m into the state beginWindowDrag leaves it in for window i,
// displaces the pane by (dx, dy) as the motion handler would, and returns the
// slot it left.
func holdDrag(m *OS, i, dx, dy int) [4]int {
	w := m.Windows[i]
	slot := [4]int{w.X, w.Y, w.Width, w.Height}
	m.Dragging = true
	m.InteractionMode = true
	m.DraggedWindowIndex = i
	w.IsBeingManipulated = true
	m.TiledX, m.TiledY, m.TiledWidth, m.TiledHeight = slot[0], slot[1], slot[2], slot[3]
	w.X += dx
	w.Y += dy
	return slot
}

func rectOf4(m *OS, i int) [4]int {
	w := m.Windows[i]
	return [4]int{w.X, w.Y, w.Width, w.Height}
}

// dragLayouts are the three tiling layouts a retile mid-drag has to leave the
// dragged pane alone in.
var dragLayouts = []struct {
	name      string
	bsp, roll bool
}{
	{"bsp", true, false},
	{"master-stack", false, false},
	{"scrolling", false, true},
}

// TestARetileMidDragLeavesThePaneUnderThePointer: a layout pass that runs while
// a drag is live places every pane but the dragged one, and records the slot it
// would have given that pane so the drop can still use it. In every layout,
// since each has a placement loop of its own.
func TestARetileMidDragLeavesThePaneUnderThePointer(t *testing.T) {
	for _, l := range dragLayouts {
		t.Run(l.name, func(t *testing.T) {
			r := newRigSized(t, 2, holderCols, holderRows)
			r.m.UseBSPLayout, r.m.UseScrollingLayout = l.bsp, l.roll
			r.tile()
			r.m.CompleteAllAnimations()
			other := rectOf4(r.m, 0)
			slot := holdDrag(r.m, 1, -10, 5)
			displaced := rectOf4(r.m, 1)

			if r.m.tiledLayoutStale() {
				t.Error("a pane in mid-drag reads as a stale layout, which is what retiles it back into its slot")
			}

			r.m.TileAllWindows()
			r.m.CompleteAllAnimations()
			if got := rectOf4(r.m, 1); got != displaced {
				t.Errorf("the retile moved the dragged pane from %v to %v; it belongs to the pointer until the drop", displaced, got)
			}
			if got := rectOf4(r.m, 0); got != other {
				t.Errorf("the retile moved the pane that was not being dragged from %v to %v", other, got)
			}
			if got := [4]int{r.m.TiledX, r.m.TiledY, r.m.TiledWidth, r.m.TiledHeight}; got != slot {
				t.Errorf("the retile recorded slot %v for the dragged pane, want %v", got, slot)
			}

			// The control: with no drag live, the same displaced pane is a stale
			// layout and the retile puts it back. Without this the assertions
			// above could pass on a fixture that never retiles anything.
			r.m.Dragging = false
			r.m.DraggedWindowIndex = -1
			r.m.Windows[1].IsBeingManipulated = false
			if !r.m.tiledLayoutStale() {
				t.Fatal("a displaced pane with no drag live does not read as stale, so the check above proves nothing")
			}
			r.m.TileAllWindows()
			r.m.CompleteAllAnimations()
			if got := rectOf4(r.m, 1); got != slot {
				t.Errorf("with no drag live the retile left the pane at %v, want its slot %v", got, slot)
			}
		})
	}
}

// TestAPeerSyncMidDragLeavesThePaneUnderThePointer: a peer's sync landing
// during a drag neither moves the dragged pane nor lets its own focus change
// take the pane's place. The focus is adopted, as it is for any sync; what the
// motion handler does with that is pinned in internal/input.
func TestAPeerSyncMidDragLeavesThePaneUnderThePointer(t *testing.T) {
	r, p, ex := geometryRig(t, clientGlobals{}, clientGlobals{})
	r.m.CompleteAllAnimations()
	other := rectOf4(r.m, 0)
	slot := holdDrag(r.m, 1, -10, 5)
	displaced := rectOf4(r.m, 1)

	// The peer focuses the other pane and says so, which is one keystroke on
	// its side and one sync on ours.
	p.m.FocusWindow(0)
	p.m.SyncStateToDaemon()
	settleUntil(t, ex, "the peer's focus to arrive", func() bool { return r.m.FocusedWindow == 0 })

	if got := rectOf4(r.m, 1); got != displaced {
		t.Errorf("the sync moved the dragged pane from %v to %v; it belongs to the pointer until the drop", displaced, got)
	}
	if got := rectOf4(r.m, 0); got != other {
		t.Errorf("the sync moved the pane that was not being dragged from %v to %v", other, got)
	}
	if got := [4]int{r.m.TiledX, r.m.TiledY, r.m.TiledWidth, r.m.TiledHeight}; got != slot {
		t.Errorf("after the sync the drop would use slot %v, want %v", got, slot)
	}
	if r.m.LiveWindowDrag() != r.m.Windows[1] {
		t.Error("the drag is no longer live after the sync")
	}
}
