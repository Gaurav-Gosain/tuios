package app

import (
	"strings"
	"testing"
)

func spacerFrame(t *testing.T, spec string, height int) []string {
	t.Helper()
	dir := fileViewTree(t)
	withSections(t, spec)
	m := sidebarTestOS(t, 120, height, "left")
	wideRail(m)
	openFilesOn(t, m, dir)
	lines, _ := m.sidebarPanelLines()
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = stripANSIForTrace(l)
	}
	return out
}

func railLineBlank(line string) bool {
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line), "│")) == ""
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

	if out := sidebarBudgetLines(20, plans, rows, rowH); out[1] < 4 {
		t.Errorf("a 40 percent spacer took %d lines of a rail with room to spare: %v", out[1], out)
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
	withSections(t, "sessions,spacer,terminals,files")
	m := sidebarTestOS(t, 120, 24, "left")
	openFilesOn(t, m, dir)
	lines, _ := m.sidebarPanelLines()
	plain := make([]string, len(lines))
	for i, l := range lines {
		plain[i] = stripANSIForTrace(l)
	}

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

	before := m.SidebarScroll[sidebarSectionSessions]
	if !m.SidebarWheel(1, y, false) {
		t.Fatal("the rail did not take a wheel event inside its own band")
	}
	if m.SidebarScroll[sidebarSectionSessions] == before {
		t.Errorf("a wheel on the gap at row %d scrolled nothing; bands are %v", row, m.sidebarSectionY)
	}
}
