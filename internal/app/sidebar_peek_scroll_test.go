package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// peekTree builds a machine whose rail list overflows: one attached session
// and thirty foreign ones, whose pane counts alternate so a pointer sweeping
// the list crosses sessions whose peeks take different heights.
func peekTree() sessiontree.Tree {
	windows := func(n int, prefix string) []sessiontree.WindowInput {
		out := make([]sessiontree.WindowInput, 0, n)
		for i := range n {
			out = append(out, sessiontree.WindowInput{ID: fmt.Sprintf("%s-%d", prefix, i), Title: fmt.Sprintf("%s pane %d", prefix, i)})
		}
		return out
	}
	sessions := []sessiontree.SessionInput{
		{Name: "main", Attached: true, IsCurrent: true, CurrentWorkspace: 1, Windows: windows(1, "main")},
	}
	panes := []int{1, 3, 0, 5, 2}
	for i := range 30 {
		sessions = append(sessions, sessiontree.SessionInput{
			Name:             fmt.Sprintf("filler-%02d", i),
			CurrentWorkspace: 1,
			Windows:          windows(panes[i%len(panes)], fmt.Sprintf("filler-%02d", i)),
		})
	}
	return sessiontree.Build(sessions)
}

// TestRailPeekKeepsTheSessionsViewport is the scrolled-rail regression: the
// pointer rests on a session row, the wheel scrolls the sessions section to
// its bottom, and the next pointer move switches the peek. A peek that takes
// more lines than the one before it re-flows the sections and re-clamps the
// sessions viewport under the pointer, so the bottom of the list is
// unreachable: the wheel earns rows and the pointer takes them back. The peek
// must fit into the height the terminals section already had, whatever the
// layout order.
func TestRailPeekKeepsTheSessionsViewport(t *testing.T) {
	m := sidebarTestOS(t, 120, 30, "left")
	m.SessionName = "main"
	tree := peekTree()

	railPlain(t, m, tree)
	sessionsY := m.sidebarSectionY[sidebarSectionSessions]
	if sessionsY[1] <= sessionsY[0] {
		t.Fatalf("the sessions section has no band: %v", sessionsY)
	}

	// The pointer rests on a two-pane session row while the wheel runs, so
	// every render between wheel events budgets the terminals section for a
	// two-pane peek. filler-04 has two panes; filler-03 five.
	m.SidebarPeek = "filler-04"
	railPlain(t, m, tree)
	x := m.GetSidebarWidth() / 2
	for range 20 {
		m.SidebarWheel(x, sessionsY[0]+1, false)
	}
	railPlain(t, m, tree)
	scrolled := m.SidebarScroll[sidebarSectionSessions]
	if scrolled == 0 {
		t.Fatalf("the wheel moved no offset; the test cannot reach the folded rows")
	}

	// One pointer move onto a neighbouring session row: the peek switches to
	// a session whose peek takes more lines, and the viewport must hold.
	m.SidebarPeek = "filler-03"
	railPlain(t, m, tree)
	if m.SidebarScroll[sidebarSectionSessions] != scrolled {
		t.Fatalf("a peek switch moved the sessions scroll from %d to %d: the list jumped under the pointer", scrolled, m.SidebarScroll[sidebarSectionSessions])
	}
	m.SidebarPeek = "filler-10"
	railPlain(t, m, tree)
	if m.SidebarScroll[sidebarSectionSessions] != scrolled {
		t.Fatalf("a second peek switch moved the sessions scroll from %d to %d", scrolled, m.SidebarScroll[sidebarSectionSessions])
	}
}

// TestRailPeekThatFitsListsEveryPane is the second half of the pin: with the
// terminals section below the sessions one and room on the rail, a peek with
// more panes than the attached session has lists every one of them. Held at the
// attached session's resting height, the same peek draws as a single "… +N" row.
func TestRailPeekThatFitsListsEveryPane(t *testing.T) {
	windows := func(n int, prefix string) []sessiontree.WindowInput {
		out := make([]sessiontree.WindowInput, 0, n)
		for i := range n {
			out = append(out, sessiontree.WindowInput{ID: fmt.Sprintf("%s-%d", prefix, i), Title: fmt.Sprintf("%s pane %d", prefix, i)})
		}
		return out
	}
	tree := sessiontree.Build([]sessiontree.SessionInput{
		{Name: "main", Attached: true, IsCurrent: true, CurrentWorkspace: 1, Windows: windows(1, "main")},
		{Name: "pk-six", CurrentWorkspace: 1, Windows: windows(6, "six")},
	})
	m := sidebarTestOS(t, 120, 40, "left")
	m.SessionName = "main"

	railPlain(t, m, tree)
	m.SidebarPeek = "pk-six"
	lines := railPlain(t, m, tree)
	hdr := lineOf(lines, "terminals")
	if hdr < 0 {
		t.Fatalf("the rail never drew the terminals header:\n%v", lines)
	}
	if !strings.Contains(lines[hdr], "pk-six") {
		t.Fatalf("the terminals header does not name the peeked session: %q", lines[hdr])
	}
	for i := range 6 {
		name := fmt.Sprintf("six pane %d", i)
		text := lines[hdr+1+i]
		if !strings.Contains(text, name) {
			t.Fatalf("the peek's line %d is %q, want %s: a peek whose panes fit the rail lists every one of them\n%v", i, text, name, lines[hdr:hdr+8])
		}
		if fields := strings.Fields(text); strings.HasPrefix(fields[len(fields)-1], "+") {
			t.Fatalf("the peek's line %d is %q, which ends in a +N row\n%v", i, text, lines[hdr:hdr+8])
		}
	}
}
