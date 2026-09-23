package app

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// questionRow draws the rail at the given width with one blocked agent pane in
// a foreign session, and returns that pane's row with the styling stripped.
func questionRow(t *testing.T, width int, w sessiontree.WindowInput) string {
	t.Helper()
	m, _ := sectionsTestOS(t, 220, 40)
	m.Windows = m.Windows[:1]
	withSidebar(t, true, "left", width)
	m.Settings = config.Global
	m.SidebarWidthPref = width
	if got := m.GetSidebarWidth(); got != width {
		t.Fatalf("rail width = %d, want %d", got, width)
	}
	lines, _ := m.sidebarPanelLinesForTree(needTree(w))
	return stripANSIForTrace(railAgentRow(m, lines, w.ID))
}

// TestAgentRowShowsWhatABlockedPaneAsks (issue 165): the row of a pane that
// needs you says what it is asking at every rail width, with the need word
// said once, whether a screen rule read the prompt or a hook reported it.
func TestAgentRowShowsWhatABlockedPaneAsks(t *testing.T) {
	at := time.Now().Add(-3 * time.Minute).UnixNano()
	screen := sessiontree.WindowInput{
		ID: "dddddddd4444", Title: "REVIEW", AgentState: "needs_input", Harness: "claude-code",
		Message: "approval: Do you want to make this edit to main.go?", AgentKind: "approval", StateAt: at,
	}
	hook := sessiontree.WindowInput{
		ID: "dddddddd4444", Title: "REVIEW", AgentState: "needs_input", Harness: "claude-code",
		Message: "approve Bash: make", AgentKind: "approval", StateAt: at,
	}
	sep := sidebarAgentSep()
	for _, c := range []struct {
		name  string
		width int
		win   sessiontree.WindowInput
		want  string
	}{
		{"screen rule, width 30", 30, screen, "approval" + sep + "Do you want"},
		{"screen rule, width 60", 60, screen, "approval" + sep + "claude" + sep + "Do you want to make this edit"},
		{"screen rule, width 100", 100, screen, "approval" + sep + "claude" + sep + "Do you want to make this edit to main.go?"},
		{"hook, width 30", 30, hook, "approval" + sep + "approve Bash"},
		{"hook, width 60", 60, hook, "approval" + sep + "claude" + sep + "approve Bash: make"},
		{"hook, width 100", 100, hook, "approval" + sep + "claude" + sep + "approve Bash: make"},
	} {
		t.Run(c.name, func(t *testing.T) {
			row := questionRow(t, c.width, c.win)
			if !strings.Contains(row, c.want) {
				t.Fatalf("row =\n%s\nwant it to contain %q", row, c.want)
			}
			if strings.Count(row, "approval") != 1 {
				t.Errorf("row =\n%s\nsays approval %d times, want once", row, strings.Count(row, "approval"))
			}
		})
	}
}
