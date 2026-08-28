package app

import (
	"slices"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// newDockEditorOS builds a model with the editor open on the default layout.
func newDockEditorOS(t *testing.T) *OS {
	t.Helper()
	m := &OS{Settings: config.Global, Width: 120, Height: 44}
	m.UserConfig = config.DefaultConfig()
	m.ConfigReadOnly = true // applied live, never written to the real config file
	m.OpenDockEditor()
	return m
}

// selectDockRow puts the selection on the named component, failing if the
// editor is not showing it.
func selectDockRow(t *testing.T, m *OS, name string, kind dockRowKind) {
	t.Helper()
	for i, row := range m.dockEditorRows() {
		if row.Name == name && row.Kind == kind {
			m.DockEditorSelected = i
			return
		}
	}
	t.Fatalf("component %q is not on an editor row of kind %d", name, kind)
}

// TestDockEditorReordersWithinARegion checks a shift swaps two neighbours and
// that the selection follows the component rather than staying on the row.
func TestDockEditorReordersWithinARegion(t *testing.T) {
	m := newDockEditorOS(t)
	before := m.sideList("left")
	if len(before) < 2 {
		t.Fatal("the default left region needs two components for this test")
	}
	first, second := before[0], before[1]

	selectDockRow(t, m, first, dockRowComponent)
	m.DockEditorShift(1)

	after := m.sideList("left")
	if after[0] != second || after[1] != first {
		t.Errorf("left is %v, want %q and %q swapped", after, first, second)
	}
	if got := m.dockEditorRows()[m.DockEditorSelected].Name; got != first {
		t.Errorf("the selection is on %q, want it to have followed %q", got, first)
	}
}

// TestDockEditorCrossesIntoTheNextRegion checks a component shifted off the end
// of its region lands in the neighbouring one, which is what makes "put this on
// the left" one gesture rather than two controls.
func TestDockEditorCrossesIntoTheNextRegion(t *testing.T) {
	m := newDockEditorOS(t)
	left := m.sideList("left")
	last := left[len(left)-1]

	selectDockRow(t, m, last, dockRowComponent)
	m.DockEditorShift(1)

	if slices.Contains(m.sideList("left"), last) {
		t.Errorf("%q is still on the left", last)
	}
	center := m.sideList("center")
	if len(center) == 0 || center[0] != last {
		t.Errorf("center is %v, want %q at its head", center, last)
	}
}

// TestDockEditorRefusesToMoveAPinnedComponent checks a component with a fixed
// side is not moved out of it. Such a component draws on its own side whatever
// list names it, so the move would change the file and nothing on screen.
func TestDockEditorRefusesToMoveAPinnedComponent(t *testing.T) {
	m := newDockEditorOS(t)

	pinned := config.DockComponentWindows
	if config.DockFixedSide(pinned) != "center" {
		t.Skipf("%q is no longer pinned to the center", pinned)
	}
	selectDockRow(t, m, pinned, dockRowComponent)
	before := m.sideList("center")

	m.DockEditorShift(-1)

	if got := m.sideList("center"); !slices.Equal(got, before) {
		t.Errorf("center is %v, want the pinned component left where it was (%v)", got, before)
	}
	if slices.Contains(m.sideList("left"), pinned) {
		t.Errorf("%q was moved onto the left, where it cannot draw", pinned)
	}
	if len(m.Notifications) == 0 {
		t.Error("the refused move said nothing; an edit that appears to do nothing is the bug")
	}
}

// TestDockEditorRemovesAndAddsBack checks the round trip, so removing a
// component from inside the editor is not one-way.
func TestDockEditorRemovesAndAddsBack(t *testing.T) {
	m := newDockEditorOS(t)
	target := m.sideList("left")[0]

	selectDockRow(t, m, target, dockRowComponent)
	m.DockEditorToggle()
	if slices.Contains(m.sideList("left"), target) {
		t.Fatalf("%q is still placed after being removed", target)
	}

	selectDockRow(t, m, target, dockRowAvailable)
	m.DockEditorToggle()

	placed := slices.Concat(m.sideList("left"), m.sideList("center"), m.sideList("right"))
	if !slices.Contains(placed, target) {
		t.Errorf("%q did not come back onto the bar: %v", target, placed)
	}
}

// TestDockEditorAddsAPinnedComponentToItsOwnSide checks a component that can
// only draw on one side lands there, rather than in a list that will not draw
// it.
func TestDockEditorAddsAPinnedComponentToItsOwnSide(t *testing.T) {
	m := newDockEditorOS(t)

	pinned := config.DockComponentCopyHelp
	side := config.DockFixedSide(pinned)
	if side == "" {
		t.Skipf("%q is no longer pinned", pinned)
	}
	selectDockRow(t, m, pinned, dockRowComponent)
	m.DockEditorToggle() // off the bar
	selectDockRow(t, m, pinned, dockRowAvailable)
	m.DockEditorToggle() // and back

	if !slices.Contains(m.sideList(side), pinned) {
		t.Errorf("%q went somewhere other than the %s it is pinned to", pinned, side)
	}
}

// TestDockEditorRevertLeavesAnUntouchedListUnset is the config-file half of
// undo: a layout nobody changed must not be written out as three explicit
// lists, which would pin the user to today's defaults in a file that named
// none.
func TestDockEditorRevertLeavesAnUntouchedListUnset(t *testing.T) {
	m := newDockEditorOS(t)
	if m.UserConfig.Dock.Left != nil {
		t.Skip("the fixture config already names the dock lists")
	}

	target := m.sideList("left")[0]
	selectDockRow(t, m, target, dockRowComponent)
	m.DockEditorToggle()
	if m.UserConfig.Dock.Left == nil {
		t.Fatal("an edit did not write the left list")
	}

	m.DockEditorRevert()
	if m.UserConfig.Dock.Left != nil {
		t.Errorf("undo left the list written out as %v, want it unset again",
			*m.UserConfig.Dock.Left)
	}
}

// TestDockEditorResetRestoresTheDefaults checks the reset key puts back what the
// bar draws with no [dock] table at all.
func TestDockEditorResetRestoresTheDefaults(t *testing.T) {
	m := newDockEditorOS(t)
	empty := []string{}
	m.UserConfig.Dock.Left = &empty

	m.DockEditorReset()

	if got := m.sideList("left"); !slices.Equal(got, config.DefaultDockLeft()) {
		t.Errorf("left is %v, want the default %v", got, config.DefaultDockLeft())
	}
}

// TestDockEditorSelectionSkipsHeaders checks the region headings are not landed
// on. A heading is a label, and stopping on one costs a keystroke on the way
// past it.
func TestDockEditorSelectionSkipsHeaders(t *testing.T) {
	m := newDockEditorOS(t)
	rows := m.dockEditorRows()
	for range rows {
		if rows[m.DockEditorSelected].Kind == dockRowHeader {
			t.Fatalf("the selection landed on a header at row %d", m.DockEditorSelected)
		}
		m.DockEditorMove(1)
		rows = m.dockEditorRows()
	}
}

// TestDockEditorEmptyRegionStaysEmpty checks a region the user emptied is
// recorded as an empty list rather than as unset, which DockList would read
// back as "the default" and fill up again.
func TestDockEditorEmptyRegionStaysEmpty(t *testing.T) {
	m := newDockEditorOS(t)
	for _, name := range m.sideList("center") {
		selectDockRow(t, m, name, dockRowComponent)
		m.DockEditorToggle()
	}
	if got := m.sideList("center"); len(got) != 0 {
		t.Errorf("center reads back as %v after being emptied", got)
	}
}
