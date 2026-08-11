package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
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

// TestAccentSurvivesFocus pins that an accent stays visible on the focused
// window. The focus pill's saturated fill swallows a colored mark, so the
// focused row used to drop the accent entirely: setting a colour and then
// selecting the pane made the colour disappear.
func TestAccentSurvivesFocus(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")
	mark := accentMark()

	// "logs" carries no agent state, so its accent is the glyph. Focus it, which
	// renders the row as the focus pill.
	idx := m.windowIndexByID("cccccccc3333")
	if idx < 0 {
		t.Fatal("fixture window missing")
	}
	m.FocusWindow(idx)
	focused := m.Windows[idx]
	m.SetWindowAccent(focused.ID, 3)

	var row string
	for _, r := range railText(t, m) {
		if strings.Contains(r, printableTitle(windowRowTitle(focused))) {
			row = r
			break
		}
	}
	if row == "" {
		t.Fatalf("focused window row missing from the rail:\n%s", strings.Join(railText(t, m), "\n"))
	}
	if !strings.Contains(row, mark) {
		t.Errorf("focused row lost its accent: %q", row)
	}
}

// TestRailKeepsTheOldNameWhileRenaming: there is one rename surface, the
// dialog. The rail keeps drawing the name the window still has, so the two
// together are the old-vs-new comparison rather than two editors on one buffer.
func TestRailKeepsTheOldNameWhileRenaming(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")
	m.SidebarFocused = true // started from the rail

	m.BeginRenameWindow(m.Windows[2]) // "logs", not the focused window
	m.RenameBuffer = "audit"

	rows := strings.Join(railText(t, m), "\n")
	if strings.Contains(rows, "audit") {
		t.Errorf("the rail is still editing the buffer:\n%s", rows)
	}
	if !strings.Contains(rows, "logs") {
		t.Errorf("the rail dropped the name the window still has:\n%s", rows)
	}

	// The dialog carries the buffer.
	dialog, _, _, _, ok := m.renderRenameDialog()
	if !ok {
		t.Fatal("no rename dialog while a rename is in flight")
	}
	if !strings.Contains(stripANSIForTrace(dialog), "audit") {
		t.Errorf("the dialog is not showing the buffer: %q", dialog)
	}
}

// TestRenameDialogIsCentred pins the placement complaint: the dialog used to
// anchor to the rail row it renamed, which put it in the top-left corner. It
// belongs in the middle of the screen at every size, measured off the frame it
// actually draws rather than off the layout math that placed it.
func TestRenameDialogIsCentred(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {80, 24}, {60, 20}, {34, 12}} {
		w, h := size[0], size[1]
		m := sidebarTestOS(t, w, h, "left")
		m.SidebarFocused = true
		m.BeginRenameWindow(m.Windows[2])

		content, geo, x, y, ok := m.renderRenameDialog()
		if !ok {
			t.Fatalf("%dx%d: no rename dialog while a rename is in flight", w, h)
		}
		drawnW := lipgloss.Width(content)
		drawnH := lipgloss.Height(content)
		if drawnW != geo.Width || drawnH != geo.Height {
			t.Fatalf("%dx%d: the dialog draws %dx%d but reports %dx%d", w, h, drawnW, drawnH, geo.Width, geo.Height)
		}
		// Centred to within the odd-leftover cell on each axis.
		if slack := (w - drawnW) - 2*x; slack < 0 || slack > 1 {
			t.Errorf("%dx%d: dialog at x=%d is %d cells wide, off horizontal centre by %d", w, h, x, drawnW, slack)
		}
		if slack := (h - drawnH) - 2*y; slack < 0 || slack > 1 {
			t.Errorf("%dx%d: dialog at y=%d is %d rows tall, off vertical centre by %d", w, h, y, drawnH, slack)
		}
	}
}

// TestSidebarSignatureFoldsWhatTheRailDraws is the render-cache guard: the
// rail is served from a cache keyed by this signature, so any input the rows
// draw from has to move it or the row goes stale on screen - and anything the
// rows do not draw has to stay out, or it rebuilds them for nothing.
func TestSidebarSignatureFoldsWhatTheRailDraws(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")
	base := m.sidebarSignature()

	m.SetWindowAccent("cccccccc3333", 3)
	withAccent := m.sidebarSignature()
	if withAccent == base {
		t.Error("setting an accent left the signature unchanged, so the rail would keep the old row")
	}

	// A rename is deliberately absent from the signature: the buffer lives in
	// its own dialog and the rail draws the old name throughout, so typing must
	// not rebuild the rail once per keystroke.
	m.BeginRenameWindow(m.Windows[2])
	m.RenameBuffer = "a"
	if m.sidebarSignature() != withAccent {
		t.Error("starting a rename moved the signature, so typing rebuilds the whole rail")
	}
	m.RenameBuffer = "ab"
	if m.sidebarSignature() != withAccent {
		t.Error("typing into the rename buffer moved the signature")
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
