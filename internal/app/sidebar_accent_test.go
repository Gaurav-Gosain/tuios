package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// railText renders the rail and returns its rows with styling stripped, which is
// what a user actually reads.
func railText(t *testing.T, m *OS) []string {
	t.Helper()
	lines, _ := m.sidebarPanelLines()
	plain := make([]string, 0, len(lines))
	for _, l := range lines {
		plain = append(plain, stripANSIForTrace(l))
	}
	return plain
}

// TestRailDrawsTheAccentChip proves the accent reaches the screen: the row for
// an accented window wears the chip in its glyph column, and a window with an
// agent state keeps the state glyph, since state outranks identity.
func TestRailDrawsTheAccentChip(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")
	mark := accentMark()

	before := strings.Join(railText(t, m), "\n")
	if strings.Contains(before, mark) {
		t.Fatalf("precondition: the accent chip is on the rail before any accent was set\n%s", before)
	}

	// "logs" has no agent state; "editor" is working.
	m.SetWindowAccent("cccccccc3333", 1)
	m.SetWindowAccent("aaaaaaaa1111", 2)

	rows := railText(t, m)
	logsRow, editorRow := "", ""
	for _, r := range rows {
		switch {
		case strings.Contains(r, "logs"):
			logsRow = r
		case strings.Contains(r, "editor"):
			editorRow = r
		}
	}
	if logsRow == "" || editorRow == "" {
		t.Fatalf("window rows missing from the rail:\n%s", strings.Join(rows, "\n"))
	}
	if !strings.Contains(logsRow, mark) {
		t.Errorf("accented row does not show the chip: %q", logsRow)
	}
	if strings.Contains(editorRow, mark) {
		t.Errorf("accent overwrote an agent-state glyph: %q", editorRow)
	}
}

// TestRailDrawsTheRenameEditor proves an inline rename shows up on the row it
// targets, which is the whole point of renaming from the rail.
func TestRailDrawsTheRenameEditor(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")

	m.BeginRenameWindow(m.Windows[2]) // "logs", not the focused window
	m.RenameBuffer = "audit"

	rows := strings.Join(railText(t, m), "\n")
	if !strings.Contains(rows, "audit_") {
		t.Errorf("the rename editor is not on the rail:\n%s", rows)
	}
}

// TestSidebarSignatureFoldsAccentAndRename is the render-cache guard: the rail
// is served from a cache keyed by this signature, so any input the rows draw
// from has to move it or the row goes stale on screen.
func TestSidebarSignatureFoldsAccentAndRename(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")
	base := m.sidebarSignature()

	m.SetWindowAccent("cccccccc3333", 3)
	withAccent := m.sidebarSignature()
	if withAccent == base {
		t.Error("setting an accent left the signature unchanged, so the rail would keep the old row")
	}

	m.BeginRenameWindow(m.Windows[2])
	m.RenameBuffer = "a"
	renaming := m.sidebarSignature()
	if renaming == withAccent {
		t.Error("starting a rename left the signature unchanged")
	}
	m.RenameBuffer = "ab"
	if m.sidebarSignature() == renaming {
		t.Error("typing into the rename buffer left the signature unchanged")
	}
}

// TestAccentPickerPicksAndClears walks the picker the way the keys drive it:
// it opens on the accent the window already has, applying a row stores it, and
// the clear row takes it away.
func TestAccentPickerPicksAndClears(t *testing.T) {
	withSidebar(t, true, "left", config.SidebarDefaultWidth)
	m := &OS{}

	m.OpenAccentPicker("w1")
	if !m.ShowAccentPicker || m.AccentPickerSelected != accentSwatchCount {
		t.Fatalf("an unaccented window must open on the clear row, got %d", m.AccentPickerSelected)
	}

	m.AccentPickerApply(4)
	if m.ShowAccentPicker {
		t.Error("applying an accent left the picker open")
	}
	if idx, ok := m.WindowAccent("w1"); !ok || idx != 4 {
		t.Fatalf("accent = (%d, %v), want (4, true)", idx, ok)
	}

	m.OpenAccentPicker("w1")
	if m.AccentPickerSelected != 4 {
		t.Errorf("the picker opened on row %d, want the accent the window has (4)", m.AccentPickerSelected)
	}
	m.AccentPickerClear()
	if _, ok := m.WindowAccent("w1"); ok {
		t.Error("the clear row left the accent in place")
	}
}

// navIndexOfWindow returns the nav index of a window row, or -1.
func navIndexOfWindow(m *OS, id string) int {
	for i, r := range m.SidebarNav {
		if r.Kind == sidebarRowWindow && r.WindowID == id {
			return i
		}
	}
	return -1
}

// TestRailCursorRenamesAndAccents drives the two rail keys through the cursor:
// they act on the row the cursor is on, which need not be the focused pane, and
// they refuse on a session row rather than acting on the wrong thing.
func TestRailCursorRenamesAndAccents(t *testing.T) {
	m, _ := railOS(t)

	idx := navIndexOfWindow(m, "cccccccc3333") // "logs", not the focused window
	if idx < 0 {
		t.Fatal("the fixture has no window row to put the cursor on")
	}
	m.SidebarCursor = idx

	m.SidebarRenameCursor()
	if !m.RenamingWindow || m.RenameTargetID != "cccccccc3333" {
		t.Fatalf("rename targets %q (renaming=%v), want the cursor row's window", m.RenameTargetID, m.RenamingWindow)
	}
	m.EndRenameWindow()

	m.SidebarAccentCursor()
	if !m.ShowAccentPicker || m.AccentPickerWindowID != "cccccccc3333" {
		t.Fatalf("accent picker targets %q (open=%v), want the cursor row's window", m.AccentPickerWindowID, m.ShowAccentPicker)
	}
	m.CloseAccentPicker()

	// A session row is the daemon's to name; the rail must not rename some
	// window by accident because the cursor was elsewhere.
	m.SidebarCursor = navIndexOfSession(m, "main")
	m.SidebarRenameCursor()
	if m.RenamingWindow {
		t.Error("a session row started a window rename")
	}
	m.SidebarAccentCursor()
	if m.ShowAccentPicker {
		t.Error("a session row opened the window accent picker")
	}
}

// TestAccentSurvivesRestart is the real-user case: an accent set today is on the
// row after the client is restarted, because it is written to the sidebar state
// file the rest of the rail's layout already persists to.
func TestAccentSurvivesRestart(t *testing.T) {
	withSidebar(t, true, "left", config.SidebarDefaultWidth)

	m := &OS{}
	m.SetWindowAccent("w1", 5)

	next := &OS{}
	next.loadSidebarState()
	if idx, ok := next.WindowAccent("w1"); !ok || idx != 5 {
		t.Fatalf("accent after restart = (%d, %v), want (5, true)", idx, ok)
	}
}
