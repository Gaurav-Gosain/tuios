package app

import (
	"strconv"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
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

// pillCapsOS is pillOS with the Nerd Font glyph set on, which is the state the
// rounded caps are drawn in. Every glyph the bar uses is still one cell wide, so
// dockBarRow's rune-per-column assertion holds and a recorded rectangle can
// still be compared against the cells that were painted in it.
func pillCapsOS(t *testing.T, w int, names map[int]string, workspaces ...int) *OS {
	t.Helper()
	prev := config.UseASCIIOnly
	t.Cleanup(func() { config.UseASCIIOnly = prev })
	m := pillOS(t, w, names, workspaces...)
	config.UseASCIIOnly = false
	return m
}

// pillText is what a pill carrying label draws: a column of padding either side
// of the label, inside the rounded caps the glyph set provides.
func pillText(label string) string {
	return config.GetDockWorkspaceCapLeft() + " " + label + " " + config.GetDockWorkspaceCapRight()
}

// TestWorkspacePillRectsMatchTheirDrawnCells is the invariant a named workspace
// puts at risk. The pills are as wide as the names on them, so a rect cut from
// anything other than the drawn geometry lands a click on the neighbour, which
// is how a minimized dock entry came to be unclickable while the cell to its
// right worked.
//
// Both edge columns of every pill are walked, at three dock widths, in both
// glyph sets, and with named and unnamed workspaces in the same strip. With the
// caps on those edge columns are the caps themselves, which is the case worth
// walking: a rounded end drawn outside the rectangle is a pill whose visible
// shape is wider than the thing you can click.
func TestWorkspacePillRectsMatchTheirDrawnCells(t *testing.T) {
	names := map[int]string{2: "review", 4: "deploy-fix"}
	for _, capped := range []bool{false, true} {
		for _, w := range []int{160, 100, 64} {
			t.Run(strconv.FormatBool(capped)+"/"+strconv.Itoa(w), func(t *testing.T) {
				build := pillOS
				if capped {
					build = pillCapsOS
				}
				m := build(t, w, names, 1, 2, 3, 4)
				row := dockBarRow(t, m)
				if len(m.dockWorkspaceHits) == 0 {
					t.Fatal("the strip recorded no rectangles")
				}
				lc := config.GetDockWorkspaceCapLeft()
				if capped == (lc == "") {
					t.Fatalf("the glyph set is wrong for this case: left cap %q with capped=%v", lc, capped)
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
					if capped {
						// The cap columns are the rect's own first and last, so
						// the shape and the target are the same rectangle.
						if got := cells(row, h.X0, h.X0+1); got != lc {
							t.Errorf("workspace %d's rect opens on %q, not its left cap %q", h.Workspace, got, lc)
						}
						if rc := config.GetDockWorkspaceCapRight(); cells(row, h.X1-1, h.X1) != rc {
							t.Errorf("workspace %d's rect ends on %q, not its right cap %q",
								h.Workspace, cells(row, h.X1-1, h.X1), rc)
						}
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

// TestWorkspacePillsKeepTheirCapsWhateverTheDockDoes: dock_pill_caps is about
// the mode chip and the minimized run, where a cap on every entry turned the row
// into beads. The workspace pills are tabs and keep their shape either way, so a
// flat dock is still a dock with rounded workspace tabs in it.
func TestWorkspacePillsKeepTheirCapsWhateverTheDockDoes(t *testing.T) {
	for _, flat := range []bool{false, true} {
		t.Run(strconv.FormatBool(flat), func(t *testing.T) {
			prev := config.DockPillCaps
			config.DockPillCaps = !flat
			t.Cleanup(func() { config.DockPillCaps = prev })

			m := pillCapsOS(t, 120, map[int]string{2: "review"}, 1, 2, 3)
			row := dockBarRow(t, m)
			for _, h := range m.dockWorkspaceHits {
				if got := cells(row, h.X0, h.X0+1); got != config.GetDockWorkspaceCapLeft() {
					t.Errorf("workspace %d lost its left cap with dock_pill_caps=%v: %q",
						h.Workspace, !flat, got)
				}
			}
		})
	}
}

// TestWorkspacePillsDropTheCapsUnderASCII: a half circle has no 7-bit stand-in,
// so the ASCII strip draws the fill alone rather than bracketing every pill.
// The rectangles have to follow it down, or a terminal without the font records
// two columns per pill that nothing was painted in.
func TestWorkspacePillsDropTheCapsUnderASCII(t *testing.T) {
	m := pillOS(t, 120, map[int]string{2: "review"}, 1, 2, 3)
	if got := config.GetDockWorkspaceCapLeft() + config.GetDockWorkspaceCapRight(); got != "" {
		t.Fatalf("the ASCII strip still has caps: %q", got)
	}
	row := dockBarRow(t, m)
	for _, h := range m.dockWorkspaceHits {
		label := "+"
		if h.Workspace > 0 {
			label = m.workspacePillLabel(h.Workspace)
		}
		if got, want := cells(row, h.X0, h.X1), " "+label+" "; got != want {
			t.Errorf("workspace %d's rect covers %q, but its uncapped pill draws %q", h.Workspace, got, want)
		}
	}
}

// TestActiveWorkspacePillReadsAsActive checks the drawn dock rather than the
// pill in isolation: the styled run the renderer emitted has to be the active
// one, underline and all, or the strip is a row of identical pills that never
// says which workspace you are on.
func TestActiveWorkspacePillReadsAsActive(t *testing.T) {
	m := pillOS(t, 120, map[int]string{2: "review"}, 1, 2, 3)
	m.CurrentWorkspace = 2
	dock, _ := m.renderDockString()

	pal := theme.UI()
	active := workspacePill("review", true, false, pal)
	if !strings.Contains(dock, active) {
		t.Errorf("the dock does not draw workspace 2's pill as the active one: %q", active)
	}
	if !isUnderlined(active) {
		t.Error("the active pill lost the underline, which is the whole of its emphasis")
	}
	// The resting pills carry the fill but not the emphasis.
	resting := workspacePill("1", false, false, pal)
	if !strings.Contains(dock, resting) {
		t.Errorf("the dock does not draw workspace 1's pill at rest: %q", resting)
	}
	if isUnderlined(resting) {
		t.Error("a resting pill is underlined, so every pill reads as current")
	}
}

// isUnderlined reports whether any SGR sequence in s sets the underline
// attribute. The parameters arrive merged with the colours, so the sequence is
// parsed rather than matched as a literal.
func isUnderlined(s string) bool {
	for _, seq := range strings.Split(s, "\x1b[") {
		end := strings.IndexByte(seq, 'm')
		if end < 0 {
			continue
		}
		params := strings.Split(seq[:end], ";")
		for i := 0; i < len(params); i++ {
			// A colour carries its channels as parameters of its own, and one of
			// them may well be a 4.
			if p := params[i]; p == "38" || p == "48" {
				if i+1 < len(params) && params[i+1] == "5" {
					i += 2
					continue
				}
				i += 4
				continue
			}
			if params[i] == "4" {
				return true
			}
		}
	}
	return false
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

// TestWorkspaceStripKeepsTheActivePillInViewOnResize: the strip can start
// scrolling without the user asking, when the terminal narrows. The pill that
// says where you are cannot be what the narrowing pushes out.
func TestWorkspaceStripKeepsTheActivePillInViewOnResize(t *testing.T) {
	m := overflowOS(t)
	m.Width, m.EffectiveWidth = 200, 200
	m.SwitchToWorkspace(6)
	dockBarRow(t, m)
	if len(m.dockWorkspaceArrowHits) != 0 {
		t.Fatal("the strip already scrolls at 200 columns, so the narrowing proves nothing")
	}

	m.Width, m.EffectiveWidth = 90, 90
	dockBarRow(t, m)
	if len(m.dockWorkspaceArrowHits) == 0 {
		t.Fatal("the strip did not start scrolling when the dock narrowed")
	}
	for _, h := range m.dockWorkspaceHits {
		if h.Workspace == 6 {
			return
		}
	}
	t.Errorf("narrowing the dock pushed the current workspace off the strip: %v", m.dockWorkspaceHits)
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
