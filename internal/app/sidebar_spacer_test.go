package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The spacer is the first layout entry that wants a floor rather than a
// ceiling. Every section takes only the lines its rows can fill; empty space
// with nothing to grow into is the whole point of a spacer, so it is sized by
// what it was asked for and not by what it holds.

// spacerFrame renders the rail with a layout and a listing, and hands back the
// plain lines. Every claim below is made against the drawn frame rather than
// against the allocator, because a gap that the arithmetic agrees on and the
// renderer does not draw is not a gap.
func spacerFrame(t *testing.T, spec string, height int) []string {
	t.Helper()
	dir := fileViewTree(t)
	withSections(t, spec)
	m := sidebarTestOS(t, 120, height, "left")
	openFilesOn(t, m, dir)
	lines, _ := m.sidebarPanelLines()
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = stripANSIForTrace(l)
	}
	return out
}

// blankRun is the longest run of lines with nothing on them, and where it
// starts. A rail's edge rule is on every line, so "blank" means the content
// columns are empty rather than the string being.
func blankRun(lines []string) (start, run int) {
	best, bestAt, cur, curAt := 0, -1, 0, 0
	for i, l := range lines {
		if strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(l), "│")) == "" {
			if cur == 0 {
				curAt = i
			}
			cur++
			if cur > best {
				best, bestAt = cur, curAt
			}
			continue
		}
		cur = 0
	}
	return bestAt, best
}

// railLineBlank reports whether a rail line has nothing on it. The edge rule
// facing the panes is on every line, so blank means the content columns are.
func railLineBlank(line string) bool {
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line), "│")) == ""
}

// gapAbove is how many blank lines sit directly above the row naming want.
// Measured where the spacer was asked for rather than anywhere on the rail,
// since a rail with room to spare has a gap at the bottom whatever its layout
// says and counting the longest run would find that one instead.
func gapAbove(t *testing.T, lines []string, want string) int {
	t.Helper()
	at := lineOf(lines, want)
	if at < 0 {
		t.Fatalf("%q is not on the rail:\n%s", want, strings.Join(lines, "\n"))
	}
	n := 0
	for i := at - 1; i >= 0 && railLineBlank(lines[i]); i-- {
		n++
	}
	return n
}

// TestBareSpacerPushesTheRestDown is the spacer's headline behaviour, on the
// drawn rail: a spacer with no share takes the lines nothing else wants, so
// everything listed under it goes to the bottom.
//
// The bare spacer had to mean this rather than "an equal split with the other
// flexible entries". A layout says "sessions, gap, terminals" to put the panes
// at the bottom of the rail, and an equal split would have put them halfway up
// it and called that the same request.
//
// Negative control, confirmed red: delete the bare-spacer pass from
// sidebarBudgetLines. The gap collapses to nothing and the sections stack from
// the top, so both claims fail.
func TestBareSpacerPushesTheRestDown(t *testing.T) {
	lines := spacerFrame(t, "sessions,spacer,terminals,files", 40)

	sessions, terminals := lineOf(lines, "sessions"), lineOf(lines, "terminals")
	if sessions < 0 || terminals < 0 {
		t.Fatalf("a section is missing from the rail:\n%s", strings.Join(lines, "\n"))
	}
	if gap := gapAbove(t, lines, "terminals"); gap < 10 {
		t.Errorf("the gap above terminals is %d lines, want the spacer to take most of the rail:\n%s",
			gap, strings.Join(lines, "\n"))
	}
	if sessions > terminals {
		t.Errorf("sessions at %d is under terminals at %d, so the order was lost", sessions, terminals)
	}
	// The section under the spacer reaches the bottom of the rail, which is the
	// claim "pushes the rest down" actually makes.
	if last := lineOf(lines, "README.md"); last < len(lines)-4 {
		t.Errorf("the last file row is at %d on a %d-line rail, so nothing was pushed down:\n%s",
			last, len(lines), strings.Join(lines, "\n"))
	}
}

// TestSpacerWithAShareKeepsIt is the floor half of the rule.
//
// A section's share is a ceiling and not a floor: spare lines go to whichever
// section still has rows hidden, so a list next to it grows into the room. A
// spacer turns that over, because a gap that shrank whenever a neighbour had
// one more row to show would not be a gap anybody could rely on.
//
// Negative control, confirmed red: put spacers into the grow lists in
// sidebarBudgetLines. The files section eats the gap and the run drops to 1.
func TestSpacerWithAShareKeepsIt(t *testing.T) {
	// files has more rows than a quarter of this rail holds, so it is a section
	// with rows below its own fold sitting directly under the gap.
	lines := spacerFrame(t, "sessions:20,spacer:20,files:25,terminals", 40)
	if gap := gapAbove(t, lines, "files"); gap < 4 || gap > 10 {
		t.Errorf("a 20 percent spacer took %d lines of a 40-line rail:\n%s",
			gap, strings.Join(lines, "\n"))
	}
}

// TestTwoSpacersMakeTwoGaps is why membership had to leave the booleans and
// move into the ordered list. A boolean per section can say "no files"; it
// cannot say "a gap here and another one there", and it has no second switch to
// tell the two apart.
//
// Negative control, confirmed red: put the spacer back inside the parser's
// repeat check. The rail draws one gap and this fails.
func TestTwoSpacersMakeTwoGaps(t *testing.T) {
	lines := spacerFrame(t, "files,spacer,sessions,spacer,agents", 40)
	gaps := 0
	run := 0
	for _, l := range lines {
		if strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(l), "│")) == "" {
			run++
			continue
		}
		if run >= 3 {
			gaps++
		}
		run = 0
	}
	if run >= 3 {
		gaps++
	}
	if gaps != 2 {
		t.Errorf("a layout with two spacers drew %d gaps, want 2:\n%s", gaps, strings.Join(lines, "\n"))
	}
}

// TestSpacerGivesUpItsLinesFirst is the rule the layout fix earlier this week
// established, applied to the one entry that has nothing to show for its lines:
// give up space before giving up the region.
//
// A rail too short for its own floors has no lines to spend on being empty. The
// spacer is shrunk before any section is, and it is shrunk to nothing before a
// section gives up its first line, so the sections a person can read survive a
// short rail and the gap between them does not.
//
// Measured on the allocator rather than on the frame, because the render's own
// truncation cuts an overrunning rail back to its band: a spacer that refused
// to give anything up still looks right on screen and takes a section off the
// bottom of it, which is a symptom the frame cannot tell apart from a rail that
// is simply full.
//
// Negative control, confirmed red: move the spacer group to the end of the
// give-up order in sidebarBudgetLines. The sections are shrunk first and the
// spacer keeps its 40 percent of a rail that has no room for it.
func TestSpacerGivesUpItsLinesFirst(t *testing.T) {
	plans := []sidebarSectionPlan{
		{Section: sidebarSectionSessions, Share: 30},
		{Section: sidebarSectionCount, Share: 40, Spacer: true},
		{Section: sidebarSectionAgents, Share: 30},
	}
	rowH := []int{1, 1, 1}
	rows := []int{4, 1, 4}
	// The two sections keep two rows each before the ladder starts taking whole
	// lines off the rail, so four lines is the rail that has exactly nothing to
	// spare on being empty.
	const floors = 4

	for avail := 0; avail <= 20; avail++ {
		out := sidebarBudgetLines(avail, plans, rows, rowH)
		if out[1] > 0 && (out[0] < 2 || out[2] < 2) {
			t.Errorf("avail=%d: %v keeps %d empty lines while a section is under its floor",
				avail, out, out[1])
		}
		if avail <= floors && out[1] != 0 {
			t.Errorf("avail=%d: %v spends %d lines on empty space that the sections need",
				avail, out, out[1])
		}
	}

	// And with room for everything the spacer is served, or "give up first"
	// would have been "never get any".
	if out := sidebarBudgetLines(20, plans, rows, rowH); out[1] < 4 {
		t.Errorf("a 40 percent spacer took %d lines of a rail with room to spare: %v", out[1], out)
	}
}

// TestSpacerMayBeTheLastEntry answers the question the layout already had an
// opinion about. The rail pins its last section to the bottom edge and holds it
// at its ceiling so the gap above it survives; a spacer written last is a
// person saying where that gap goes instead.
//
// So a trailing spacer is allowed, the sections above it stack from the top,
// and the gap is under them rather than over the last one.
//
// Negative control, confirmed red: keep the implicit pinning when a spacer is
// present. The slack moves back above the last section and this fails.
func TestSpacerMayBeTheLastEntry(t *testing.T) {
	lines := spacerFrame(t, "sessions,terminals,files,spacer", 40)
	at, run := blankRun(lines)
	if run < 8 {
		t.Errorf("a trailing spacer took %d lines:\n%s", run, strings.Join(lines, "\n"))
	}
	last := 0
	for _, name := range []string{"sessions", "terminals", "files"} {
		i := lineOf(lines, name)
		if i < 0 {
			t.Fatalf("%s is missing:\n%s", name, strings.Join(lines, "\n"))
		}
		last = max(last, i)
	}
	if at < last {
		t.Errorf("the gap starts at %d, above the last section at %d:\n%s",
			at, last, strings.Join(lines, "\n"))
	}
}

// TestWheelOverASpacerScrollsTheSectionAboveIt keeps the gap from belonging to
// nobody.
//
// A spacer draws nothing, so it has no band and no rows of its own. A wheel over
// it would then fall through every section's band and scroll none of them,
// which reads on screen as the rail ignoring the pointer over a third of
// itself. The gap goes to the section above, which is the rule the pinned
// block's floating gap already follows.
//
// Negative control, confirmed red: stop extending the band above a spacer
// across its lines in sidebarPanelLinesForTree. The wheel over the gap scrolls
// nothing and the offset stays at 0.
func TestWheelOverASpacerScrollsTheSectionAboveIt(t *testing.T) {
	dir := fileViewTree(t)
	// The spacer is above the section the rail pins to its bottom edge, so the
	// gap it makes is not the pinned block's floating blank: that one already
	// belongs to the section over it, and a spacer that leant on it would look
	// covered without being.
	withSections(t, "sessions,spacer,terminals,files")
	m := sidebarTestOS(t, 120, 24, "left")
	openFilesOn(t, m, dir)
	lines, _ := m.sidebarPanelLines()
	plain := make([]string, len(lines))
	for i, l := range lines {
		plain[i] = stripANSIForTrace(l)
	}

	// The middle of the gap the spacer made, which is the run of blanks under
	// the sessions section.
	at := lineOf(plain, "terminals")
	if at < 0 {
		t.Fatalf("no terminals section:\n%s", strings.Join(plain, "\n"))
	}
	top := at
	for top > 0 && railLineBlank(plain[top-1]) {
		top--
	}
	if at-top < 3 {
		t.Fatalf("the gap above terminals is only %d lines:\n%s", at-top, strings.Join(plain, "\n"))
	}
	row := (top + at) / 2
	y := m.GetTopMargin() + row

	before := m.SidebarScrollS
	if !m.SidebarWheel(1, y, false) {
		t.Fatal("the rail did not take a wheel event inside its own band")
	}
	if m.SidebarScrollS == before {
		t.Errorf("a wheel on the gap at row %d scrolled nothing; bands are %v", row, m.sidebarSectionY)
	}
}

// TestLayoutWithNoSpacerIsUnchanged is the compatibility claim. Every rule the
// spacer added is conditioned on there being one, so a config that has never
// heard of spacers lays out exactly as it did.
//
// The pinned block is the thing to check: it is the rule the spacer turns off,
// and it is the one a person would notice going missing.
func TestLayoutWithNoSpacerIsUnchanged(t *testing.T) {
	lines := spacerFrame(t, config.SidebarDefaultSections, 40)
	agents := lineOf(lines, "agents")
	if agents < 0 {
		t.Fatalf("no agents section:\n%s", strings.Join(lines, "\n"))
	}
	// Pinned means the block sits at the bottom with the slack above it.
	if agents < len(lines)-6 {
		t.Errorf("the agents block is at %d on a %d-line rail, so it is no longer pinned:\n%s",
			agents, len(lines), strings.Join(lines, "\n"))
	}
	at, run := blankRun(lines)
	if at+run > agents {
		t.Errorf("the slack at %d..%d is not above the pinned block at %d:\n%s",
			at, at+run, agents, strings.Join(lines, "\n"))
	}
}
