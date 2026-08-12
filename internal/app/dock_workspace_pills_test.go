package app

import (
	"strconv"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// pillOS is a dock w columns wide with one window per listed workspace and the
// given names applied.
//
// ASCII glyphs are on throughout: every icon on the bar is then one cell, so a
// rune index into the drawn row is a screen column and a test can compare a
// recorded rectangle against the cells that were actually painted in it.
func pillOS(t *testing.T, w int, names map[int]string, workspaces ...int) *OS {
	t.Helper()
	prevTabs, prevASCII := config.DockWorkspaceTabs, config.UseASCIIOnly
	config.DockWorkspaceTabs, config.UseASCIIOnly = true, true
	t.Cleanup(func() { config.DockWorkspaceTabs, config.UseASCIIOnly = prevTabs, prevASCII })

	m := newNarrowOS(t, w, 30)
	m.NumWorkspaces = 9
	m.CurrentWorkspace = workspaces[0]
	m.Windows = make([]*terminal.Window, 0, len(workspaces))
	for i, ws := range workspaces {
		m.Windows = append(m.Windows, &terminal.Window{
			ID: "pill-" + strconv.Itoa(i), Width: 40, Height: 10, Workspace: ws,
		})
	}
	m.adoptSessionLabels(&session.SessionState{WorkspaceNames: names})
	return m
}

// overflowOS is a dock whose named workspaces cannot all fit its bar, which is
// the state the scrolling is for.
func overflowOS(t *testing.T) *OS {
	t.Helper()
	names := map[int]string{
		1: "editor", 2: "review", 3: "deploy-fix",
		4: "logs-tail", 5: "scratchpad", 6: "db-console",
	}
	return pillOS(t, 90, names, 1, 2, 3, 4, 5, 6)
}

// dockBarRow renders the dock and returns its bar row as plain text, asserting
// the row measures one cell per rune so the caller may index it by column.
func dockBarRow(t *testing.T, m *OS) string {
	t.Helper()
	dock, _ := m.renderDockString()
	rows := strings.Split(stripANSIForTrace(dock), "\n")
	row := rows[len(rows)-1]
	if config.DockbarPosition == "top" {
		row = rows[0]
	}
	if lipgloss.Width(row) != len([]rune(row)) {
		t.Fatalf("the bar row is %d cells over %d runes, so a column is not a rune here",
			lipgloss.Width(row), len([]rune(row)))
	}
	return row
}

// cells returns the drawn text of columns [x0, x1) of a bar row.
func cells(row string, x0, x1 int) string {
	r := []rune(row)
	if x0 < 0 || x1 > len(r) || x0 > x1 {
		return ""
	}
	return string(r[x0:x1])
}

// pillText is what a pill carrying label draws: a column of padding either side
// of the label, inside the caps when they are on.
func pillText(label string) string {
	return config.GetDockPillLeftChar() + " " + label + " " + config.GetDockPillRightChar()
}

// TestWorkspacePillRectsMatchTheirDrawnCells is the invariant a named workspace
// puts at risk. The pills are as wide as the names on them, so a rect cut from
// anything other than the drawn geometry lands a click on the neighbour, which
// is how a minimized dock entry came to be unclickable while the cell to its
// right worked.
//
// Both edge columns of every pill are walked, at three dock widths, with and
// without the caps, and with named and unnamed workspaces in the same strip.
func TestWorkspacePillRectsMatchTheirDrawnCells(t *testing.T) {
	names := map[int]string{2: "review", 4: "deploy-fix"}
	for _, caps := range []bool{false, true} {
		for _, w := range []int{160, 100, 64} {
			t.Run(strconv.FormatBool(caps)+"/"+strconv.Itoa(w), func(t *testing.T) {
				prev := config.DockPillCaps
				config.DockPillCaps = caps
				t.Cleanup(func() { config.DockPillCaps = prev })

				m := pillOS(t, w, names, 1, 2, 3, 4)
				row := dockBarRow(t, m)
				if len(m.dockWorkspaceHits) == 0 {
					t.Fatal("the strip recorded no rectangles")
				}

				for _, h := range m.dockWorkspaceHits {
					label := "+"
					if h.Workspace > 0 {
						label = m.workspacePillLabel(h.Workspace)
					}
					if got, want := cells(row, h.X0, h.X1), pillText(label); got != want {
						t.Errorf("workspace %d's rect [%d,%d) covers %q, but its pill draws %q",
							h.Workspace, h.X0, h.X1, got, want)
					}
					if h.Workspace == 0 {
						continue // the add pill resolves at click time
					}
					// Both edges, then the cells either side of them: the gap
					// between pills belongs to neither.
					for _, x := range []int{h.X0, h.X1 - 1} {
						if got := m.DockWorkspaceAt(x, h.Y); got != h.Workspace {
							t.Errorf("column %d of workspace %d's pill resolves to %d", x, h.Workspace, got)
						}
					}
					for _, x := range []int{h.X0 - 1, h.X1} {
						if got := m.DockWorkspaceAt(x, h.Y); got == h.Workspace {
							t.Errorf("column %d is outside workspace %d's pill but resolves to it", x, h.Workspace)
						}
					}
				}
			})
		}
	}
}

// TestWorkspaceStripDrawsTheWidthItPlanned: the layout pass hands the rest of
// the bar the columns the strip claimed, so a strip that draws one cell more
// than it planned pushes the "+" into the columns the bar's own truncation
// takes away, leaving a recorded rectangle over cells nobody can see.
func TestWorkspaceStripDrawsTheWidthItPlanned(t *testing.T) {
	names := map[int]string{1: "editor", 2: "review", 3: "deploy-fix", 4: "logs-tail", 5: "scratchpad"}
	for _, w := range []int{160, 100, 90, 64, 50, 40, 34, 28} {
		t.Run(strconv.Itoa(w), func(t *testing.T) {
			m := pillOS(t, w, names, 1, 2, 3, 4, 5)
			layout := m.CalculateDockLayout()
			drawn := m.renderDockWorkspaceStrip(layout.WorkspaceStrip, 0)
			if got := lipgloss.Width(drawn); got != layout.WorkspaceStrip.Width {
				t.Errorf("the strip draws %d cells but claimed %d: %q", got, layout.WorkspaceStrip.Width, drawn)
			}
		})
	}
}

// TestWorkspaceStripScrollsRatherThanTruncates: a strip too narrow for its
// workspaces shows fewer of them whole, never a clipped name. A half-drawn name
// is a workspace the user cannot identify, and the pill under it still claims
// its columns.
func TestWorkspaceStripScrollsRatherThanTruncates(t *testing.T) {
	m := overflowOS(t)
	row := dockBarRow(t, m)

	tabs := m.buildDockWorkspaceTabs()
	if len(m.dockWorkspaceHits) >= len(tabs) {
		t.Fatalf("the strip drew all %d tabs, so nothing overflowed", len(tabs))
	}
	if len(m.dockWorkspaceArrowHits) == 0 {
		t.Fatal("the strip overflowed without an arrow saying so")
	}
	for _, h := range m.dockWorkspaceHits {
		if h.Workspace == 0 {
			continue
		}
		label := m.workspacePillLabel(h.Workspace)
		if !strings.Contains(row, pillText(label)) {
			t.Errorf("workspace %d is drawn but its name %q is not whole in the row: %q", h.Workspace, label, row)
		}
	}
	// Scrolling to the end brings the workspaces that were cut off into view,
	// which is the difference between a strip that scrolls and one that drops
	// its tail.
	seen := map[int]bool{}
	for range tabs {
		for _, h := range m.dockWorkspaceHits {
			seen[h.Workspace] = true
		}
		right := arrowHit(m, 1)
		if right == nil {
			break
		}
		m.ScrollDockWorkspacesAt(right.X0, right.Y)
		dockBarRow(t, m)
	}
	for _, h := range m.dockWorkspaceHits {
		seen[h.Workspace] = true
	}
	for _, ws := range m.occupiedWorkspaces() {
		if !seen[ws] {
			t.Errorf("workspace %d could not be reached by scrolling the strip", ws)
		}
	}
}

// arrowHit returns the recorded arrow stepping the given direction, or nil.
func arrowHit(m *OS, delta int) *dockWorkspaceArrowHit {
	for i, h := range m.dockWorkspaceArrowHits {
		if h.Delta == delta {
			return &m.dockWorkspaceArrowHits[i]
		}
	}
	return nil
}

// TestWorkspaceStripArrowsFollowTheContent: an arrow is a claim that there is
// more that way, so each one is drawn only while that is true. An arrow on the
// right alone would be a lie the moment the strip has scrolled.
func TestWorkspaceStripArrowsFollowTheContent(t *testing.T) {
	m := overflowOS(t)
	dockBarRow(t, m)

	if arrowHit(m, -1) != nil {
		t.Error("the strip starts at its first pill but offers to scroll left")
	}
	right := arrowHit(m, 1)
	if right == nil {
		t.Fatal("the strip has workspaces past its right-hand end but no arrow to them")
	}

	// Step to the far end, then the arrows have swapped roles.
	for range m.buildDockWorkspaceTabs() {
		r := arrowHit(m, 1)
		if r == nil {
			break
		}
		m.ScrollDockWorkspacesAt(r.X0, r.Y)
		dockBarRow(t, m)
	}
	if arrowHit(m, 1) != nil {
		t.Error("the strip is at its last pill and still offers to scroll right")
	}
	if arrowHit(m, -1) == nil {
		t.Error("the strip has scrolled but does not offer the way back")
	}

	// An arrow's own columns are its own: they must not resolve to a workspace.
	for _, h := range m.dockWorkspaceArrowHits {
		for x := h.X0; x < h.X1; x++ {
			if ws := m.DockWorkspaceAt(x, h.Y); ws != 0 {
				t.Errorf("arrow column %d also resolves to workspace %d", x, ws)
			}
		}
	}
}

// TestWorkspaceStripArrowStepsOnePill pins the step. The pills are as wide as
// the names on them, so a page is a different distance every time and would
// skip past the workspace being reached for.
func TestWorkspaceStripArrowStepsOnePill(t *testing.T) {
	m := overflowOS(t)
	dockBarRow(t, m)

	before := m.dockWorkspaceHits[0].Workspace
	right := arrowHit(m, 1)
	if right == nil {
		t.Fatal("the strip did not overflow, so there is no arrow to step")
	}
	if !m.ScrollDockWorkspacesAt(right.X0, right.Y) {
		t.Fatal("the arrow's own first column did not take the click")
	}
	if !m.ScrollDockWorkspacesAt(right.X1-1, right.Y) {
		t.Fatal("the arrow's own last column did not take the click")
	}
	m.dockWorkspaceScroll-- // undo the second click, one step is what is under test
	dockBarRow(t, m)

	pills := m.occupiedWorkspaces()
	want := pills[indexOfWorkspace(pills, before)+1]
	if got := m.dockWorkspaceHits[0].Workspace; got != want {
		t.Errorf("one click moved the strip from workspace %d to %d, want %d", before, got, want)
	}
}

func indexOfWorkspace(ws []int, want int) int {
	for i, w := range ws {
		if w == want {
			return i
		}
	}
	return -1
}

// TestWorkspaceStripKeepsTheActivePillInView: the strip's job is to say where
// you are. A keyboard switch to a workspace scrolled off the end must bring its
// pill back, or the strip quietly points at the wrong one.
func TestWorkspaceStripKeepsTheActivePillInView(t *testing.T) {
	m := overflowOS(t)
	dockBarRow(t, m)

	drawn := func() map[int]bool {
		out := map[int]bool{}
		for _, h := range m.dockWorkspaceHits {
			out[h.Workspace] = true
		}
		return out
	}
	if drawn()[4] {
		t.Fatal("workspace 4 is already in view, so the switch under test proves nothing")
	}

	m.SwitchToWorkspace(4) // the keyboard path: no arrow was touched
	dockBarRow(t, m)
	if !drawn()[4] {
		t.Errorf("switching to workspace 4 left its pill off the strip: %v", m.dockWorkspaceHits)
	}

	// And back the other way, from the far end to the first workspace.
	m.SwitchToWorkspace(1)
	dockBarRow(t, m)
	if !drawn()[1] {
		t.Errorf("switching back to workspace 1 left its pill off the strip: %v", m.dockWorkspaceHits)
	}
}

// TestWorkspaceStripLeavesTheRestOfTheBarAlone: the "+", the readout and the
// session controls are not workspaces, so the strip may not spend their columns
// on itself however many workspaces there are.
func TestWorkspaceStripLeavesTheRestOfTheBarAlone(t *testing.T) {
	names := map[int]string{1: "editor", 2: "review", 3: "deploy-fix", 4: "logs-tail", 5: "scratchpad"}
	for _, w := range []int{34, 40, 60, 100} {
		t.Run(strconv.Itoa(w), func(t *testing.T) {
			m := pillOS(t, w, names, 1, 2, 3, 4, 5)
			row := dockBarRow(t, m)

			if lipgloss.Width(row) != m.GetRenderWidth() {
				t.Errorf("the bar is %d cells on a %d-column screen", lipgloss.Width(row), m.GetRenderWidth())
			}
			// The control that makes a workspace is pinned outside the scrolling
			// run, so it is there whatever the strip is doing.
			add := false
			for _, h := range m.dockWorkspaceHits {
				if h.Workspace == 0 {
					add = true
					if got := m.DockWorkspaceAt(h.X0, h.Y); got == 0 {
						t.Error("the add pill's first column resolves to no workspace")
					}
				}
			}
			if !add {
				t.Error("the add pill was swallowed by the strip")
			}
			// The dock's live pane count, which the e2e harness reads.
			if want := " " + strconv.Itoa(m.CurrentWorkspace) + ":"; !strings.Contains(row, want) {
				t.Errorf("the bar lost its %q readout: %q", want, row)
			}
			if len(m.dockSessionHits) == 0 {
				t.Fatalf("the session controls are off at %d columns", w)
			}
			// Nothing the strip drew may reach the controls' columns.
			for _, s := range m.dockSessionHits {
				for _, h := range m.dockWorkspaceHits {
					if h.Y == s.Y && h.X1 > s.X0 {
						t.Errorf("workspace %d's pill ends at %d, inside the session control at %d",
							h.Workspace, h.X1, s.X0)
					}
				}
				for _, a := range m.dockWorkspaceArrowHits {
					if a.Y == s.Y && a.X1 > s.X0 {
						t.Errorf("an overflow arrow ends at %d, inside the session control at %d", a.X1, s.X0)
					}
				}
			}
		})
	}
}
