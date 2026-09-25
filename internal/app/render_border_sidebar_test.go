package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// sharedBorderSidebarOS builds the shared-border model of sharedBorderOS with the
// sidebar reserving a margin on the given side ("left", "right", or "" for none),
// so the separator overlay is exercised against a real content region rather than
// the full screen.
func sharedBorderSidebarOS(t *testing.T, n int, side string) *OS {
	t.Helper()
	origShared, origAnim := config.Global.SharedBorders, config.Global.AnimationsEnabled
	origStyle, origASCII := config.Global.BorderStyle, config.Global.UseASCIIOnly
	origEnabled, origPos := config.Global.SidebarEnabled, config.Global.SidebarPosition
	origDock := config.Global.DockbarPosition
	// The dock's hairline closes the content region from below, and a divider
	// that reaches it meets it there, so pin the side it is on.
	config.Global.DockbarPosition = "bottom"
	config.Global.SharedBorders = true
	config.Global.AnimationsEnabled = false
	config.Global.BorderStyle = "rounded"
	config.Global.UseASCIIOnly = false
	config.Global.SidebarEnabled = side != ""
	if side != "" {
		config.Global.SidebarPosition = side
	}
	t.Cleanup(func() {
		config.Global.SharedBorders = origShared
		config.Global.AnimationsEnabled = origAnim
		config.Global.BorderStyle = origStyle
		config.Global.UseASCIIOnly = origASCII
		config.Global.SidebarEnabled = origEnabled
		config.Global.SidebarPosition = origPos
		config.Global.DockbarPosition = origDock
	})

	m := &OS{
		Settings: config.Global,
		// The layout reads the model's session-settled geometry, seeded from
		// the globals the way NewOS seeds it.
		SharedBorders:    config.Global.SharedBorders,
		PaneGap:          config.Global.PaneGap,
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		WorkspaceFocus:   make(map[int]int),
		Width:            120,
		Height:           40,
		AutoTiling:       true,
		UseBSPLayout:     true,
		FocusedWindow:    0,
	}
	for i := range n {
		m.Windows = append(m.Windows, &terminal.Window{
			ID:        "win-" + string(rune('a'+i)),
			Workspace: 1,
			Width:     120,
			Height:    40,
			Tiled:     true,
		})
	}
	m.TileAllWindows()

	tree := m.WorkspaceTrees[m.CurrentWorkspace]
	if tree == nil {
		t.Fatal("sharedBorderSidebarOS: no BSP tree")
	}
	for intID, rect := range tree.ApplyLayout(m.GetBSPBounds(), m.separatorGap()) {
		if win := m.GetWindowByIntID(intID); win != nil {
			win.X, win.Y, win.Width, win.Height = rect.X, rect.Y, rect.W, rect.H
		}
	}
	return m
}

// focusRightmostPane focuses the window whose right edge reaches the content's
// right boundary, and returns it. That pane's right edge is exactly where a
// right-side sidebar draws its own edge rule, so it is the one that exposes the
// doubled-border bug.
func focusRightmostPane(t *testing.T, m *OS) *terminal.Window {
	t.Helper()
	bounds := m.GetBSPBounds()
	edge := bounds.X + bounds.W
	for i, w := range m.Windows {
		if w.X+w.Width == edge {
			m.FocusedWindow = i
			return w
		}
	}
	t.Fatalf("no pane reaches the content right boundary %d", edge)
	return nil
}
