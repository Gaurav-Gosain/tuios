package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestSidebarEdgeResizeClampAndPersist drives the edge-rule width drag: the
// press arms it, motion clamps the width to the allowed range, release persists
// it, and a stored width overrides the config default on the next load.
//
// The width the drag moves is read off the model rather than off the config
// global. It was the global, and the global is process-wide: one tuios-web
// process serves several sessions, so one browser tab's drag resized a rail
// nobody had touched. The width is also session state now, shared with the
// session's other clients, and shared state cannot live in a process global.
func TestSidebarEdgeResizeClampAndPersist(t *testing.T) {
	withSidebar(t, true, "left", config.SidebarDefaultWidth)
	m := &OS{Settings: config.Global, Width: 120, Height: 40}
	top := m.GetTopMargin()

	w := m.GetSidebarWidth()
	edgeX := w - 1
	if !m.sidebarOnEdge(edgeX) {
		t.Fatalf("column %d not recognized as the edge rule", edgeX)
	}
	if !m.SidebarClick(edgeX, top, false) || !m.SidebarEdgeActive() {
		t.Fatal("edge press did not arm the width resize")
	}

	lo, hi := m.sidebarWidthBounds()

	// Past the max clamps to the upper bound; past the min to the lower bound.
	m.SidebarEdgeMotion(200, top)
	if got := m.sidebarWidthPreference(); got != hi {
		t.Errorf("over-wide drag: width = %d, want clamp %d", got, hi)
	}
	m.SidebarEdgeMotion(0, top)
	if got := m.sidebarWidthPreference(); got != lo {
		t.Errorf("over-narrow drag: width = %d, want clamp %d", got, lo)
	}
	// A width inside the range is taken as the pointer column plus one.
	m.SidebarEdgeMotion(39, top)
	if got := m.sidebarWidthPreference(); got != 40 {
		t.Errorf("in-range drag: width = %d, want 40", got)
	}

	if !m.SidebarEdgeRelease(39, top) || m.SidebarEdgeActive() {
		t.Fatal("edge release did not end the resize")
	}
	data, err := os.ReadFile(filepath.Join(sidebarStateDir(), sidebarStateFileName))
	if err != nil {
		t.Fatalf("state file not written: %v", err)
	}
	var st sidebarStateFile
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatalf("state file: %v", err)
	}
	if st.Width != 40 {
		t.Errorf("persisted width = %d, want 40", st.Width)
	}

	// The stored width wins over the config default on load.
	m.Settings.SidebarWidth = config.SidebarDefaultWidth
	m2 := &OS{Settings: config.Global, Width: 120, Height: 40}
	m2.loadSidebarState()
	if got := m2.sidebarWidthPreference(); got != 40 {
		t.Errorf("loaded width = %d, want the stored 40", got)
	}
	if m2.Settings.SidebarWidth != config.SidebarDefaultWidth {
		t.Errorf("loading a stored width moved the config global to %d; it is process-wide and several sessions read it",
			m2.Settings.SidebarWidth)
	}
}
