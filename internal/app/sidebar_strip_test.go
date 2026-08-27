package app

import (
	"regexp"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
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

// TestStripRestsAsAStackOfNamedLists is the state that matters, because it is
// the usual one: a Panel band the full height of the rail carrying one
// contiguous stack under a pad. Each list is headed by its own name cut to one
// column with its add control beside it, and the rail below the stack is empty
// band. No badge, no digits, no fills, and no holes inside the stack.
func TestStripRestsAsAStackOfNamedLists(t *testing.T) {
	m, tree := quietStripOS(t, 120, 20)
	lines := railPlain(t, m, tree)

	if want := m.GetUsableHeight(); len(lines) != want {
		t.Fatalf("the strip drew %d lines, want %d", len(lines), want)
	}
	rule := config.Global.GetWindowBorderLeft()
	want := []string{
		"  " + rule, // the pad above the stack: no badge, no hole for one
		"+s" + rule, // sessions, and the control that makes one
		"▎·" + rule, // the attached session
		" ·" + rule,
		" ·" + rule,
		"+t" + rule, // the shown session's panes, and the control that makes one
		"▎·" + rule, // the focused pane
		" ●" + rule, // the pane running something
	}
	for i, w := range want {
		if lines[i] != w {
			t.Errorf("resting line %d = %q, want %q\n%s", i, lines[i], w, strings.Join(lines, "\n"))
		}
	}
	// The only pane with anything to say is in the session the terminals list is
	// already showing, so there is no agents group: the strip never draws one
	// pane twice.
	tail := []string{" »" + rule, "  " + rule}
	for i, w := range tail {
		if got := lines[len(lines)-len(tail)+i]; got != w {
			t.Errorf("tail line %d = %q, want %q\n%s", i, got, w, strings.Join(lines, "\n"))
		}
	}
	for i := len(want); i < len(lines)-len(tail); i++ {
		if lines[i] != "  "+rule {
			t.Errorf("line %d = %q, want the rail under the stack to be empty band", i, lines[i])
		}
	}
	// The digits are gone: at three columns a window count is trivia, and it was
	// the main source of the mixed vocabulary that stopped the marks scanning.
	if got := strings.Join(lines, ""); strings.ContainsAny(got, "0123456789") {
		t.Errorf("the resting strip prints a digit:\n%s", strings.Join(lines, "\n"))
	}
}

// TestStripStackHasNoHolesInIt is the complaint this round answers, stated as
// the rule it broke: every drawn row of the strip's stack is adjacent to the
// next one. Marks spread down a tall column with blank rows between them read
// as a broken rail rather than as a sparse one.
func TestStripStackHasNoHolesInIt(t *testing.T) {
	for _, h := range []int{45, 30, 20, 12} {
		m, tree := stripOS(t, 120, h)
		lines := railPlain(t, m, tree)

		marked := func(i int) bool {
			return strings.TrimSpace(strings.TrimSuffix(lines[i], config.Global.GetWindowBorderLeft())) != ""
		}
		first := 0
		for first < len(lines) && !marked(first) {
			first++
		}
		end := first
		for end < len(lines) && marked(end) {
			end++
		}
		// The toggle is pinned to the rail's foot, so it is the one mark allowed
		// to stand apart from the stack. Anything else below it is a row the
		// stack left a hole above.
		glyph, _ := m.sidebarCollapseGlyph(sidebarVariantGlyph)
		for i := end; i < len(lines); i++ {
			if marked(i) && !strings.Contains(lines[i], glyph) {
				t.Errorf("h=%d: line %d stands below the stack with a hole above it:\n%s", h, i, strings.Join(lines, "\n"))
			}
		}
		if !strings.Contains(strings.Join(lines, "\n"), glyph) {
			t.Errorf("h=%d: the strip drew no toggle:\n%s", h, strings.Join(lines, "\n"))
		}
	}
}

// TestStripBandCoversItsFullHeight is the figure/ground claim, asserted on the
// drawn frame: an agent TUI's own left margin is a column of glyphs on Canvas,
// so every cell of the strip has to sit on a ground of its own or the two read
// as one object. That confusion is the whole reason for the redesign.
func TestStripBandCoversItsFullHeight(t *testing.T) {
	for _, pos := range []string{"left", "right"} {
		m, tree := stripOS(t, 120, 20)
		withSidebar(t, true, pos, config.SidebarDefaultWidth)
		m.Settings = config.Global
		m.SidebarCollapsed = true
		lines, w := m.sidebarPanelLinesForTree(tree)

		panel := panelSGR(t)
		badge := 0
		for i, line := range lines {
			cells := stripCells(line)
			if len(cells) != w {
				t.Fatalf("%s line %d drew %d cells, want %d", pos, i, len(cells), w)
			}
			for x, cell := range cells {
				bg := bgOf(cell)
				switch {
				case bg == panel:
				case bg == "":
					t.Errorf("%s cell (%d,%d) %q sits on bare canvas; the band has to own every cell", pos, i, x, cell)
				default:
					badge++ // the badge's severity fill, counted below
				}
			}
		}
		// Exactly one inked cell pair on the whole strip, and it is the badge.
		if badge != 2 {
			t.Errorf("%s: %d cells carry a fill other than the band, want the badge's two", pos, badge)
		}
	}
}

// TestBandIsConfinedToTheCollapsedStrip: a standing fill is the thing the rest
// of the rail spent a round removing, so the exception has to stay where it was
// argued for. Expanded, the rail is still lines of text on the bare canvas.
func TestBandIsConfinedToTheCollapsedStrip(t *testing.T) {
	m, tree := stripOS(t, 120, 30)
	m.SidebarCollapsed = false
	lines, _ := m.sidebarPanelLinesForTree(tree)

	panel := panelSGR(t)
	for i, line := range lines {
		for x, cell := range stripCells(line) {
			if bgOf(cell) == panel {
				t.Fatalf("the expanded rail paints Panel at (%d,%d): %q", i, x, cell)
			}
		}
	}
}

// TestStripInksSeverityInExactlyOnePlace: the old strip said "something is
// wrong" with a badge and again with an inked session cell four rows away. Two
// saturated blocks on a three-column rail is decoration; the badge already
// shouts, so the session carries its severity as a coloured mark.
func TestStripInksSeverityInExactlyOnePlace(t *testing.T) {
	m, tree := stripOS(t, 120, 20)
	lines, _ := m.sidebarPanelLinesForTree(tree)

	panel := panelSGR(t)
	inked := 0
	for _, line := range lines {
		for _, cell := range stripCells(line) {
			if bg := bgOf(cell); bg != "" && bg != panel {
				inked++
			}
		}
	}
	if inked != 2 {
		t.Errorf("%d cells are inked, want the badge's two only:\n%s", inked, strings.Join(lines, "\n"))
	}

	// The errored session still says so, as a mark rather than as a block.
	plain := railPlain(t, m, tree)
	if !strings.Contains(strings.Join(plain, "\n"), agentStateIndicator("errored")) {
		t.Errorf("no session carries its severity glyph:\n%s", strings.Join(plain, "\n"))
	}
}

// TestStripBadgeLeadsTheSpine: the badge is the strip's one digit and its one
// fill, it appears only when something is blocked, and it reserves no hole when
// nothing is.
func TestStripBadgeLeadsTheSpine(t *testing.T) {
	m, tree := stripOS(t, 120, 20)
	lines := railPlain(t, m, tree)
	rule := config.Global.GetWindowBorderLeft()

	if lines[0] != "  "+rule {
		t.Errorf("line 0 = %q, want a pad above the badge", lines[0])
	}
	if want := "2" + agentStateIndicator("errored") + rule; lines[1] != want {
		t.Errorf("line 1 = %q, want the badge %q", lines[1], want)
	}
	// The stack starts directly under the alarm, headed by the list it is a
	// header for: the header is what holds the badge off the marks, so the alarm
	// costs the stack no line of its own.
	if lines[2] != "+s"+rule {
		t.Errorf("line 2 = %q, want the sessions header under the badge", lines[2])
	}
	if lines[3] != "▎·"+rule {
		t.Errorf("line 3 = %q, want the sessions list to start under its header", lines[3])
	}

	quiet, qtree := quietStripOS(t, 120, 20)
	m.Settings = config.Global
	if got := railPlain(t, quiet, qtree)[1]; got != "+s"+rule {
		t.Errorf("with nothing blocked line 1 = %q, want the stack, not a reserved hole", got)
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

// TestStripListsKeepOneShapeAtOneInterval: one glyph per item, always the same
// column, one row each with nothing between them. One interval across every
// list is what makes the stack read as one object rather than as scattered
// debris, which was the complaint.
func TestStripListsKeepOneShapeAtOneInterval(t *testing.T) {
	m, tree := quietStripOS(t, 120, 20)
	lines := railPlain(t, m, tree)

	// The sessions list is the rows between its own header and the next one.
	head := lineOf(lines, "+s")
	next := lineOf(lines, "+t")
	if head < 0 || next < 0 {
		t.Fatalf("the strip drew no headers:\n%s", strings.Join(lines, "\n"))
	}
	marks := lines[head+1 : next]
	if len(marks) != 3 {
		t.Fatalf("the sessions list drew %d marks, want one per session:\n%s", len(marks), strings.Join(lines, "\n"))
	}
	for i, l := range marks {
		body := strings.TrimSuffix(l, config.Global.GetWindowBorderLeft())
		if strings.TrimSpace(body) == "" {
			t.Errorf("session mark %d is a blank row: %q", i, l)
		}
		if len([]rune(body)) != 2 {
			t.Errorf("session mark %d is %d cells wide, want the strip's two", i, len([]rune(body)))
		}
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

// TestStripPacksThenSaysWhatItCut is the same degradation read off the drawn
// frame: a rail with eight sessions and room for four marks packs them, keeps
// the top of the list, and ends on the tail mark rather than just stopping.
func TestStripPacksThenSaysWhatItCut(t *testing.T) {
	m, tree := manySessionsOS(t, 120, 9)
	lines := railPlain(t, m, tree)
	rule := config.Global.GetWindowBorderLeft()

	want := []string{
		"  " + rule,
		"+s" + rule,
		"▎·" + rule,
		" ·" + rule,
		" ⋮" + rule, // the six it had no line for
		" »" + rule,
		"  " + rule,
	}
	for i := range want {
		if i >= len(lines) || lines[i] != want[i] {
			t.Fatalf("short rail = \n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
		}
	}
	if len(lines) != len(want) {
		t.Errorf("the short rail drew %d lines, want %d", len(lines), len(want))
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

// TestStripASCIIAndMonochromeBothStayCoherent: a design resting on a glyph or a
// colour that either mode lacks fails invisibly. ASCII swaps every mark for a
// plain one; monochrome drops the band's fill, which is why the hairline rule
// stays as the boundary of last resort.
func TestStripASCIIAndMonochromeBothStayCoherent(t *testing.T) {
	t.Run("ascii", func(t *testing.T) {
		prev := config.Global.UseASCIIOnly
		config.Global.UseASCIIOnly = true
		overlay.SetASCII(true)
		t.Cleanup(func() {
			config.Global.UseASCIIOnly = prev
			overlay.SetASCII(prev)
		})

		m, tree := stripOS(t, 120, 20)
		lines := railPlain(t, m, tree)
		joined := strings.Join(lines, "\n")
		for _, glyph := range []string{"»", "▎", "×", "▲", "·", "⋮"} {
			if strings.Contains(joined, glyph) {
				t.Errorf("the ASCII strip still draws %q:\n%s", glyph, joined)
			}
		}
		for _, want := range []string{">>", "2x", ">."} {
			if !strings.Contains(joined, want) {
				t.Errorf("the ASCII strip is missing %q:\n%s", want, joined)
			}
		}
	})

	t.Run("monochrome", func(t *testing.T) {
		// Monochrome is the rendered frame with every colour dropped, which is
		// what a terminal with no palette to give leaves on screen.
		m, tree := stripOS(t, 120, 20)

		m.Settings = config.Global
		lines := railPlain(t, m, tree)
		rule := config.Global.GetWindowBorderLeft()
		joined := strings.Join(lines, "\n")

		for i, l := range lines {
			if !strings.HasSuffix(l, rule) {
				t.Fatalf("mono line %d = %q keeps no edge; with the fill gone the rule is the only boundary left", i, l)
			}
		}
		// The badge and the marks still say which is which without any colour.
		for _, want := range []string{"2" + agentStateIndicator("errored"), "▎·", agentStateIndicator("errored"), "»"} {
			if !strings.Contains(joined, want) {
				t.Errorf("monochrome loses %q:\n%s", want, joined)
			}
		}
	})
}

// TestStripTooltipsStillPopAndStayOnScreen: two cells is enough to steer by and
// not enough to read, so the label is the strip's only prose. The redesign
// changed which rows exist, which is exactly what would silently unhook it.
func TestStripTooltipsStillPopAndStayOnScreen(t *testing.T) {
	for _, pos := range []string{"left", "right"} {
		m, tree := stripOS(t, 120, 20)
		withSidebar(t, true, pos, config.SidebarDefaultWidth)
		m.Settings = config.Global
		m.SidebarCollapsed = true
		m.sidebarPanelLinesForTree(tree)

		var row sidebarStripRow
		for _, r := range m.sidebarStripRows {
			if r.Kind == sidebarStripSession {
				row = r
				break
			}
		}
		if row.Label == "" {
			t.Fatalf("%s: no session row carries a tooltip label", pos)
		}
		m.sidebarTooltipTrack(1, row.Y0)
		m.Tooltip.At = m.Tooltip.At.Add(-2 * tooltipDelay)
		layer := m.renderRailTooltip()
		if layer == nil {
			t.Fatalf("%s: hovering a session row popped no tooltip", pos)
		}
		x, width := layer.GetX(), lipgloss.Width(layer.GetContent())
		if x < 0 || x+width > m.GetRenderWidth() {
			t.Errorf("%s: the tooltip spans %d..%d, outside the screen's 0..%d", pos, x, x+width, m.GetRenderWidth())
		}
	}
}
