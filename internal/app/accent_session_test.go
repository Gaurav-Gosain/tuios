package app

import (
	"reflect"
	"testing"
)

// TestSessionAccentApplyWithoutMovingChangesNothing holds the same rule one
// level up: the picker opens on the automatic colour, so applying it untouched
// must not pin the session to that hue and take it out of the arbitration.
func TestSessionAccentApplyWithoutMovingChangesNothing(t *testing.T) {
	withSessionColors(t, true)
	m, _ := accentInheritOS(t)

	m.OpenSessionAccentPicker("main")
	if cmd := m.AccentPickerApply(); cmd != nil {
		t.Error("applying without moving wrote the automatic colour back as an explicit accent")
	}
	if m.ShowAccentPicker {
		t.Error("applying left the picker open")
	}
}

// TestSessionAccentEntryPointsSeedIdentically: the rail's accent key on a
// session row and the row's context menu open the same picker on the same
// colour. The key used to refuse, because a session had nothing to set.
func TestSessionAccentEntryPointsSeedIdentically(t *testing.T) {
	withSessionColors(t, true)
	m, _ := accentInheritOS(t)

	m.OpenSessionAccentPicker("main")
	direct := m.AccentPicker
	m.CloseAccentPicker()

	idx := navIndexOfSession(m, "main")
	if idx < 0 {
		t.Fatal("the rail has no session row to put the cursor on")
	}
	m.SidebarCursor = idx
	m.SidebarAccentCursor()
	if m.AccentPickerTarget != AccentTargetSession || m.AccentPickerTargetID != "main" {
		t.Fatalf("the rail's accent key targeted %v %q", m.AccentPickerTarget, m.AccentPickerTargetID)
	}
	if got := m.AccentPicker; !reflect.DeepEqual(got, direct) {
		t.Errorf("the rail's accent key seeded %+v, want %+v", got, direct)
	}
	m.CloseAccentPicker()

	// The session row's menu offers it, and carries which session was clicked so
	// the action does not fall back to the attached one. The menu asks the daemon
	// what else is running, which this fixture has no socket for.
	m.DaemonClient = nil
	title, items := m.sessionMenu("docs")
	if title != "docs" {
		t.Errorf("the menu is headed %q, want the session the row names", title)
	}
	found := false
	for _, it := range items {
		if it.Action == "set_session_accent" {
			found = true
		}
	}
	if !found {
		t.Error("the session row's context menu has no accent row")
	}
}

// TestPinningASessionSettlesTheOthers: an explicit accent is reserved, so no
// automatic colour is handed out in that hue, and a session that has to move
// moves once. Repeating the arbitration on unchanged input must land in the same
// place, or the rail would flip colours on every render.
func TestPinningASessionSettlesTheOthers(t *testing.T) {
	withSessionColors(t, true)
	m, tree := accentInheritOS(t)

	// Pin the attached session to the hue one of the others prefers, so the
	// collision is real rather than hypothetical.
	var clash Accent
	for _, name := range []string{"api", "docs"} {
		if a, ok := m.SessionColor(name); ok {
			clash = a
			break
		}
	}
	m.SessionAccent = clash.Hex()

	first := map[string]Accent{}
	for range 3 {
		m.sidebarPanelLinesForTree(tree)
		got := map[string]Accent{}
		for _, name := range []string{"api", "docs"} {
			a, _ := m.SessionColor(name)
			got[name] = a
			if a == clash {
				t.Errorf("%s was handed the hue the pinned session holds (%s)", name, a.Hex())
			}
		}
		if len(first) == 0 {
			first = got
			continue
		}
		for name, a := range got {
			if first[name] != a {
				t.Errorf("%s moved from %s to %s on a re-render with nothing changed", name, first[name].Hex(), a.Hex())
			}
		}
	}
}

// TestSessionAccentPreviewsOnTheRail: the picker previews on the rows that wear
// the colour, which for a session is its own row and every pane inheriting from
// it, so the choice is made against what it will look like.
func TestSessionAccentPreviewsOnTheRail(t *testing.T) {
	withSessionColors(t, true)
	m, _ := accentInheritOS(t)

	m.OpenSessionAccentPicker("main")
	m.AccentPickerHueCell(5)
	m.AccentPickerCell(7, 2)
	want := RGBAccent(m.AccentPicker.Cur)
	if got, ok := m.SessionColor("main"); !ok || got != want {
		t.Errorf("the session reads as %+v while the picker is on %s", got, want.Hex())
	}
	m.CloseAccentPicker()
	if got, _ := m.SessionColor("main"); got == want {
		t.Error("closing the picker left the previewed colour behind")
	}
}
