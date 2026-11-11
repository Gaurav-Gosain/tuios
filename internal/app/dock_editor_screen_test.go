package app_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/apptest"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The dock editor, driven the way a person drives it: opened from the
// settings row the palette finds, with the arrows, shift and an arrow, and
// enter, and read off the panel.

// dockEditorTitle is the start of the editor's title.
const dockEditorTitle = "Dock layout"

// dockRegionLabels are the editor's region headings, and the region each
// names.
var dockRegionLabels = map[string]string{
	"Left": "left", "Center": "center", "Right": "right", "Not on the bar": "available",
}

// openDockEditor opens the editor as the e2e test does: the palette finds the
// Components row of settings, and enter on it opens the editor.
func openDockEditor(t *testing.T, f *apptest.Fixture) {
	t.Helper()
	f.Keys("ctrl+p")
	f.Type("components")
	f.Keys("enter", "enter")
	if _, _, ok := f.Find(dockEditorTitle); !ok {
		t.Fatalf("the dock editor did not open:\n%s", f.Text())
	}
}

// dockRow is one row of the editor: its region and the component on it,
// "nothing here" for an empty region.
type dockRow struct{ region, name string }

// selectedDockRow reads the selected row off the panel: the row with the
// cursor mark, and the region heading nearest above it, "" when the panel has
// scrolled that heading off.
func selectedDockRow(t *testing.T, f *apptest.Fixture) dockRow {
	t.Helper()
	box := dockEditorBox(t, f)
	region := ""
	for y, row := range f.Screen() {
		if y <= box.Y0 || y >= box.Y1 {
			continue
		}
		text := strings.TrimSpace(panelRow(row, box))
		if label, ok := dockHeading(text); ok {
			region = dockRegionLabels[label]
			continue
		}
		if name, ok := strings.CutPrefix(text, "› "); ok {
			name, _, _ = strings.Cut(name, "  ")
			return dockRow{region, strings.TrimSpace(name)}
		}
	}
	t.Fatalf("no selected row in the dock editor:\n%s", f.Text())
	return dockRow{}
}

// walkDock moves the selection from the top of the editor down one row at a
// time and hands each row to visit until visit returns false or the
// selection comes back to a row it has been on: the last row, or the first
// one again where the list wraps. The editor scrolls, so a row whose heading
// is off screen keeps the region of the row before it; the first row is in
// the first region.
func walkDock(t *testing.T, f *apptest.Fixture, visit func(dockRow) bool) {
	t.Helper()
	f.Keys("home")
	region := "left"
	seen := map[dockRow]bool{}
	for range 64 {
		row := selectedDockRow(t, f)
		if row.region == "" {
			row.region = region
		}
		region = row.region
		if seen[row] || !visit(row) {
			return
		}
		seen[row] = true
		f.Keys("down")
	}
	t.Fatal("the dock editor's selection never came back to a row it had been on")
}

// dockHeading reports whether a panel row is a region heading: a label
// followed by a rule.
func dockHeading(text string) (string, bool) {
	for label := range dockRegionLabels {
		if rest, ok := strings.CutPrefix(text, label+" "); ok && strings.Trim(rest, "─━-") == "" && rest != "" {
			return label, true
		}
	}
	return "", false
}

// panelRow is the part of a screen row inside the panel's frame.
func panelRow(row string, box apptest.Box) string {
	r := []rune(row)
	if box.X0+1 >= len(r) {
		return ""
	}
	return string(r[box.X0+1 : min(box.X1, len(r))])
}

func dockEditorBox(t *testing.T, f *apptest.Fixture) apptest.Box {
	t.Helper()
	// The panel's title is on the first row inside its frame.
	screen := f.Screen()
	whole, _ := f.Panels()
	for _, b := range whole {
		if strings.HasPrefix(strings.TrimSpace(panelRow(screen[b.Y0+1], b)), dockEditorTitle) {
			return b
		}
	}
	t.Fatalf("no whole dock editor panel on screen:\n%s", f.Text())
	return apptest.Box{}
}

// dockRegions reads what each region holds, in order, by walking the
// editor. The selection is left on the last row.
func dockRegions(t *testing.T, f *apptest.Fixture) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	walkDock(t, f, func(row dockRow) bool {
		if row.name == "nothing here" {
			out[row.region] = []string{}
		} else {
			out[row.region] = append(out[row.region], row.name)
		}
		return true
	})
	return out
}

// selectDock puts the selection on a component in a region by pressing the
// arrows, and fails when the editor does not show it there.
func selectDock(t *testing.T, f *apptest.Fixture, region, name string) {
	t.Helper()
	found := false
	walkDock(t, f, func(row dockRow) bool {
		found = row == dockRow{region, name}
		return !found
	})
	if !found {
		t.Fatalf("%q is not on a row of the %s region:\n%s", name, region, f.Text())
	}
}

func dockFixture(t *testing.T) *apptest.Fixture {
	t.Helper()
	// Framed panels, so the editor's edges can be found over the settings
	// panel it opens on.
	f := apptest.New(t, apptest.Panes(1), apptest.Size(120, 44),
		apptest.Config(func(c *config.UserConfig) { c.Appearance.FramePanels = true }))
	openDockEditor(t, f)
	return f
}

// TestDockEditorReordersWithinARegion: shift and down swaps a component with
// the next one in its region, and the selection follows the component rather
// than staying on the row. Negative control: make DockEditorShift swap and
// skip selectDockComponent, and the selection check fails.
func TestDockEditorReordersWithinARegion(t *testing.T) {
	f := dockFixture(t)
	before := dockRegions(t, f)["left"]
	if len(before) < 2 {
		t.Fatalf("the default left region needs two components for this test, it has %v", before)
	}
	first, second := before[0], before[1]

	selectDock(t, f, "left", first)
	f.Keys("J")

	if got := selectedDockRow(t, f); got.name != first {
		t.Errorf("the selection is on %v, want it to have followed %q", got, first)
	}
	if after := dockRegions(t, f)["left"]; len(after) < 2 || after[0] != second || after[1] != first {
		t.Errorf("left is %v, want %q and %q swapped", after, first, second)
	}
}

// TestDockEditorCrossesIntoTheNextRegion: a component moved off the end of
// its region lands at the head of the next one, which is what makes "put
// this in the center" one gesture rather than two controls. Negative
// control: return before the crossing in DockEditorShift.
func TestDockEditorCrossesIntoTheNextRegion(t *testing.T) {
	f := dockFixture(t)
	left := dockRegions(t, f)["left"]
	last := left[len(left)-1]

	selectDock(t, f, "left", last)
	f.Keys("J")

	regions := dockRegions(t, f)
	if slices.Contains(regions["left"], last) {
		t.Errorf("%q is still on the left: %v", last, regions["left"])
	}
	if center := regions["center"]; len(center) == 0 || center[0] != last {
		t.Errorf("center is %v, want %q at its head", center, last)
	}
}

// TestDockEditorRefusesToMoveAPinnedComponent: a component with a fixed side
// is not moved out of it, and the editor says why. Such a component draws on
// its own side whatever list names it, so the move would change the file and
// nothing on screen. Negative control: drop the Fixed check in
// DockEditorShift.
func TestDockEditorRefusesToMoveAPinnedComponent(t *testing.T) {
	f := dockFixture(t)
	pinned := config.DockComponentWindows
	if config.DockFixedSide(pinned) != "center" {
		t.Fatalf("%q is no longer pinned to the center; pick another pinned component", pinned)
	}
	before := dockRegions(t, f)

	selectDock(t, f, "center", pinned)
	if before["center"][0] != pinned {
		t.Fatalf("%q is not at the head of the center (%v), so shift and up would only reorder it", pinned, before["center"])
	}
	f.Keys("K")

	after := dockRegions(t, f)
	if !slices.Equal(after["center"], before["center"]) {
		t.Errorf("center is %v, want the pinned component left where it was (%v)", after["center"], before["center"])
	}
	if slices.Contains(after["left"], pinned) {
		t.Errorf("%q was moved onto the left, where it cannot draw", pinned)
	}
	if _, _, ok := f.Find(pinned + " always draws on the center"); !ok {
		t.Errorf("the refused move said nothing; an edit that appears to do nothing is the bug:\n%s", f.Text())
	}
}

// TestDockEditorRemovesAndAddsBack: enter takes a component off the bar and
// enter on it under "Not on the bar" puts it back, so a removal from inside
// the editor is not one-way. Negative control: return early from the
// available case of DockEditorToggle.
func TestDockEditorRemovesAndAddsBack(t *testing.T) {
	f := dockFixture(t)
	target := dockRegions(t, f)["left"][0]

	selectDock(t, f, "left", target)
	f.Keys("enter")
	if regions := dockRegions(t, f); slices.Contains(regions["left"], target) || !slices.Contains(regions["available"], target) {
		t.Fatalf("%q was not taken off the bar: %v", target, regions)
	}

	selectDock(t, f, "available", target)
	f.Keys("enter")
	regions := dockRegions(t, f)
	placed := slices.Concat(regions["left"], regions["center"], regions["right"])
	if !slices.Contains(placed, target) {
		t.Errorf("%q did not come back onto the bar: %v", target, regions)
	}
}

// TestDockEditorAddsAPinnedComponentToItsOwnSide: a component that can only
// draw on one side goes back there, rather than into a list that will not
// draw it. The component is one pinned to the center, because an unpinned
// one goes back on the right, so a right-pinned one would pass either way.
// Negative control: ignore row.Fixed in DockEditorToggle.
func TestDockEditorAddsAPinnedComponentToItsOwnSide(t *testing.T) {
	f := dockFixture(t)
	pinned := config.DockComponentWindows
	side := config.DockFixedSide(pinned)
	if side == "" || side == "right" {
		t.Fatalf("%q is pinned to %q; the test needs a component pinned elsewhere than the right", pinned, side)
	}

	selectDock(t, f, side, pinned)
	f.Keys("enter") // off the bar
	selectDock(t, f, "available", pinned)
	f.Keys("enter") // and back

	if regions := dockRegions(t, f); !slices.Contains(regions[side], pinned) {
		t.Errorf("%q went somewhere other than the %s it is pinned to: %v", pinned, side, regions)
	}
}

// TestDockEditorUndoLeavesAnUntouchedListUnset is the config-file half of
// undo: u puts the layout back, and a list nobody had named is not written
// out as an explicit copy of today's default, which would pin the person to
// it in a file that named none. The file is not on screen, so the test reads
// the model's config for that half. Negative control: write the left list
// out after DockEditorRevert restores it.
func TestDockEditorUndoLeavesAnUntouchedListUnset(t *testing.T) {
	f := dockFixture(t)
	if f.M.UserConfig.Dock.Left != nil {
		t.Fatal("the default config already names the dock lists; the test proves nothing")
	}
	before := dockRegions(t, f)

	selectDock(t, f, "left", before["left"][0])
	f.Keys("enter")
	if f.M.UserConfig.Dock.Left == nil {
		t.Fatal("an edit did not write the left list")
	}

	f.Keys("u")
	if got := dockRegions(t, f); !slices.Equal(got["left"], before["left"]) {
		t.Errorf("undo left the left region as %v, want %v", got["left"], before["left"])
	}
	if f.M.UserConfig.Dock.Left != nil {
		t.Errorf("undo left the list written out as %v, want it unset again", *f.M.UserConfig.Dock.Left)
	}
}

// TestDockEditorEmptyRegionStaysEmpty: a region the person emptied stays
// empty, rather than reading back as unset, which the dock takes for "the
// default" and fills up again. Negative control: store an emptied list as
// unset in writeSide.
func TestDockEditorEmptyRegionStaysEmpty(t *testing.T) {
	f := dockFixture(t)
	for _, name := range dockRegions(t, f)["center"] {
		selectDock(t, f, "center", name)
		f.Keys("enter")
	}
	if got := dockRegions(t, f)["center"]; len(got) != 0 {
		t.Errorf("center reads back as %v after being emptied", got)
	}
}
