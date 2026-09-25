package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// stripHits renders a collapsed strip on the given side and hands back the
// model with the rectangles it recorded while drawing.
func stripHits(t *testing.T, pos string, h int) *OS {
	t.Helper()
	m, tree := stripOS(t, 120, h)
	withSidebar(t, true, pos, config.SidebarDefaultWidth)
	m.Settings = config.Global
	m.SidebarCollapsed = true
	m.SidebarFocused = true
	m.sidebarPanelLinesForTree(tree)
	return m
}

// TestStripEdgeColumnStillResizesWhenTheGestureMoves: the edge rule is the width
// handle, and the click borrowed from it must give it straight back the moment
// the pointer leaves the column it was pressed on.
func TestStripEdgeColumnStillResizesWhenTheGestureMoves(t *testing.T) {
	m := stripHits(t, "left", 20)
	edgeX := m.GetSidebarWidth() - 1
	top := m.GetTopMargin()
	m.DaemonClient = nil // the resize re-lays the panes, which syncs to the daemon

	if !m.SidebarClick(edgeX, top+1, false) || !m.SidebarEdgeActive() {
		t.Fatal("a press on the strip's edge rule did not arm the resize")
	}
	m.SidebarEdgeMotion(24, top+1)
	if m.SidebarEdge.HaveRow {
		t.Error("the resize drag is still holding a pending row click")
	}
	m.SidebarEdgeRelease(24, top+1)
	if m.SidebarCollapsed {
		t.Error("dragging the strip's edge out left the rail collapsed")
	}
	// Read off the model: the drag moves this session's rail, not a global
	// every session in the process shares. See TestSidebarEdgeResizeClampAndPersist.
	if got := m.sidebarWidthPreference(); got != 25 {
		t.Errorf("the drag set the width to %d, want the pointer's column", got)
	}
}
