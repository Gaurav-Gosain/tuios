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

// TestStripRestsAsABandWithASpine is the state that matters, because it is the
// usual one: a Panel band the full height of the rail, the accent bar and a dim
// dot for the attached session, one dot per other session at a fixed interval,
// and the expand control. No badge, no digits, no fills.
func TestStripRestsAsABandWithASpine(t *testing.T) {
	m, tree := quietStripOS(t, 120, 20)
	lines := railPlain(t, m, tree)

	if want := m.GetUsableHeight(); len(lines) != want {
		t.Fatalf("the strip drew %d lines, want %d", len(lines), want)
	}
	rule := config.GetWindowBorderLeft()
	want := []string{
		"  " + rule, // pad: no badge, and no hole reserved for one
		"▎·" + rule, // the attached session
		"  " + rule,
		" ·" + rule,
		"  " + rule,
		" ·" + rule,
	}
	for i, w := range want {
		if lines[i] != w {
			t.Errorf("resting line %d = %q, want %q\n%s", i, lines[i], w, strings.Join(lines, "\n"))
		}
	}
	// The fixture has one pane working, so the bottom group carries it: the rule,
	// the mark, the blank that holds the group off the controls, then the toggle
	// and the rail's last pad.
	tail := []string{"──" + rule, " ●" + rule, "  " + rule, " +" + rule, " »" + rule, "  " + rule}
	for i, w := range tail {
		if got := lines[len(lines)-len(tail)+i]; got != w {
			t.Errorf("tail line %d = %q, want %q\n%s", i, got, w, strings.Join(lines, "\n"))
		}
	}
	for i := len(want); i < len(lines)-len(tail); i++ {
		if lines[i] != "  "+rule {
			t.Errorf("line %d = %q, want the slack between the spine and the group to be empty band", i, lines[i])
		}
	}
	// The digits are gone: at three columns a window count is trivia, and it was
	// the main source of the mixed vocabulary that stopped the marks scanning.
	if got := strings.Join(lines, ""); strings.ContainsAny(got, "0123456789") {
		t.Errorf("the resting strip prints a digit:\n%s", strings.Join(lines, "\n"))
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
	rule := config.GetWindowBorderLeft()

	if lines[0] != "  "+rule {
		t.Errorf("line 0 = %q, want a pad above the badge", lines[0])
	}
	if want := "2" + agentStateIndicator("errored") + rule; lines[1] != want {
		t.Errorf("line 1 = %q, want the badge %q", lines[1], want)
	}
	if lines[2] != "  "+rule {
		t.Errorf("line 2 = %q, want a pad under the badge", lines[2])
	}
	if lines[3] != "▎·"+rule {
		t.Errorf("line 3 = %q, want the spine to start under the badge's pad", lines[3])
	}

	quiet, qtree := quietStripOS(t, 120, 20)
	if got := railPlain(t, quiet, qtree)[1]; got != "▎·"+rule {
		t.Errorf("with nothing blocked line 1 = %q, want the spine, not a reserved hole", got)
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

// TestStripSpineKeepsOneShapeAtOneInterval: one glyph per session, always the
// same column, one blank row between marks. The interval is what makes a column
// of marks read as a list rather than as scattered debris, which was the
// complaint.
func TestStripSpineKeepsOneShapeAtOneInterval(t *testing.T) {
	m, tree := quietStripOS(t, 120, 20)
	lines := railPlain(t, m, tree)

	// The spine is everything above the group's rule, which is the boundary the
	// two lists are told apart by.
	spine := lines
	for i, l := range lines {
		if strings.Contains(l, "──") {
			spine = lines[:i]
			break
		}
	}

	var marks []int
	for i, l := range spine {
		if strings.TrimSpace(l[:len(l)-len(config.GetWindowBorderLeft())]) != "" {
			marks = append(marks, i)
		}
	}
	if len(marks) != 3 {
		t.Fatalf("the spine drew %d marks, want one per session:\n%s", len(marks), strings.Join(lines, "\n"))
	}
	for i := 1; i < len(marks); i++ {
		if marks[i]-marks[i-1] != 2 {
			t.Errorf("marks %d and %d are %d rows apart, want the fixed interval of 2", i-1, i, marks[i]-marks[i-1])
		}
	}
}

// TestStripSpacingCollapsesBeforeMarksDrop pins the degradation order: a short
// rail gives up the blank row between marks before it gives up a session, and
// says so with a tail mark only once even packed rows have run out.
func TestStripSpacingCollapsesBeforeMarksDrop(t *testing.T) {
	for _, tc := range []struct {
		region, sessions int
		shown, interval  int
		more             bool
	}{
		{20, 3, 3, 2, false}, // room to spare: spaced
		{5, 3, 3, 2, false},  // exactly the spaced height
		{4, 3, 3, 1, false},  // one short: packed, nothing lost
		{3, 3, 3, 1, false},  // packed exactly
		{2, 3, 1, 1, true},   // out of room: one mark and a tail
		{0, 3, 0, 1, false},  // no region at all
		{5, 0, 0, 1, false},  // no sessions
	} {
		shown, interval, more := sidebarStripPlan(tc.region, tc.sessions)
		if shown != tc.shown || interval != tc.interval || more != tc.more {
			t.Errorf("plan(region=%d sessions=%d) = %d/%d/%v, want %d/%d/%v",
				tc.region, tc.sessions, shown, interval, more, tc.shown, tc.interval, tc.more)
		}
		// The last mark spends no trailing blank, so the spine's own span is one
		// interval short of the naive product.
		if span := max(shown*interval-(interval-1), 0); span > tc.region {
			t.Errorf("plan(region=%d sessions=%d) spans %d rows", tc.region, tc.sessions, span)
		}
	}
}

// TestStripPacksThenSaysWhatItCut is the same degradation read off the drawn
// frame: a rail with eight sessions and room for four marks packs them, keeps
// the top of the list, and ends on the tail mark rather than just stopping.
func TestStripPacksThenSaysWhatItCut(t *testing.T) {
	m, tree := manySessionsOS(t, 120, 9)
	lines := railPlain(t, m, tree)
	rule := config.GetWindowBorderLeft()

	want := []string{
		"  " + rule,
		"▎·" + rule,
		" ·" + rule,
		" ⋮" + rule, // the six it had no line for
		" +" + rule,
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
		lines := railPlain(t, m, tree)
		rule := config.GetWindowBorderLeft()
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
