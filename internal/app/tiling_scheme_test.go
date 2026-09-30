package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
)

// TestNewWorkspaceStartsWithTheConfiguredScheme: GetOrCreateBSPTree reads
// appearance.tiling_scheme (via m.Settings) only the moment a workspace's tree
// is first made. This is the config option's whole effect: everything after
// that first tree is the tree's own scheme (see AutoScheme on BSPTree).
func TestNewWorkspaceStartsWithTheConfiguredScheme(t *testing.T) {
	m := splitOS(t)
	if got := m.GetOrCreateBSPTree().AutoScheme; got != layout.SchemeSpiral {
		t.Fatalf("a fresh workspace with the default config started with %v, want spiral", got)
	}

	m.Settings.TilingScheme = config.TilingSchemeLongestSide
	m.CurrentWorkspace = 2
	if got := m.GetOrCreateBSPTree().AutoScheme; got != layout.SchemeLongestSide {
		t.Fatalf("a new workspace under appearance.tiling_scheme=longest_side started with %v, want longest_side", got)
	}

	// The first workspace already has a tree, so it keeps spiral: changing
	// the config default does not reach back and move it.
	m.CurrentWorkspace = 1
	if got := m.GetOrCreateBSPTree().AutoScheme; got != layout.SchemeSpiral {
		t.Fatalf("an already-tiled workspace changed scheme to %v when the config default changed", got)
	}
}

// TestCycleTilingSchemeStepsThroughAllFour: cycle_tiling_scheme walks spiral,
// longest_side, alternate, smart_split, and back to spiral, matching
// config.TilingSchemes. It does nothing when tiling is off.
func TestCycleTilingSchemeStepsThroughAllFour(t *testing.T) {
	m := splitOS(t)
	want := []layout.AutoScheme{
		layout.SchemeLongestSide,
		layout.SchemeAlternate,
		layout.SchemeSmartSplit,
		layout.SchemeSpiral,
	}
	for i, w := range want {
		name := m.CycleTilingScheme()
		if name == "" {
			t.Fatalf("step %d: CycleTilingScheme returned \"\" while tiling was on", i)
		}
		if got := m.WorkspaceTrees[m.CurrentWorkspace].AutoScheme; got != w {
			t.Fatalf("step %d: scheme = %v, want %v", i, got, w)
		}
		if name != w.String() {
			t.Fatalf("step %d: CycleTilingScheme returned %q, want %q", i, name, w.String())
		}
	}

	m.AutoTiling = false
	if name := m.CycleTilingScheme(); name != "" {
		t.Fatalf("CycleTilingScheme returned %q while tiling was off", name)
	}
}

// TestCycleTilingSchemeAppliesToNewSplits: a scheme set on the current
// workspace's tree is the scheme the next auto-placed split actually uses, not
// just a label on the tree.
func TestCycleTilingSchemeAppliesToNewSplits(t *testing.T) {
	m := splitOS(t) // one window, 200x60, workspace 1
	first := m.Windows[0]

	if !m.SetTilingScheme(layout.SchemeLongestSide) {
		t.Fatal("SetTilingScheme reported failure while tiling was on")
	}

	second := split(m, first.ID, layout.PreselectionNone)
	tree := m.WorkspaceTrees[m.CurrentWorkspace]
	parent := tree.WindowToNode[m.GetWindowIntID(second.ID)].Parent
	if parent == nil {
		t.Fatal("the new window has no parent split")
	}
	// The pane is 200x60: wider than it is tall even scaled for cell aspect,
	// so longest_side splits it vertically (left|right).
	if parent.SplitType != layout.SplitVertical {
		t.Fatalf("split type = %v under longest_side on a 200x60 pane, want vertical", parent.SplitType)
	}

	if !m.SetTilingScheme(layout.SchemeAlternate) {
		t.Fatal("SetTilingScheme reported failure while tiling was on")
	}
	third := split(m, second.ID, layout.PreselectionNone)
	thirdParent := tree.WindowToNode[m.GetWindowIntID(third.ID)].Parent
	// alternate keys off the tree's total split count (1 internal node so
	// far), which is odd, so it splits horizontally.
	if thirdParent.SplitType != layout.SplitHorizontal {
		t.Fatalf("split type = %v under alternate with one existing split, want horizontal", thirdParent.SplitType)
	}
}

// TestSetTilingSchemeFalseWhenTilingIsOff mirrors the other BSP setters
// (EqualizeSplits, RotateFocusedSplit): nothing happens, and nothing panics,
// with tiling off.
func TestSetTilingSchemeFalseWhenTilingIsOff(t *testing.T) {
	m := splitOS(t)
	m.AutoTiling = false
	if m.SetTilingScheme(layout.SchemeAlternate) {
		t.Fatal("SetTilingScheme reported success while tiling was off")
	}
}
