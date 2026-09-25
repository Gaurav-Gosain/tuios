package input

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// masterStackPair is borderMatrixOS switched to the master-stack layout, with
// the master pane first.
func masterStackPair(t *testing.T, shared bool) (*app.OS, *terminal.Window, *terminal.Window) {
	t.Helper()
	m, _, _ := borderMatrixOS(t, true, shared)
	m.ApplyLayoutModeName(config.LayoutModeMasterStack)
	m.TileAllWindows()
	m.CompleteAllAnimations()
	master, stack := m.Windows[0], m.Windows[1]
	if master.X >= stack.X {
		t.Fatalf("setup: master-stack did not put the panes side by side: %d and %d", master.X, stack.X)
	}
	return m, master, stack
}

// TestMasterGrowKeySurvivesARetile is the ">" key (resize_master_grow) in
// master-stack: the width it adds has to be there after the next retile.
//
// NEGATIVE CONTROL: drop the SyncMasterStackFromGeometry call from
// AdjustTilingNeighbors and the master pane is back at half the width.
func TestMasterGrowKeySurvivesARetile(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(fmt.Sprintf("shared=%v", shared), func(t *testing.T) {
			m, master, _ := masterStackPair(t, shared)
			m.FocusedWindow = 0
			before := master.Width

			for range 3 {
				handleResizeMasterGrow(tea.KeyPressMsg{}, m)
			}
			grown := master.Width
			if grown != before+12 {
				t.Fatalf("setup: three presses took the master pane from %d to %d columns, want %d", before, grown, before+12)
			}

			m.TileAllWindows()
			m.CompleteAllAnimations()
			if master.Width != grown {
				t.Errorf("the master pane is %d columns after a retile, want the %d the key left it at", master.Width, grown)
			}
		})
	}
}

// TestMasterBorderDragSurvivesARetile is the mouse path: a drag on the divider
// in master-stack is written into the master ratio on release, so the retile
// that follows keeps it.
//
// NEGATIVE CONTROL: drop the SyncMasterStackFromGeometry call from the release
// path and the master pane is back at half the width after the retile.
func TestMasterBorderDragSurvivesARetile(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(fmt.Sprintf("shared=%v", shared), func(t *testing.T) {
			m, master, _ := masterStackPair(t, shared)
			before := master.Width

			grabX := dividerColumn(master)
			rowY := master.Y + master.Height/2
			m = send(m, clickMsg(grabX, rowY))
			if !m.BorderResizing {
				t.Fatalf("setup: a press at x=%d on the divider armed no resize", grabX)
			}
			m = send(m, motionMsg(grabX+20, rowY))
			m = send(m, releaseMsg(grabX+20, rowY))
			dragged := master.Width
			if dragged <= before {
				t.Fatalf("setup: the drag left the master pane at %d columns (was %d)", dragged, before)
			}

			m.TileAllWindows()
			m.CompleteAllAnimations()
			if master.Width != dragged {
				t.Errorf("the master pane is %d columns after a retile, want the %d the drag left it at", master.Width, dragged)
			}
		})
	}
}
