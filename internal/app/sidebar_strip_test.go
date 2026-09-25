package app

import (
	"regexp"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// stripOS is a collapsed rail with three sessions, one of them attached and one
// of them holding two panes that want a human.
func stripOS(t *testing.T, w, h int) (*OS, sessiontree.Tree) {
	t.Helper()
	m, _ := sectionsTestOS(t, w, h)
	m.SidebarCollapsed = true
	tree := sessiontree.Build([]sessiontree.SessionInput{
		{Name: "main", Attached: true, IsCurrent: true, Windows: []sessiontree.WindowInput{
			{ID: "aaaaaaaa1111", Title: "nvim", Focused: true},
			{ID: "bbbbbbbb2222", Title: "build", AgentState: "working"},
		}},
		{Name: "api", Windows: []sessiontree.WindowInput{
			{ID: "dddddddd4444", Title: "server", AgentState: "needs_input"},
			{ID: "eeeeeeee5555", Title: "tests", AgentState: "errored"},
		}},
		{Name: "docs"},
	})
	return m, tree
}

// quietStripOS is the state the strip is in nearly all the time: three sessions,
// nothing blocked, nothing finished unread. It is the resting frame the redesign
// is judged on, so it gets its own fixture.
func quietStripOS(t *testing.T, w, h int) (*OS, sessiontree.Tree) {
	t.Helper()
	m, _ := sectionsTestOS(t, w, h)
	m.SidebarCollapsed = true
	tree := sessiontree.Build([]sessiontree.SessionInput{
		{Name: "main", Attached: true, IsCurrent: true, Windows: []sessiontree.WindowInput{
			{ID: "aaaaaaaa1111", Title: "nvim", Focused: true},
			{ID: "bbbbbbbb2222", Title: "build", AgentState: "working"},
		}},
		{Name: "api", Windows: []sessiontree.WindowInput{{ID: "dddddddd4444", Title: "server"}}},
		{Name: "docs"},
	})
	return m, tree
}

// manySessionsOS is a collapsed rail carrying more sessions than a short screen
// has lines to draw them on.
func manySessionsOS(t *testing.T, w, h int) (*OS, sessiontree.Tree) {
	t.Helper()
	m, _ := sectionsTestOS(t, w, h)
	m.SidebarCollapsed = true
	in := make([]sessiontree.SessionInput, 0, 8)
	for i := range 8 {
		in = append(in, sessiontree.SessionInput{Name: string(rune('a' + i)), IsCurrent: i == 0, Attached: i == 0})
	}
	return m, sessiontree.Build(in)
}

// sgrPattern matches one SGR sequence, so a rendered line can be walked cell by
// cell with the style each cell was painted in still in hand.
var stripSGR = regexp.MustCompile(`\x1b\[[0-9;:]*m`)

// stripCells splits a rendered rail line into one entry per cell, each carrying
// the SGR sequences in force when it was drawn. Assertions about the band's
// ground have to read the frame, not the layout maths that produced it.
func stripCells(line string) []string {
	var cells []string
	style := ""
	for len(line) > 0 {
		if loc := stripSGR.FindStringIndex(line); loc != nil && loc[0] == 0 {
			seq := line[:loc[1]]
			if seq == "\x1b[m" || seq == "\x1b[0m" {
				style = ""
			} else {
				style += seq
			}
			line = line[loc[1]:]
			continue
		}
		r := []rune(line)[0]
		cells = append(cells, style+string(r))
		line = line[len(string(r)):]
	}
	return cells
}

// bgOf is the background colour a rendered cell carries, as its own SGR
// parameters, or "" when it carries none. Pulled out of the sequence rather
// than compared whole, because lipgloss folds the foreground in with it and two
// cells on the same ground would otherwise never compare equal.
func bgOf(cell string) string {
	for _, seq := range stripSGR.FindAllString(cell, -1) {
		parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(seq, "\x1b["), "m"), ";")
		for i, p := range parts {
			if p != "48" || i+1 >= len(parts) {
				continue
			}
			switch parts[i+1] {
			case "2":
				return strings.Join(parts[i:min(i+5, len(parts))], ";")
			case "5":
				return strings.Join(parts[i:min(i+3, len(parts))], ";")
			}
		}
	}
	return ""
}

// panelSGR is the background any band cell should carry: Panel, rendered
// through the same path the rail renders through.
func panelSGR(t *testing.T) string {
	t.Helper()
	return bgOf(lipgloss.NewStyle().Background(theme.UI().Panel).Render(" "))
}

// TestStripBadgeRollsUpTheWorstSeverity: the badge is one cell of alarm, so it
// has to be the loudest one, and it caps rather than overflowing its cell.
func TestStripBadgeRollsUpTheWorstSeverity(t *testing.T) {
	quiet := sessiontree.Build([]sessiontree.SessionInput{
		{Name: "a", Windows: []sessiontree.WindowInput{{ID: "w1", AgentState: "working"}}},
	})
	if got := sidebarStripBadgeFor(quiet.Sessions); got.Count != 0 {
		t.Errorf("a rail with nothing blocked counted %d; an alarm that is always on is not an alarm", got.Count)
	}

	mixed := sessiontree.Build([]sessiontree.SessionInput{
		{Name: "a", Windows: []sessiontree.WindowInput{
			{ID: "w1", AgentState: "needs_input"},
			{ID: "w2", AgentState: "needs_input"},
		}},
		{Name: "b", Windows: []sessiontree.WindowInput{{ID: "w3", AgentState: "errored"}}},
	})
	got := sidebarStripBadgeFor(mixed.Sessions)
	if got.Count != 3 || got.State != "errored" {
		t.Errorf("badge = %d/%q, want 3/errored", got.Count, got.State)
	}

	// Ten or more will not fit in one cell, so it says "more" instead of lying.
	var many []sessiontree.WindowInput
	for i := range 12 {
		many = append(many, sessiontree.WindowInput{ID: string(rune('a' + i)), AgentState: "needs_input"})
	}
	full := sessiontree.Build([]sessiontree.SessionInput{{Name: "a", Windows: many}})
	cell := stripANSIForTrace(sidebarStripBadgeCell(sidebarStripBadgeFor(full.Sessions), 2, theme.UI()))
	if !strings.HasPrefix(cell, "+") {
		t.Errorf("a badge of 12 renders %q, want a + lead", cell)
	}
}

// TestStripSpacingCollapsesBeforeMarksDrop pins the degradation order: a short
// rail gives up the blank row between marks before it gives up a session, and
// says so with a tail mark only once even packed rows have run out.
func TestStripSpacingCollapsesBeforeMarksDrop(t *testing.T) {
	for _, tc := range []struct {
		rows, total int
		shown       int
		more        bool
	}{
		{20, 3, 3, false}, // room to spare
		{3, 3, 3, false},  // exactly the list's height
		{2, 3, 1, true},   // out of room: one mark and a tail
		{1, 3, 0, true},   // a row that can only say it was cut
		{0, 3, 0, false},  // no rows at all
		{5, 0, 0, false},  // nothing in the list
	} {
		shown, more := sidebarStripPlan(tc.rows, tc.total)
		if shown != tc.shown || more != tc.more {
			t.Errorf("plan(rows=%d total=%d) = %d/%v, want %d/%v",
				tc.rows, tc.total, shown, more, tc.shown, tc.more)
		}
		span := shown
		if more {
			span++
		}
		if span > tc.rows {
			t.Errorf("plan(rows=%d total=%d) spans %d rows", tc.rows, tc.total, span)
		}
	}
}

// TestStripSpineFollowsRailOrder: the strip is the same list, folded, so a
// session cannot sit third collapsed and first expanded. Order is the one thing
// the two states share, and it is what makes the fold learnable.
func TestStripSpineFollowsRailOrder(t *testing.T) {
	sessionsOf := func(m *OS) []string {
		var out []string
		for _, n := range m.SidebarNav {
			if n.Kind == sidebarRowSession {
				out = append(out, n.SessionID)
			}
		}
		return out
	}

	m, tree := stripOS(t, 120, 30)
	m.SidebarCollapsed = false
	m.sidebarPanelLinesForTree(tree)
	expanded := sessionsOf(m)

	m.SidebarCollapsed = true
	m.sidebarPanelLinesForTree(tree)
	collapsed := sessionsOf(m)

	if len(collapsed) != len(expanded) || len(collapsed) == 0 {
		t.Fatalf("the strip lists %v, the expanded rail %v", collapsed, expanded)
	}
	for i := range collapsed {
		if collapsed[i] != expanded[i] {
			t.Fatalf("strip order %v differs from rail order %v", collapsed, expanded)
		}
	}
}

// TestStripHitsAndNavStayIndexForIndex: the strip records its rectangles as it
// draws them, so a click and the keyboard cursor can never point at different
// rows. Both rail sides, because the mirrored strip is the one that gets less
// use and so drifts first.
func TestStripHitsAndNavStayIndexForIndex(t *testing.T) {
	for _, pos := range []string{"left", "right"} {
		m, tree := stripOS(t, 120, 20)
		withSidebar(t, true, pos, config.SidebarDefaultWidth)
		m.Settings = config.Global
		m.SidebarCollapsed = true
		lines, w := m.sidebarPanelLinesForTree(tree)

		if len(m.SidebarHits) != len(m.SidebarNav) {
			t.Fatalf("%s: %d hits against %d nav rows", pos, len(m.SidebarHits), len(m.SidebarNav))
		}
		sessions := 0
		for i, h := range m.SidebarHits {
			n := m.SidebarNav[i]
			if h.Kind != n.Kind || h.SessionID != n.SessionID {
				t.Errorf("%s: hit %d is %v/%q but nav %d is %v/%q", pos, i, h.Kind, h.SessionID, i, n.Kind, n.SessionID)
			}
			if h.Kind == sidebarRowSession {
				sessions++
				if h.X1-h.X0 != w {
					t.Errorf("%s: a strip session row claims %d columns, want the whole band", pos, h.X1-h.X0)
				}
				// The rectangle names the row that was actually drawn there.
				line := stripANSIForTrace(lines[h.Y0-m.GetTopMargin()])
				if strings.TrimSpace(line) == "" {
					t.Errorf("%s: hit %d points at a blank line %q", pos, i, line)
				}
			}
		}
		if sessions != 3 {
			t.Errorf("%s: %d session hits, want one per session", pos, sessions)
		}

		// The badge is on both lists too: it is recorded on the strip's own row
		// list for the tooltip, and as a target, because an alarm you cannot click
		// through to its cause is the one object on the strip that does nothing.
		kinds := map[sidebarStripRowKind]int{}
		for _, r := range m.sidebarStripRows {
			kinds[r.Kind]++
		}
		if kinds[sidebarStripBadge] != 1 || kinds[sidebarStripSession] != 3 || kinds[sidebarStripToggle] != 1 {
			t.Errorf("%s: strip rows = %v, want one badge, three sessions and one toggle", pos, kinds)
		}
	}
}
