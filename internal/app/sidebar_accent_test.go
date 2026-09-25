package app

import (
	"strings"
	"testing"
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

// TestSidebarSignatureFoldsWhatTheRailDraws is the render-cache guard: the
// rail is served from a cache keyed by this signature, so any input the rows
// draw from has to move it or the row goes stale on screen, and anything the
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
