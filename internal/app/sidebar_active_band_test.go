package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// activeBandOS builds a client attached to session "main" with one sibling,
// the way a daemon-backed client sees two sessions.
func activeBandOS(t *testing.T) (*OS, sessiontree.Tree) {
	t.Helper()
	m := sidebarTestOS(t, 120, 40, "left")
	m.SessionName = "main"
	tree := sessiontree.Build([]sessiontree.SessionInput{
		{Name: "main", Attached: true, IsCurrent: true, WindowCount: 1},
		{Name: "scratch", WindowCount: 1},
	})
	return m, tree
}

// TestActiveSessionBandAndBold pins the two things that make the attached
// session findable at a glance: a band under its row when the rail does not
// own the keyboard, and bold on its name. A resting sibling row carries
// neither.
func TestActiveSessionBandAndBold(t *testing.T) {
	m, tree := activeBandOS(t)
	lines, _ := m.sidebarPanelLinesForTree(tree)

	var current, resting string
	for _, l := range lines {
		if strings.Contains(l, "main") {
			current = l
		}
		if strings.Contains(l, "scratch") {
			resting = l
		}
	}
	if current == "" || resting == "" {
		t.Fatalf("session rows not found in the rail:\n%s", strings.Join(lines, "\n"))
	}

	// Bold arrives merged into one SGR with the colours ("[1;38;2;…"), so
	// the match is on the leading attribute, not a lone "[1m".
	if !strings.Contains(current, "\x1b[1;") {
		t.Errorf("the attached session's name is not bold:\n%q", current)
	}
	if strings.Contains(resting, "\x1b[1;") {
		t.Errorf("a resting session's row renders bold:\n%q", resting)
	}

	// The band: the attached row carries a background the resting row does
	// not. A 48-series escape means a set background; the resting row's
	// ground is the rail's own, so it draws none.
	if !strings.Contains(current, "\x1b[48;") {
		t.Errorf("the attached session's row has no band:\n%q", current)
	}
	if strings.Contains(resting, "\x1b[48;") {
		t.Errorf("a resting session's row draws a band:\n%q", resting)
	}
}
