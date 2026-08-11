package app

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// sidebarMultiSessionOS builds an OS attached to "main" with agent-flagged
// windows and the sidebar on, plus a synthetic three-session tree the way a
// daemon-backed client would see one. The tree order is the daemon's creation
// order: main, scratch, deploy.
func sidebarMultiSessionOS(t *testing.T, w, h int) (*OS, sessiontree.Tree) {
	t.Helper()
	m := newNarrowOS(t, w, h)
	m.CurrentWorkspace = 1
	m.SessionName = "main"
	m.Windows = []*terminal.Window{
		{ID: "aaaaaaaa1111", CustomName: "claude", Width: 40, Height: 20, Workspace: 1, AgentState: "working"},
		{ID: "bbbbbbbb2222", CustomName: "tests", Width: 40, Height: 20, Workspace: 1, AgentState: "needs_input"},
		{ID: "cccccccc3333", CustomName: "logs", Width: 40, Height: 20, Workspace: 1},
	}
	m.FocusedWindow = 0
	withSidebar(t, true, "left", config.SidebarDefaultWidth)
	m.SidebarOrder, m.SidebarCollapsed = nil, nil

	tree := sessiontree.Build([]sessiontree.SessionInput{
		{Name: "main", Attached: true, IsCurrent: true, Windows: []sessiontree.WindowInput{
			{ID: "aaaaaaaa1111", Title: "claude", AgentState: "working", Focused: true},
			{ID: "bbbbbbbb2222", Title: "tests", AgentState: "needs_input"},
			{ID: "cccccccc3333", Title: "logs"},
		}},
		{Name: "scratch", WindowCount: 2},
		{Name: "deploy", WindowCount: 1},
	})
	return m, tree
}

// sessionHits returns the recorded session-row hits in display order.
func sessionHits(m *OS) []sidebarRowHit {
	var hits []sidebarRowHit
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowSession {
			hits = append(hits, h)
		}
	}
	return hits
}

// TestSidebarSessionOrderPreserved checks the display order is the tree's
// order with the current session marked in place, and that adding a session
// appends instead of reshuffling the rows already on screen.
func TestSidebarSessionOrderPreserved(t *testing.T) {
	m, tree := sidebarMultiSessionOS(t, 120, 40)

	m.sidebarPanelLinesForTree(tree)
	if want := []string{"main", "scratch", "deploy"}; !reflect.DeepEqual(m.SidebarSessionIDs, want) {
		t.Fatalf("display order = %v, want %v (current session must not be hoisted)", m.SidebarSessionIDs, want)
	}

	grown := sessiontree.Tree{Sessions: append(append([]sessiontree.Node{}, tree.Sessions...),
		sessiontree.Node{Kind: sessiontree.KindSession, ID: "fresh", Title: "fresh"})}
	m.sidebarPanelLinesForTree(grown)
	if want := []string{"main", "scratch", "deploy", "fresh"}; !reflect.DeepEqual(m.SidebarSessionIDs, want) {
		t.Fatalf("after a new session, display order = %v, want %v (new sessions append)", m.SidebarSessionIDs, want)
	}
}

// TestSidebarExpandCollapsePersists checks a collapse toggle survives a
// restart: the toggle lands in the state file and a fresh OS loads it back.
func TestSidebarExpandCollapsePersists(t *testing.T) {
	m, tree := sidebarMultiSessionOS(t, 120, 40)
	m.sidebarPanelLinesForTree(tree)

	hits := sessionHits(m)
	if len(hits) != 3 {
		t.Fatalf("session rows = %d, want 3", len(hits))
	}
	// The current session starts expanded: its window rows are on screen. The
	// footer's width stepper is a hit too, pinned below them.
	if got := len(m.SidebarHits) - len(hits); got != 3+2+1 {
		t.Fatalf("non-session rows = %d, want 6 (3 windows + 2 agents + the footer stepper)", got)
	}

	// A press on the name area of the current session row plus a release on it
	// toggles the expansion.
	row := hits[0]
	if !m.SidebarClick(row.X0+5, row.Y0, false) || !m.SidebarRelease(row.X0+5, row.Y0) {
		t.Fatalf("click on the current session row was not consumed")
	}
	if !m.SidebarCollapsed["main"] {
		t.Fatalf("current session not collapsed after click; state=%v", m.SidebarCollapsed)
	}
	m.sidebarPanelLinesForTree(tree)
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowWindow {
			t.Fatalf("window rows still on screen after collapsing their session")
		}
	}

	// The toggle was persisted: a fresh OS reads it back.
	if _, err := os.Stat(filepath.Join(sidebarStateDir(), sidebarStateFileName)); err != nil {
		t.Fatalf("state file not written: %v", err)
	}
	fresh := &OS{}
	fresh.loadSidebarState()
	if !fresh.SidebarCollapsed["main"] {
		t.Fatalf("fresh OS did not load the collapse toggle; state=%v", fresh.SidebarCollapsed)
	}
}

// TestSidebarDragReorderPersists drives the full reorder gesture: press on a
// session row, drag onto another, release. The draft order must track the
// pointer, the commit must land in SidebarOrder and the state file, and a
// fresh OS must load it back.
func TestSidebarDragReorderPersists(t *testing.T) {
	m, tree := sidebarMultiSessionOS(t, 120, 40)
	m.sidebarPanelLinesForTree(tree)

	hits := sessionHits(m)
	if len(hits) != 3 {
		t.Fatalf("session rows = %d, want 3", len(hits))
	}
	scratch, deploy := hits[1], hits[2]

	if !m.SidebarClick(scratch.X0+6, scratch.Y0, false) {
		t.Fatalf("press on the scratch row was not consumed")
	}
	if m.SidebarDrag.Dragging {
		t.Fatalf("press alone must not start the drag")
	}
	if !m.SidebarDragMotion(scratch.X0+6, deploy.Y0) {
		t.Fatalf("drag motion was not consumed")
	}
	if !m.SidebarDrag.Dragging {
		t.Fatalf("vertical motion did not commit the press to a drag")
	}
	if want := []string{"main", "deploy", "scratch"}; !reflect.DeepEqual(m.SidebarDrag.Order, want) {
		t.Fatalf("draft order = %v, want %v", m.SidebarDrag.Order, want)
	}

	// Mid-drag the draft order is what renders, so the dragged row is its own
	// drop indicator.
	m.sidebarPanelLinesForTree(tree)
	if want := []string{"main", "deploy", "scratch"}; !reflect.DeepEqual(m.SidebarSessionIDs, want) {
		t.Fatalf("mid-drag display order = %v, want %v", m.SidebarSessionIDs, want)
	}

	if !m.SidebarRelease(scratch.X0+6, deploy.Y0) {
		t.Fatalf("release did not resolve the drag")
	}
	if want := []string{"main", "deploy", "scratch"}; !reflect.DeepEqual(m.SidebarOrder, want) {
		t.Fatalf("committed order = %v, want %v", m.SidebarOrder, want)
	}

	fresh := &OS{}
	fresh.loadSidebarState()
	if want := []string{"main", "deploy", "scratch"}; !reflect.DeepEqual(fresh.SidebarOrder, want) {
		t.Fatalf("fresh OS loaded order %v, want %v", fresh.SidebarOrder, want)
	}
}

// TestOrderByKeyAppendsUnranked pins the order-overlay semantics: ranked items
// take the saved order, unranked ones keep their natural order after them.
func TestOrderByKeyAppendsUnranked(t *testing.T) {
	items := []string{"a", "b", "c", "d"}
	got := orderByKey(items, func(s string) string { return s }, []string{"c", "a"})
	if want := []string{"c", "a", "b", "d"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("orderByKey = %v, want %v", got, want)
	}
	// A ranked name with no matching item is simply ignored.
	got = orderByKey(items, func(s string) string { return s }, []string{"zz", "d"})
	if want := []string{"d", "a", "b", "c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("orderByKey with stale rank = %v, want %v", got, want)
	}
}

// TestSidebarAgentsSectionListsAgentPanes checks the pinned section lists
// exactly the panes carrying an agent state, routes their rows at windows, and
// disappears when no agents are running.
func TestSidebarAgentsSectionListsAgentPanes(t *testing.T) {
	m, tree := sidebarMultiSessionOS(t, 120, 40)
	lines, _ := m.sidebarPanelLinesForTree(tree)

	var agents []sidebarRowHit
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowAgent {
			agents = append(agents, h)
		}
	}
	if len(agents) != 2 {
		t.Fatalf("agent rows = %d, want 2 (claude and tests carry agent states, logs does not)", len(agents))
	}
	wantIDs := map[string]bool{"aaaaaaaa1111": true, "bbbbbbbb2222": true}
	for _, h := range agents {
		if !wantIDs[h.WindowID] {
			t.Errorf("agent row targets unexpected window %q", h.WindowID)
		}
		if h.WindowIndex < 0 {
			t.Errorf("agent row for %q has no focusable window index", h.WindowID)
		}
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "agents") {
		t.Errorf("agents section header missing")
	}

	// A click on an agent row focuses its pane.
	m.FocusedWindow = 2
	if !m.SidebarClick(agents[0].X0+3, agents[0].Y0, false) {
		t.Fatalf("click on an agent row was not consumed")
	}
	if got := m.Windows[m.FocusedWindow].ID; got != agents[0].WindowID {
		t.Errorf("agent row click focused %q, want %q", got, agents[0].WindowID)
	}

	// No agents: the section vanishes rather than sitting there empty.
	bare := sessiontree.Build([]sessiontree.SessionInput{
		{Name: "main", Attached: true, IsCurrent: true, Windows: []sessiontree.WindowInput{
			{ID: "cccccccc3333", Title: "logs", Focused: true},
		}},
	})
	lines, _ = m.sidebarPanelLinesForTree(bare)
	if strings.Contains(strings.Join(lines, "\n"), "agents") {
		t.Errorf("agents section rendered with no agents running")
	}
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowAgent {
			t.Errorf("agent hit recorded with no agents running")
		}
	}
}

// TestSidebarASCIIFallback renders every variant in ASCII-only mode and
// asserts nothing outside ASCII leaks through except the agent-state shapes,
// which are deliberately kept everywhere they appear. In the default mode the
// only private-use codepoints allowed are the dock's own pill caps.
func TestSidebarASCIIFallback(t *testing.T) {
	m, tree := sidebarMultiSessionOS(t, 120, 40)

	prevASCII := config.UseASCIIOnly
	config.UseASCIIOnly = true
	overlay.SetASCII(true)
	t.Cleanup(func() {
		config.UseASCIIOnly = prevASCII
		overlay.SetASCII(prevASCII)
	})

	agentShapes := map[rune]bool{'●': true, '▲': true, '○': true, '■': true, '×': true}
	for _, size := range [][2]int{{120, 40}, {80, 24}, {51, 37}, {40, 20}} {
		m.Width, m.Height = size[0], size[1]
		m.EffectiveWidth, m.EffectiveHeight = size[0], size[1]
		lines, w := m.sidebarPanelLinesForTree(tree)
		if w == 0 {
			t.Fatalf("%dx%d: sidebar hidden unexpectedly", size[0], size[1])
		}
		for i, ln := range lines {
			if lw := lipgloss.Width(ln); lw != w {
				t.Errorf("%dx%d row %d is %d cells, want %d", size[0], size[1], i, lw, w)
			}
			for _, r := range stripSidebarANSI(ln) {
				if r <= 0x7e || agentShapes[r] {
					continue
				}
				t.Errorf("%dx%d row %d leaks non-ASCII rune %q (U+%04X)", size[0], size[1], i, r, r)
			}
		}
	}

	// Default mode: private-use codepoints only where the dock already uses
	// them (the pill caps); anything else is the kind of tofu the rail must
	// never emit.
	config.UseASCIIOnly = false
	overlay.SetASCII(false)
	sanctioned := map[rune]bool{}
	for _, r := range config.DockPillLeftChar + config.DockPillRightChar {
		sanctioned[r] = true
	}
	m.Width, m.Height = 120, 40
	m.EffectiveWidth, m.EffectiveHeight = 120, 40
	lines, _ := m.sidebarPanelLinesForTree(tree)
	for i, ln := range lines {
		for _, r := range stripSidebarANSI(ln) {
			pua := (r >= 0xe000 && r <= 0xf8ff) || r >= 0xf0000
			if pua && !sanctioned[r] {
				t.Errorf("row %d emits unsanctioned private-use rune U+%04X", i, r)
			}
		}
	}
}

// TestSidebarSanitizesTitles checks a title carrying nerd-font private-use
// icons and control characters reaches the rail laundered.
func TestSidebarSanitizesTitles(t *testing.T) {
	if got := printableTitle(" nvim \x1b]0;x\x07"); got != "nvim ]0;x" {
		// The escape byte and the bell go; printable remnants of a title
		// sequence stay (they are the shell's bug to fix, not tofu).
		t.Errorf("printableTitle = %q", got)
	}
	overlay.SetASCII(true)
	t.Cleanup(func() { overlay.SetASCII(false) })
	if got := printableTitle("café ▲"); got != "caf" {
		t.Errorf("ASCII printableTitle = %q, want %q", got, "caf")
	}
}

// stripSidebarANSI drops SGR escape sequences so rune audits see only what is
// actually printed.
func stripSidebarANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		switch {
		case inEsc:
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEsc = false
			}
		case r == 0x1b:
			inEsc = true
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
