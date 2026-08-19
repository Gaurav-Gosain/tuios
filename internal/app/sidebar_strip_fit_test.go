package app

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// TestRailFitsAShortRegion walks heights 0-15 with the rail expanded, and says
// so: "the collapsed strip composes its own lines against the same height". It
// composes them from a different function, with its own badge, its own two
// stacked lists and its own two controls, and none of that was ever walked. The
// overrun that test exists for is a rail emitting more lines than its region,
// which paints over the dock and records hit rectangles outside the band, and
// the strip can do it exactly as easily.

// stripHeightOS is sectionsTestOS collapsed, which is the state under test.
func stripHeightOS(t *testing.T, w, h int, pos string) (*OS, sessiontree.Tree) {
	t.Helper()
	m, tree := sectionsTestOS(t, w, h)
	withSidebar(t, true, pos, config.SidebarDefaultWidth)
	m.SidebarCollapsed = true
	return m, tree
}

// TestCollapsedStripFitsAShortRegion is the expanded rail's sweep, applied to
// the strip on both sides.
func TestCollapsedStripFitsAShortRegion(t *testing.T) {
	for _, pos := range []string{"left", "right"} {
		for h := range 16 {
			t.Run(fmt.Sprintf("%s/h=%d", pos, h), func(t *testing.T) {
				m, tree := stripHeightOS(t, 120, h, pos)
				lines, _ := m.sidebarPanelLinesForTree(tree)

				if got, want := len(lines), m.GetUsableHeight(); got > want {
					t.Errorf("the strip drew %d rows into a region %d rows tall", got, want)
				}
				assertHitsFollowNav(t, m)
				assertHitsStayInTheBand(t, m)
				assertCursorIsOnARealRow(t, m)
			})
		}
	}
}

// TestCollapsedStripFitsWithFortySessions is the other end of the range the
// audit asked about. Three sessions fit any rail; forty is where a spine drawn
// at a fixed interval, a badge, an agents group and two pinned controls all
// want the same lines, and where whatever gives has to give without running the
// band past its region or stranding a hit rectangle outside it.
func TestCollapsedStripFitsWithFortySessions(t *testing.T) {
	for _, pos := range []string{"left", "right"} {
		for _, h := range []int{6, 12, 24, 40} {
			t.Run(fmt.Sprintf("%s/h=%d", pos, h), func(t *testing.T) {
				m, _ := stripHeightOS(t, 120, h, pos)

				inputs := make([]sessiontree.SessionInput, 0, 40)
				for i := range 40 {
					in := sessiontree.SessionInput{
						Name:             fmt.Sprintf("s%02d", i),
						CurrentWorkspace: 1,
						Windows: []sessiontree.WindowInput{
							{ID: fmt.Sprintf("w%02d", i), Title: "shell", Workspace: 1},
						},
					}
					// A handful want a human, so the badge and the agents group are
					// both live and competing with the spine for the same lines.
					if i%7 == 0 {
						in.Windows[0].AgentState = "needs_input"
					}
					if i == 0 {
						in.Attached, in.IsCurrent = true, true
					}
					inputs = append(inputs, in)
				}
				m.SessionName = "s00"
				tree := sessiontree.Build(inputs)

				lines, _ := m.sidebarPanelLinesForTree(tree)
				if got, want := len(lines), m.GetUsableHeight(); got > want {
					t.Errorf("forty sessions drew %d rows into a region %d rows tall", got, want)
				}
				assertHitsFollowNav(t, m)
				assertHitsStayInTheBand(t, m)
				assertCursorIsOnARealRow(t, m)

				// A list it cannot draw whole has to say so. Without the tail mark
				// the strip reports thirty-seven sessions as however many fit.
				if h >= 6 && !stripHasKind(m, sidebarStripMore) {
					t.Errorf("forty sessions in %d rows drew no tail mark, so the spine ends by stopping rather than by saying it is cut", h)
				}
			})
		}
	}
}

// stripHasKind reports whether the strip recorded a slot of the given kind.
func stripHasKind(m *OS, kind sidebarStripRowKind) bool {
	for _, r := range m.sidebarStripRows {
		if r.Kind == kind {
			return true
		}
	}
	return false
}
