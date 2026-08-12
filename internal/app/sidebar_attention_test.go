package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// attentionOS is one session whose four panes cover the whole state ladder, in
// deliberately calm-first tree order so a rail that did not sort would show
// them backwards.
func attentionOS(t *testing.T, w, h int) (*OS, sessiontree.Tree) {
	t.Helper()
	m := newNarrowOS(t, w, h)
	m.CurrentWorkspace = 1
	m.SessionName = "main"
	m.Windows = []*terminal.Window{
		{ID: "w-idle", CustomName: "idle", Width: 40, Height: 20, Workspace: 1, AgentState: "idle"},
		{ID: "w-work", CustomName: "build", Width: 40, Height: 20, Workspace: 1, AgentState: "working"},
		{ID: "w-done", CustomName: "claude", Width: 40, Height: 20, Workspace: 1, AgentState: "done"},
		{ID: "w-err", CustomName: "tests", Width: 40, Height: 20, Workspace: 1, AgentState: "errored"},
		{ID: "w-input", CustomName: "server", Width: 40, Height: 20, Workspace: 1, AgentState: "needs_input"},
	}
	m.FocusedWindow = 0
	withSidebar(t, true, "left", config.SidebarDefaultWidth)
	m.SidebarOrder, m.SidebarAgentSeen = nil, nil
	return m, m.BuildSessionTree()
}

// agentNavIDs is the window ID of every agent nav row, in nav order.
func agentNavIDs(m *OS) []string {
	var ids []string
	for _, r := range m.SidebarNav {
		if r.Kind == sidebarRowAgent {
			ids = append(ids, r.WindowID)
		}
	}
	return ids
}

// TestAgentsSectionPriorityOrder checks the agents section leads the rail and
// is ordered by need, with the unread bit ranking an untouched done pane above
// a working one and focusing it dropping it back below.
func TestAgentsSectionPriorityOrder(t *testing.T) {
	m, tree := attentionOS(t, 120, 40)
	m.sidebarPanelLinesForTree(tree)

	want := []string{"w-err", "w-input", "w-done", "w-work", "w-idle"}
	if got := agentNavIDs(m); !equalStrings(got, want) {
		t.Fatalf("agent order = %v, want %v (errored > needs_input > done-unseen > working > idle)", got, want)
	}

	// Looking at the finished pane is what demotes it.
	m.FocusWindow(2)
	if !m.agentSeen("w-done") {
		t.Fatal("focusing the done pane did not clear its unread bit")
	}
	m.sidebarPanelLinesForTree(m.BuildSessionTree())
	wantSeen := []string{"w-err", "w-input", "w-work", "w-done", "w-idle"}
	if got := agentNavIDs(m); !equalStrings(got, wantSeen) {
		t.Fatalf("after the look, agent order = %v, want %v (done-seen falls below working)", got, wantSeen)
	}

	// Leaving done re-arms the bit, so the next finish is unread again.
	m.noteAgentState(m.Windows[2], "working")
	if m.agentSeen("w-done") {
		t.Fatal("leaving done kept the seen bit, so the next finish would be silent")
	}
}

// TestRailDrawsAgentsAtTheBottom checks the pinned layout on screen: the
// sessions header is the rail's first row, and the agents header comes last,
// with a blank row of slack opening its section. Blank rather than a
// hairline: that rule was the third on one screen beside the rail edge and
// the dock separator, and empty space separates as well while saying nothing.
func TestRailDrawsAgentsAtTheBottom(t *testing.T) {
	m, tree := attentionOS(t, 120, 40)
	lines, _ := m.sidebarPanelLinesForTree(tree)

	if !strings.Contains(lines[0], "sessions") {
		t.Fatalf("row 0 = %q, want the sessions header first", lines[0])
	}
	agentsRow := -1
	for i, ln := range lines {
		if strings.Contains(ln, "agents") {
			agentsRow = i
			break
		}
	}
	if agentsRow <= 0 {
		t.Fatalf("agents header at row %d, want it below the sessions section", agentsRow)
	}
	if got := strings.TrimSpace(stripANSIForTrace(lines[agentsRow-1])); got != "│" && got != "" {
		t.Fatalf("row %d = %q, want a blank row opening the agents section", agentsRow-1, lines[agentsRow-1])
	}
	if rule := config.GetWindowSeparatorChar(); strings.Contains(lines[agentsRow-1], strings.Repeat(rule, 4)) {
		t.Fatalf("row %d still draws the hairline: %q", agentsRow-1, lines[agentsRow-1])
	}
}

// TestRailNavAndHitsFollowDrawnOrder is the parity guard for the layout:
// every navigable row's nth entry must be the nth recorded hit, so the keyboard
// cursor and a click land on the same thing.
func TestRailNavAndHitsFollowDrawnOrder(t *testing.T) {
	m, tree := attentionOS(t, 120, 40)
	m.sidebarPanelLinesForTree(tree)

	if len(m.SidebarNav) != len(m.SidebarHits) {
		t.Fatalf("%d nav rows but %d hits", len(m.SidebarNav), len(m.SidebarHits))
	}
	for i, r := range m.SidebarNav {
		h := m.SidebarHits[i]
		if h.Kind != r.Kind || h.SessionID != r.SessionID || h.WindowID != r.WindowID {
			t.Fatalf("row %d: nav %v/%s/%s, hit %v/%s/%s", i, r.Kind, r.SessionID, r.WindowID, h.Kind, h.SessionID, h.WindowID)
		}
		if i > 0 && h.Y0 <= m.SidebarHits[i-1].Y0 {
			t.Fatalf("hit %d at y=%d is not below hit %d at y=%d", i, h.Y0, i-1, m.SidebarHits[i-1].Y0)
		}
	}
	if m.SidebarNav[0].Kind != sidebarRowSession {
		t.Fatalf("first nav row is %v, want a session row", m.SidebarNav[0].Kind)
	}
}

// TestGlyphRailInksAttention checks the three-column rail still says which
// session wants you: an attention state fills the cell instead of tinting a
// glyph nobody can see at two cells.
func TestGlyphRailInksAttention(t *testing.T) {
	m, _ := attentionOS(t, 120, 40)
	withSidebar(t, true, "left", config.SidebarGlyphWidth)
	pal := theme.UI()
	cw := config.SidebarGlyphWidth - 1

	calm := sessiontree.Node{ID: "s", Title: "s", AgentState: "working"}
	loud := sessiontree.Node{ID: "s", Title: "s", AgentState: "needs_input"}

	// The glyph rail draws sessions through sidebarStripCell, not
	// sidebarSessionRow: at two content columns there is no room for a
	// separate gutter mark, so the whole cell inks instead.
	calmRow := m.sidebarStripCell(calm, cw, pal, false, false)
	loudRow := m.sidebarStripCell(loud, cw, pal, false, false)

	if fill := ansiBackgroundCount(loudRow); fill == 0 {
		t.Fatalf("needs_input glyph row carries no fill: %q", loudRow)
	}
	if ansiBackgroundCount(loudRow) <= ansiBackgroundCount(calmRow) {
		t.Fatalf("needs_input row is no louder than working:\n  loud=%q\n  calm=%q", loudRow, calmRow)
	}
	if got := overlay.Truncate(agentStateIndicator("needs_input"), 1); !strings.Contains(loudRow, got) {
		t.Fatalf("filled cell dropped its glyph: %q", loudRow)
	}
}

// ansiBackgroundCount counts background-set sequences in a styled row, which is
// how "is this cell inked" is asked of a lipgloss string.
// ansiBackgroundCount counts the cells carrying a truecolor background. The
// background parameters may be merged into one SGR with a foreground, so it
// matches the parameter run rather than a sequence that starts with it.
func ansiBackgroundCount(s string) int {
	return strings.Count(s, "48;2;")
}

// TestSidebarCacheInvalidatesOnAttention checks the signature covers both new
// inputs: a window's agent state and the purely local unread bit. Either going
// stale would leave the rail showing yesterday's triage.
func TestSidebarCacheInvalidatesOnAttention(t *testing.T) {
	withSidebar(t, true, "left", config.SidebarDefaultWidth)
	win := &terminal.Window{ID: "w1", CustomName: "ALPHA", AgentState: "working"}
	m := &OS{Windows: []*terminal.Window{win}, FocusedWindow: 0, Width: 120, Height: 40, SessionName: "s"}

	base := m.sidebarSignature()
	win.AgentState = "done"
	afterState := m.sidebarSignature()
	if afterState == base {
		t.Fatal("agent state change did not move the signature")
	}
	m.markAgentSeen("w1")
	if m.sidebarSignature() == afterState {
		t.Fatal("clearing the unread bit did not move the signature")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
