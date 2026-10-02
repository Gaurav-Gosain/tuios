//go:build !slim

package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestRailAgentsViewportHoldsItsRowWhenTheSortReorders is the one with real
// correctness content: a pane below the fold asking for a human hoists itself to
// the top of a priority-sorted list, and the reader must not be moved by it.
// Both addressing lists are checked, because they are two different mechanisms:
// the cursor is re-anchored by identity in sidebarPublishNav, the viewport in
// sidebarReanchorAgents, and either alone leaves the reader half-moved.
func TestRailAgentsViewportHoldsItsRowWhenTheSortReorders(t *testing.T) {
	const panes = 12
	m, _ := sectionsTestOS(t, 120, 30)
	m.SidebarFocused = true

	calm := agentsScrollTree(panes, nil)
	m.sidebarPanelLinesForTree(calm)
	m.SidebarScrollA = 2
	m.sidebarPanelLinesForTree(calm)

	before := railAgentRowIDs(m)
	if len(before) < 3 {
		t.Fatalf("the agents section drew %d rows, too few to scroll under a reader", len(before))
	}

	// The cursor rests one row into the visible block, which is where a reader
	// steering with j/k leaves it.
	held := before[1]
	for i, r := range m.SidebarNav {
		if r.Kind == sidebarRowAgent && r.WindowID == held {
			m.SidebarCursor = i
		}
	}

	// A pane the reader cannot see asks for a human. Priority puts it first, and
	// every row the reader was looking at moves down one.
	loudIdx := panes - 1
	if strings.Contains(strings.Join(before, " "), fmt.Sprintf("pane-%02d", loudIdx)) {
		t.Fatalf("the pane meant to be below the fold was on screen: %v", before)
	}
	m.sidebarPanelLinesForTree(agentsScrollTree(panes, map[int]string{loudIdx: "needs_input"}))

	after := railAgentRowIDs(m)
	if len(after) == 0 {
		t.Fatal("the agents section drew nothing after the reorder")
	}
	if after[0] != before[0] {
		t.Errorf("the agents viewport moved under the reader: it was showing %v, now %v", before, after)
	}
	row, ok := m.sidebarCursorRow()
	if !ok || row.WindowID != held {
		t.Errorf("the cursor followed the index instead of the row: was on %s, now on %+v", held, row)
	}
}

// TestRailAddressingHoldsAcrossEveryCombination walks the product of position,
// collapse, peek, filter, sort and height, and asserts the three things that
// have to be true of every frame: hits and nav name the same targets in the
// same order, no rectangle escapes the band or overlaps its predecessor, and
// the cursor lands on a row that exists.
func TestRailAddressingHoldsAcrossEveryCombination(t *testing.T) {
	// The agents section's row height is the sixth axis, and it is not one the
	// caller sets: it follows from the lines the section was given. The two
	// counters below make the sweep say which heights it actually walked, so a
	// budget change that quietly stopped producing one of them fails here rather
	// than leaving half of this test exercising nothing.
	var tall, short int
	for _, pos := range []string{"left", "right"} {
		for _, collapsed := range []bool{false, true} {
			for _, peek := range []string{"", "api", "gone"} {
				for _, filter := range []string{sidebarAgentsAll, sidebarAgentsSession} {
					for _, sortBy := range []string{sidebarAgentsPriority, sidebarAgentsRecent} {
						for _, h := range []int{30, 14, 9} {
							name := fmt.Sprintf("%s/collapsed=%v/peek=%q/%s/%s/h=%d", pos, collapsed, peek, filter, sortBy, h)
							t.Run(name, func(t *testing.T) {
								m, tree := sectionsTestOS(t, 120, h)
								withSidebar(t, true, pos, config.SidebarDefaultWidth)
								m.Settings = config.Global
								m.SidebarCollapsed = collapsed
								m.SidebarPeek = peek
								m.SidebarAgentFilter, m.SidebarAgentSort = filter, sortBy
								m.SidebarFocused = true
								m.sidebarPanelLinesForTree(tree)

								assertHitsFollowNav(t, m)
								assertHitsStayInTheBand(t, m)
								assertCursorIsOnARealRow(t, m)
								for _, hit := range m.SidebarHits {
									if hit.Kind != sidebarRowAgent {
										continue
									}
									if hit.Y1-hit.Y0 == sidebarAgentRowTall {
										tall++
									} else {
										short++
									}
								}
							})
						}
					}
				}
			}
		}
	}
	if tall == 0 || short == 0 {
		t.Errorf("the sweep drew %d tall agent rows and %d short ones; it is walking one height, not both", tall, short)
	}
}

// TestRailKillKeyOpensTheCursorRowsMenu is the bug itself: x on a terminal row
// used to show the session's menu.
func TestRailKillKeyOpensTheCursorRowsMenu(t *testing.T) {
	for _, tc := range []struct {
		name       string
		kind       sidebarRowKind
		wantTarget ContextMenuTarget
		wantWarn   string
	}{
		// "Kill session, go to next" comes first but is dimmed with no daemon
		// listing behind it, and selectWarn steps over dimmed rows the way the
		// arrow keys do.
		{"a session row", sidebarRowSession, CtxTargetDesktop, "Kill session and quit"},
		{"a terminal row", sidebarRowWindow, CtxTargetPane, "Close pane"},
		{"an agent row", sidebarRowAgent, CtxTargetPane, "Close pane"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, tree := sidebarMultiSessionOS(t, 120, 40)
			m.IsDaemonSession = true
			m.SidebarFocused = true
			m.sidebarPanelLinesForTree(tree)
			railCursorOnto(t, m, tc.kind)
			row, _ := m.sidebarCursorRow()

			m.SidebarOpenCursorMenu(true)

			cm := m.ContextMenu
			if cm == nil {
				t.Fatal("the kill key opened no menu")
			}
			if cm.Target != tc.wantTarget {
				t.Errorf("menu target = %v, want %v for %s", cm.Target, tc.wantTarget, tc.name)
			}
			// The menu opens on the row's own destructive action, which is what is
			// left of the key meaning "kill" once the row picks the menu.
			if cm.Selected < 0 || cm.Selected >= len(cm.Items) {
				t.Fatalf("selection %d is off the menu", cm.Selected)
			}
			if got := cm.Items[cm.Selected].Label; got != tc.wantWarn {
				t.Errorf("opened on %q, want %q", got, tc.wantWarn)
			}
			if !cm.Items[cm.Selected].Warn {
				t.Error("the selected row is not the destructive one")
			}
			// A pane menu must be about the pane the cursor named, not whichever
			// one happened to be focused.
			if tc.wantTarget == CtxTargetPane {
				if got := m.Windows[cm.WindowIndex].ID; got != row.WindowID {
					t.Errorf("menu is about pane %s, want the cursor row's %s", got, row.WindowID)
				}
			}
			if tc.wantTarget == CtxTargetDesktop && cm.SessionID != row.SessionID {
				t.Errorf("menu is about session %q, want the cursor row's %q", cm.SessionID, row.SessionID)
			}
		})
	}
}

// railAgentRowIDs is the windows the agents section actually drew, in the order
// it drew them. Read off the hit rectangles the renderer recorded, which is the
// only account of what reached the screen.
func railAgentRowIDs(m *OS) []string {
	var out []string
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowAgent {
			out = append(out, h.WindowID)
		}
	}
	return out
}

// railAgentRow returns the rendered lines of the agents-section row for a
// window, joined, or "" when the rail drew none. A row is one line or two, and
// which one a fact landed on is the layout's business rather than these tests'.
func railAgentRow(m *OS, lines []string, windowID string) string {
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowAgent && h.WindowID == windowID {
			top := h.Y0 - m.GetTopMargin()
			return strings.Join(lines[top:min(h.Y1-m.GetTopMargin(), len(lines))], "\n")
		}
	}
	return ""
}
