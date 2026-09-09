package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// dividerHit is the divider's rectangle, or ok false when none was drawn.
func dividerHit(m *OS) (sidebarRowHit, bool) {
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowDivider {
			return h, true
		}
	}
	return sidebarRowHit{}, false
}

// agentsHeaderRow is the rail-relative line the agents header was drawn on.
func agentsHeaderRow(t *testing.T, lines []string) int {
	t.Helper()
	for i, ln := range lines {
		if strings.Contains(stripANSIForTrace(ln), "agents") {
			return i
		}
	}
	t.Fatal("no agents header drawn")
	return -1
}

// TestDividerDragMovesTheSplitAndPersists: dragging the divider down hides
// agent rows and the share survives in sidebar.json.
func TestDividerDragMovesTheSplitAndPersists(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 40)
	// Enough agents that the block can grow and shrink.
	for i := range 6 {
		tree.Sessions[1].Children = append(tree.Sessions[1].Children, tree.Sessions[1].Children[0])
		tree.Sessions[1].Children[len(tree.Sessions[1].Children)-1].ID = "agent-" + string(rune('a'+i))
	}
	lines, _ := m.sidebarPanelLinesForTree(tree)
	div, ok := dividerHit(m)
	if !ok {
		t.Fatal("the rail drew no divider above the pinned section")
	}
	if div.Y0-m.GetTopMargin() != agentsHeaderRow(t, lines)-1 {
		t.Fatalf("the divider is on row %d, want the row above the agents header (%d)", div.Y0-m.GetTopMargin(), agentsHeaderRow(t, lines)-1)
	}
	if !m.SidebarClick(div.X0+5, div.Y0, false) || !m.SidebarSplitActive() {
		t.Fatal("a press on the divider did not arm the drag")
	}
	// Drag it down eight rows: the block gets fewer lines.
	m.SidebarSplitMotion(div.X0+5, div.Y0+8)
	if m.SidebarSectionSplit == 0 {
		t.Fatal("the drag set no split")
	}
	m.SidebarSplitRelease(div.X0+5, div.Y0+8)
	lines, _ = m.sidebarPanelLinesForTree(tree)
	after, _ := dividerHit(m)
	if after.Y0 <= div.Y0 {
		t.Fatalf("after dragging down the divider is on row %d, was %d; want it lower", after.Y0, div.Y0)
	}

	data, err := os.ReadFile(filepath.Join(sidebarStateDir(), sidebarStateFileName))
	if err != nil {
		t.Fatalf("sidebar.json was not written: %v", err)
	}
	var st sidebarStateFile
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatalf("sidebar.json: %v", err)
	}
	if st.SectionSplit != m.SidebarSectionSplit {
		t.Fatalf("sidebar.json section_split = %d, want %d", st.SectionSplit, m.SidebarSectionSplit)
	}

	// A fresh model reads it back.
	fresh := &OS{}
	fresh.loadSidebarState()
	if fresh.SidebarSectionSplit != m.SidebarSectionSplit {
		t.Fatalf("a fresh model loaded section_split %d, want %d", fresh.SidebarSectionSplit, m.SidebarSectionSplit)
	}
}

// TestDividerDoubleClickResets: two presses inside the double-click window
// put the split back on the layout's share, and enter on the divider does the
// same from the keyboard.
func TestDividerDoubleClickResets(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 40)
	m.SidebarSectionSplit = 60
	m.sidebarPanelLinesForTree(tree)
	div, ok := dividerHit(m)
	if !ok {
		t.Fatal("no divider drawn")
	}
	m.SidebarClick(div.X0+3, div.Y0, false)
	m.sidebarSplit.PressAt = time.Now().Add(-sidebarSplitDoubleClick / 2)
	m.SidebarClick(div.X0+3, div.Y0, false)
	if m.SidebarSectionSplit != 0 {
		t.Fatalf("a double-click left the split at %d, want 0", m.SidebarSectionSplit)
	}
	if m.SidebarSplitActive() {
		t.Fatal("a double-click left a drag armed")
	}

	m.SidebarSectionSplit = 60
	m.SidebarFocused = true
	m.sidebarPanelLinesForTree(tree)
	for i, r := range m.SidebarNav {
		if r.Kind == sidebarRowDivider {
			m.SidebarCursor = i
		}
	}
	if !m.SidebarCursorOnDivider() {
		t.Fatal("the cursor could not reach the divider")
	}
	m.SidebarActivateCursor()
	if m.SidebarSectionSplit != 0 {
		t.Fatalf("enter on the divider left the split at %d, want 0", m.SidebarSectionSplit)
	}
}

// TestDividerKeysStepTheSplit: with the cursor on the divider, widen and
// narrow move the share by one step each way, from the layout's own share.
func TestDividerKeysStepTheSplit(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 40)
	m.SidebarFocused = true
	m.sidebarPanelLinesForTree(tree)
	base := m.sidebarSplitEffective()
	if base == 0 {
		t.Fatal("the default layout pins no section")
	}
	m.SidebarSplitStep(1)
	if m.SidebarSectionSplit != base+sidebarSplitStep {
		t.Fatalf("one step up = %d, want %d", m.SidebarSectionSplit, base+sidebarSplitStep)
	}
	m.SidebarSplitStep(-2)
	if m.SidebarSectionSplit != base-sidebarSplitStep {
		t.Fatalf("two steps down = %d, want %d", m.SidebarSectionSplit, base-sidebarSplitStep)
	}
	for range 40 {
		m.SidebarSplitStep(1)
	}
	if m.SidebarSectionSplit != sidebarSplitMax {
		t.Fatalf("the share ran past its ceiling: %d", m.SidebarSectionSplit)
	}
}

// TestDividerNeedsSomethingToSplit: a rail drawing only the pinned section
// has no divider, and neither does a layout that pins nothing.
func TestDividerNeedsSomethingToSplit(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 40)
	m.Settings.SidebarSections = "agents"
	m.sidebarPanelLinesForTree(tree)
	if _, ok := dividerHit(m); ok {
		t.Fatal("a rail of one section drew a divider")
	}
	m.Settings.SidebarSections = "sessions,terminals,agents,spacer"
	m.sidebarPanelLinesForTree(tree)
	if _, ok := dividerHit(m); ok {
		t.Fatal("a layout ending in a spacer pins nothing and still drew a divider")
	}
}
