package app

import (
	"image/color"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/charmbracelet/x/ansi"
)

// The window IDs sidebarTestOS builds, which spreadTestOS then spreads over
// workspaces 1, 2 and 4.
const (
	winHereID      = "aaaaaaaa1111" // "editor", workspace 1 (current)
	winElsewhereID = "cccccccc3333" // "logs", workspace 4
)

// windowRowFor renders the rail and returns the tree window row for a window,
// located through its recorded hit so the agents section cannot be mistaken for
// it. ANSI is left intact.
func windowRowFor(t *testing.T, m *OS, windowID string) string {
	t.Helper()
	lines, _ := m.sidebarPanelLines()
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowWindow && h.WindowID == windowID {
			return lines[h.Y0-m.GetTopMargin()]
		}
	}
	t.Fatalf("no window row for %q", windowID)
	return ""
}

// rowContent is a drawn rail line without its ANSI, its edge rule, or its
// padding: what the row actually says.
func rowContent(line string) string {
	s := strings.TrimRight(ansi.Strip(line), " ")
	s = strings.TrimSuffix(s, config.GetWindowBorderLeft())
	return strings.TrimRight(s, " ")
}

// fgSeq is the escape sequence a foreground color renders as, so a row can be
// checked for the color it was actually drawn in rather than for a color name.
func fgSeq(c color.Color) string {
	rendered := lipgloss.NewStyle().Foreground(c).Render("x")
	return rendered[:strings.Index(rendered, "x")]
}

// TestOtherWorkspaceWindowRowCarriesItsDigit is deliverable 3: a pane on another
// workspace names the workspace it is on, right-aligned; a pane on the current
// one does not, and the digit costs the row no width.
func TestOtherWorkspaceWindowRowCarriesItsDigit(t *testing.T) {
	m := spreadTestOS(t, 120, 40, "left")
	m.SidebarHoverActive = false
	m.FocusedWindow = -1 // the focused pane draws a pill, which is a different row

	hereLine := windowRowFor(t, m, winHereID)
	thereLine := windowRowFor(t, m, winElsewhereID)
	here, there := rowContent(hereLine), rowContent(thereLine)

	if strings.HasSuffix(here, "1") {
		t.Errorf("a pane on the current workspace should carry no digit: %q", here)
	}
	if !strings.HasSuffix(there, "4") {
		t.Errorf("the pane on workspace 4 lost its right-aligned digit: %q", there)
	}
	if hw, tw := lipgloss.Width(hereLine), lipgloss.Width(thereLine); hw != tw {
		t.Errorf("the digit changed the row width: %d vs %d", tw, hw)
	}
}

// TestForeignSessionWindowRowHasNoDigit: this client does not hold another
// session's panes, so it does not know their workspaces and must not guess.
func TestForeignSessionWindowRowHasNoDigit(t *testing.T) {
	m := spreadTestOS(t, 120, 40, "left")
	if got := m.windowWorkspace("no-such-window"); got != 0 {
		t.Errorf("an unheld window reported workspace %d, want 0", got)
	}
}
