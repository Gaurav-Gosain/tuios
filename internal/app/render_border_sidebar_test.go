package app

import (
	"strings"
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
	origShared, origAnim := config.SharedBorders, config.AnimationsEnabled
	origStyle, origASCII := config.BorderStyle, config.UseASCIIOnly
	origEnabled, origPos := config.SidebarEnabled, config.SidebarPosition
	config.SharedBorders = true
	config.AnimationsEnabled = false
	config.BorderStyle = "rounded"
	config.UseASCIIOnly = false
	config.SidebarEnabled = side != ""
	if side != "" {
		config.SidebarPosition = side
	}
	t.Cleanup(func() {
		config.SharedBorders = origShared
		config.AnimationsEnabled = origAnim
		config.BorderStyle = origStyle
		config.UseASCIIOnly = origASCII
		config.SidebarEnabled = origEnabled
		config.SidebarPosition = origPos
	})

	m := &OS{
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
	for intID, rect := range tree.ApplyLayout(m.GetBSPBounds()) {
		if win := m.getWindowByIntID(intID); win != nil {
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

// TestSharedBorderSuppressesCapAtSidebarEdge is N8: with the sidebar on the
// right, the focused pane whose right edge sits against the sidebar must not draw
// a right-side corner cap there, because the sidebar already draws its edge rule
// one column over and the two read as a doubled border. The interior (left) caps
// must survive, so the suppression is targeted rather than dropping the outline.
func TestSharedBorderSuppressesCapAtSidebarEdge(t *testing.T) {
	border := func(m *OS) (tr, br, tl, bl string) {
		b := config.GetBorderForStyle()
		return b.TopRight, b.BottomRight, b.TopLeft, b.BottomLeft
	}

	withSidebar := sharedBorderSidebarOS(t, 3, "right")
	if withSidebar.GetRightMargin() <= 0 {
		t.Fatalf("right sidebar reserved no margin; content width %d", withSidebar.GetContentWidth())
	}
	focusRightmostPane(t, withSidebar)
	_, focused := separatorText(t, withSidebar)
	tr, br, tl, bl := border(withSidebar)
	if strings.Contains(focused, tr) || strings.Contains(focused, br) {
		t.Errorf("right sidebar: focused perimeter still caps at the sidebar edge (found %q or %q) in %q", tr, br, focused)
	}
	if !strings.Contains(focused, tl) && !strings.Contains(focused, bl) {
		t.Errorf("right sidebar: interior caps were dropped too; expected %q or %q in %q", tl, bl, focused)
	}

	// Regression guard: without a sidebar the same pane's right edge is the screen
	// edge, which must still cap. If it does not, the suppression is unconditional.
	noSidebar := sharedBorderSidebarOS(t, 3, "")
	focusRightmostPane(t, noSidebar)
	_, focusedNo := separatorText(t, noSidebar)
	tr2, br2, _, _ := border(noSidebar)
	if !strings.Contains(focusedNo, tr2) && !strings.Contains(focusedNo, br2) {
		t.Errorf("no sidebar: focused perimeter lost its screen-edge cap; expected %q or %q in %q", tr2, br2, focusedNo)
	}
}
