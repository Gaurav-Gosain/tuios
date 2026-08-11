package app

import (
	"strings"
	"testing"
)

// sessionRowText is the drawn session row for name, without ANSI or padding.
func sessionRowText(t *testing.T, m *OS, name string) string {
	t.Helper()
	lines, _ := m.sidebarPanelLines()
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowSession && h.SessionID == name {
			return rowContent(lines[h.Y0-m.GetTopMargin()])
		}
	}
	t.Fatalf("no session row for %q", name)
	return ""
}

// TestSessionCountOnlyWhenCollapsed: the count and a window row's workspace
// number were the same muted digit in the same column, so "1" meant two things
// on adjacent lines. An expanded session prints the rows it would be counting.
func TestSessionCountOnlyWhenCollapsed(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")

	expanded := sessionRowText(t, m, "local")
	if strings.HasSuffix(expanded, "3") {
		t.Errorf("an expanded session still counts its visible rows: %q", expanded)
	}

	m.SidebarCollapsed = map[string]bool{"local": true}
	collapsed := sessionRowText(t, m, "local")
	if !strings.HasSuffix(collapsed, "3") {
		t.Errorf("a collapsed session lost its count: %q", collapsed)
	}
}

// TestWorkspaceDigitIsNotABareDigit: the surviving count is a bare digit, so
// the workspace a pane sits on has to read as something else.
func TestWorkspaceDigitIsNotABareDigit(t *testing.T) {
	m := spreadTestOS(t, 120, 40, "left")
	m.SidebarHoverActive = false
	m.FocusedWindow = -1 // the focused pane draws a chip, which is a different row

	there := rowContent(windowRowFor(t, m, winElsewhereID))
	if !strings.HasSuffix(there, "w4") {
		t.Errorf("the other-workspace row = %q, want it to end in %q", there, "w4")
	}

	here := rowContent(windowRowFor(t, m, winHereID))
	if strings.HasSuffix(here, "w1") {
		t.Errorf("a pane on the current workspace should say nothing: %q", here)
	}
}
