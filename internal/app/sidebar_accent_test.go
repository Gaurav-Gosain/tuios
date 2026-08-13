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
	m.SetWindowAccent("cccccccc3333", SlotAccent(1))
	m.SetWindowAccent("aaaaaaaa1111", SlotAccent(2))

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

	// "logs" carries no agent state, so its accent is the glyph. Focus it,
	// which used to render the row as a saturated focus pill that swallowed a
	// coloured mark; now the focused pane's own gutter mark burns the accent
	// instead of drawing a separate chip beside it.
	idx := m.windowIndexByID("cccccccc3333")
	if idx < 0 {
		t.Fatal("fixture window missing")
	}
	m.FocusWindow(idx)
	focused := m.Windows[idx]
	accent := SlotAccent(3)
	m.SetWindowAccent(focused.ID, accent)

	lines, _ := m.sidebarPanelLines()
	var row string
	for _, l := range lines {
		if strings.Contains(stripANSIForTrace(l), printableTitle(windowRowTitle(focused))) {
			row = l
			break
		}
	}
	if row == "" {
		t.Fatalf("focused window row missing from the rail")
	}
	if !strings.Contains(row, fgSeq(accent.Color())) {
		t.Errorf("focused row's gutter does not burn the accent colour: %q", row)
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

	m.SetWindowAccent("cccccccc3333", SlotAccent(3))
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
// applying stores the colour under the cursor, reopening starts from it, and
// the clear key takes it away.
func TestAccentPickerPicksAndClears(t *testing.T) {
	withSidebar(t, true, "left", config.SidebarDefaultWidth)
	truecolorForTest(t)
	m := &OS{}
	m.Width, m.Height = 120, 40

	m.OpenAccentPicker("w1")
	if !m.ShowAccentPicker {
		t.Fatal("the picker did not open")
	}
	if m.AccentPicker.HadPrev {
		t.Error("an unaccented window opened the picker claiming a previous accent")
	}

	m.AccentPickerCell(6, 2)
	want := m.AccentPicker.Cur
	m.AccentPickerApply()
	if m.ShowAccentPicker {
		t.Error("applying an accent left the picker open")
	}
	got, ok := m.WindowAccent("w1")
	if !ok || got.RGB() != want {
		t.Fatalf("accent = %+v, want the colour under the cursor %s", got, hexString(want))
	}

	m.OpenAccentPicker("w1")
	if m.AccentPicker.Cur != want {
		t.Errorf("the picker reopened on %s, want the accent the window has (%s)",
			hexString(m.AccentPicker.Cur), hexString(want))
	}
	m.AccentPickerClear()
	if _, ok := m.WindowAccent("w1"); ok {
		t.Error("the clear key left the accent in place")
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
// a session row renames the session rather than some window the cursor is not
// on.
func TestRailCursorRenamesAndAccents(t *testing.T) {
	m, _ := railOS(t)

	idx := navIndexOfWindow(m, "cccccccc3333") // "logs", not the focused window
	if idx < 0 {
		t.Fatal("the fixture has no window row to put the cursor on")
	}
	m.SidebarCursor = idx

	m.SidebarRenameCursor()
	if !m.Renaming() || m.RenameTargetID != "cccccccc3333" {
		t.Fatalf("rename targets %q (renaming=%v), want the cursor row's window", m.RenameTargetID, m.Renaming())
	}
	m.EndRename()

	m.SidebarAccentCursor()
	if !m.ShowAccentPicker || m.AccentPickerTargetID != "cccccccc3333" {
		t.Fatalf("accent picker targets %q (open=%v), want the cursor row's window", m.AccentPickerTargetID, m.ShowAccentPicker)
	}
	m.CloseAccentPicker()

	// A session row renames the session's label, never a window: the rail must
	// not rename some pane by accident because the cursor was elsewhere.
	m.SidebarCursor = navIndexOfSession(m, "main")
	m.SidebarRenameCursor()
	if m.RenameKind == RenameWindow {
		t.Error("a session row started a window rename")
	}
	if m.DaemonClient != nil && m.RenameKind != RenameSession {
		t.Errorf("a session row started %v, want a session rename", m.RenameKind)
	}
	m.EndRename()
	m.SidebarAccentCursor()
	if m.AccentPickerTarget != AccentTargetSession || m.AccentPickerTargetID != "main" {
		t.Errorf("a session row opened the picker on %v %q, want the session's own colour",
			m.AccentPickerTarget, m.AccentPickerTargetID)
	}
}
