package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
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

// TestStripPinsTheAlarmAndTheControl: the badge lands where a glance lands and
// the control where the rail meets the panes; the identity stack floats
// between them. Stacking everything from the top left the rest blank, which
// read as debris rather than as a composition.
func TestStripPinsTheAlarmAndTheControl(t *testing.T) {
	m, tree := stripOS(t, 120, 20)
	lines := railPlain(t, m, tree)

	if want := m.GetUsableHeight(); len(lines) != want {
		t.Fatalf("the strip drew %d lines, want %d", len(lines), want)
	}
	// The badge: two panes want a human, and the worst of them is errored.
	if !strings.HasPrefix(lines[0], "2"+agentStateIndicator("errored")) {
		t.Errorf("line 0 = %q, want the attention badge 2%s", lines[0], agentStateIndicator("errored"))
	}
	if !strings.Contains(lines[len(lines)-1], "»") {
		t.Errorf("the last line = %q, want the expand toggle", lines[len(lines)-1])
	}

	// The three session cells are contiguous and centred in what is left.
	first, last := -1, -1
	for i, l := range lines {
		if strings.TrimSpace(l[:2]) != "" && i != 0 && i != len(lines)-1 {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 || last-first != 2 {
		t.Fatalf("the session stack is not three contiguous lines (%d..%d):\n%s", first, last, strings.Join(lines, "\n"))
	}
	above, below := first-1, len(lines)-2-last
	if above-below > 1 || below-above > 1 {
		t.Errorf("the stack is not centred: %d blank lines above, %d below", above, below)
	}
}

// TestStripCentringAtOddAndEvenHeights pins the arithmetic directly, because a
// stack that drifts by a line as the terminal resizes is what centring is for.
func TestStripCentringAtOddAndEvenHeights(t *testing.T) {
	for _, tc := range []struct {
		height, badge, toggle, rows int
		wantTop, wantShown          int
	}{
		{20, 1, 1, 3, 8, 3}, // 18 free, 15 spare, 7 above
		{21, 1, 1, 3, 9, 3}, // 19 free, 16 spare, 8 above
		{10, 0, 1, 3, 3, 3}, // no badge: the stack centres in 9
		{5, 1, 1, 9, 1, 3},  // more sessions than lines: fill what there is
		{2, 1, 1, 3, 1, 0},  // room for the pinned pair only
		{1, 0, 0, 3, 0, 1},  // one line, and it goes to the stack
	} {
		top, shown := sidebarStripStackTop(tc.height, tc.badge, tc.toggle, tc.rows)
		if top != tc.wantTop || shown != tc.wantShown {
			t.Errorf("stackTop(h=%d badge=%d toggle=%d rows=%d) = %d/%d, want %d/%d",
				tc.height, tc.badge, tc.toggle, tc.rows, top, shown, tc.wantTop, tc.wantShown)
		}
		if top+shown > tc.height {
			t.Errorf("the stack runs past the rail: top=%d shown=%d height=%d", top, shown, tc.height)
		}
	}
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

// TestStripCellMarksTheAttachedSessionWithoutPaintingIt: the standing Surface
// fill was the one band the rest of the rail spent a round removing; the same
// mark the expanded rail wears in its gutter says it without painting.
func TestStripCellMarksTheAttachedSession(t *testing.T) {
	pal := theme.UI()
	attached := stripANSIForTrace(new(OS).sidebarStripCell(
		sessiontree.Node{ID: "main", IsCurrent: true, WindowCount: 4}, 2, pal, false, false))
	if !strings.HasPrefix(attached, "▎") {
		t.Errorf("the attached session leads with %q, want the accent mark", attached)
	}

	other := stripANSIForTrace(new(OS).sidebarStripCell(
		sessiontree.Node{ID: "api", WindowCount: 4}, 2, pal, false, false))
	if !strings.HasPrefix(other, "4") {
		t.Errorf("an unattached session leads with %q, want its window count", other)
	}

	solo := stripANSIForTrace(new(OS).sidebarStripCell(
		sessiontree.Node{ID: "solo", WindowCount: 1}, 2, pal, false, false))
	if !strings.HasPrefix(solo, " ") {
		t.Errorf("a one-window session leads with %q, want a blank cell", solo)
	}
}

// TestStripCellsAreTheirOwnHitRects: every session cell is a click target of
// its own, and the toggle claims exactly the pane-facing column.
func TestStripCellsAreTheirOwnHitRects(t *testing.T) {
	m, tree := stripOS(t, 120, 20)
	m.sidebarPanelLinesForTree(tree)

	sessions, toggles := 0, 0
	for _, h := range m.SidebarHits {
		switch h.Kind {
		case sidebarRowSession:
			sessions++
			if h.X1-h.X0 != m.GetSidebarWidth() {
				t.Errorf("a strip session row claims %d columns, want the whole band", h.X1-h.X0)
			}
		case sidebarRowCollapse:
			toggles++
			if h.X1-h.X0 != 1 {
				t.Errorf("the toggle claims %d columns, want the one its glyph takes", h.X1-h.X0)
			}
		default:
			t.Errorf("the strip drew a %v hit, which it has no room to mean", h.Kind)
		}
	}
	if sessions != 3 || toggles != 1 {
		t.Errorf("the strip recorded %d session hits and %d toggles, want 3 and 1", sessions, toggles)
	}

	// The badge is a readout, not a control, so it takes no click; it is
	// recorded on the strip's own row list instead, which is what the tooltip
	// reads.
	kinds := map[sidebarStripRowKind]int{}
	for _, r := range m.sidebarStripRows {
		kinds[r.Kind]++
	}
	if kinds[sidebarStripBadge] != 1 || kinds[sidebarStripSession] != 3 || kinds[sidebarStripToggle] != 1 {
		t.Errorf("strip rows = %v, want one badge, three sessions and one toggle", kinds)
	}
}

// TestStripToggleExpandsTheRail: the strip's one control has to work, and it is
// the only way back for a user who is not going to guess a keybind.
func TestStripToggleExpandsTheRail(t *testing.T) {
	m, tree := stripOS(t, 120, 20)
	m.sidebarPanelLinesForTree(tree)

	var toggle sidebarRowHit
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowCollapse {
			toggle = h
		}
	}
	if toggle.X1 == 0 {
		t.Fatal("the strip drew no toggle")
	}
	// The expand re-lays the panes, which syncs to the daemon; the stub client
	// in the fixture is not one, and this test is about the toggle.
	m.DaemonClient = nil
	if !m.SidebarClick(toggle.X0, toggle.Y0, false) {
		t.Fatal("the toggle did not consume its own click")
	}
	if m.SidebarCollapsed {
		t.Error("clicking the expand toggle left the rail collapsed")
	}
	if got := sidebarVariant(m.GetSidebarWidth()); got == sidebarVariantGlyph {
		t.Errorf("the rail is still a strip after expanding: variant %d", got)
	}
}

// TestStripASCIIFallback: the strip is two cells, so a terminal without the
// block glyphs has to get something in both of them.
func TestStripASCIIFallback(t *testing.T) {
	prev := config.UseASCIIOnly
	config.UseASCIIOnly = true
	overlay.SetASCII(true)
	t.Cleanup(func() {
		config.UseASCIIOnly = prev
		overlay.SetASCII(prev)
	})

	m, tree := stripOS(t, 120, 20)
	lines := railPlain(t, m, tree)
	joined := strings.Join(lines, "\n")
	for _, glyph := range []string{"»", "▎", "×", "▲"} {
		if strings.Contains(joined, glyph) {
			t.Errorf("the ASCII strip still draws %q:\n%s", glyph, joined)
		}
	}
	if !strings.Contains(joined, ">>") {
		t.Errorf("the ASCII strip has no expand control:\n%s", joined)
	}
}
