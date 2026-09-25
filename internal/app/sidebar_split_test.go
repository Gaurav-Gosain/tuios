package app

import (
	"strings"
	"testing"
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
