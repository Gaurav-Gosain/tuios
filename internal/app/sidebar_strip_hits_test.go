package app

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The strip is three columns wide and draws its marks two rows apart, so the
// object the eye reads as "a session" is a 3x2 block. The rectangles used to be
// one cell tall and to stop short of the edge rule, which left two thirds of
// every visible slot dead and made the rail something you had to aim at. These
// pin the whole slot, cell by cell, off the frame the renderer actually drew.

// stripClick performs a whole click gesture at one cell: the press, plus the
// release the edge column's gesture needs to resolve into a click.
func stripClick(m *OS, x, y int) {
	m.SidebarClick(x, y, false)
	if m.SidebarEdgeActive() {
		m.SidebarEdgeRelease(x, y)
	}
}

// stripHits renders a collapsed strip on the given side and hands back the
// model with the rectangles it recorded while drawing.
func stripHits(t *testing.T, pos string, h int) *OS {
	t.Helper()
	m, tree := stripOS(t, 120, h)
	withSidebar(t, true, pos, config.SidebarDefaultWidth)
	m.SidebarCollapsed = true
	m.SidebarFocused = true
	m.sidebarPanelLinesForTree(tree)
	return m
}

// TestStripTargetsOwnEveryCellOfTheirSlot is the hitbox claim: every target on
// the strip claims the full band width, edge rule included, and every row of the
// slot it draws into, and each of those cells resolves back to it.
func TestStripTargetsOwnEveryCellOfTheirSlot(t *testing.T) {
	for _, pos := range []string{"left", "right"} {
		m := stripHits(t, pos, 20)
		w := m.GetSidebarWidth()
		railX0 := 0
		if pos == "right" {
			railX0 = m.GetRenderWidth() - w
		}
		if len(m.SidebarHits) == 0 {
			t.Fatalf("%s: the strip recorded no targets at all", pos)
		}
		for i, h := range m.SidebarHits {
			if h.X0 != railX0 || h.X1 != railX0+w {
				t.Errorf("%s: target %d spans columns %d..%d, want the whole band %d..%d",
					pos, i, h.X0, h.X1, railX0, railX0+w)
			}
			for y := h.Y0; y < h.Y1; y++ {
				for x := h.X0; x < h.X1; x++ {
					got, ok := m.sidebarRowAt(x, y)
					if !ok {
						t.Fatalf("%s: cell (%d,%d) of target %d hits nothing", pos, x, y, i)
					}
					if got != h {
						t.Fatalf("%s: cell (%d,%d) of target %d resolves to %+v", pos, x, y, i, got)
					}
				}
			}
		}
	}
}

// TestStripSessionSlotIsTwoRowsTall: the mark and the blank row under it are one
// object, because the interval is what the eye reads as the row. A rectangle
// covering only the glyph's line is the mismatch that made the rail fiddly.
func TestStripSessionSlotIsTwoRowsTall(t *testing.T) {
	m := stripHits(t, "left", 20)
	sessions := 0
	for _, h := range m.SidebarHits {
		if h.Kind != sidebarRowSession {
			continue
		}
		sessions++
		if h.Y1-h.Y0 != 2 {
			t.Errorf("session %q claims %d rows, want the interval it is drawn at", h.SessionID, h.Y1-h.Y0)
		}
	}
	if sessions != 3 {
		t.Fatalf("%d session targets, want one per session", sessions)
	}

	// Packed (a rail too short to space the marks), a slot is one row, and the
	// rectangles still tile the spine with no gap between them.
	short, tree := manySessionsOS(t, 120, 9)
	short.sidebarPanelLinesForTree(tree)
	prev := -1
	for _, h := range short.SidebarHits {
		if h.Kind != sidebarRowSession {
			continue
		}
		if h.Y1-h.Y0 != 1 {
			t.Errorf("a packed session claims %d rows, want 1", h.Y1-h.Y0)
		}
		if prev >= 0 && h.Y0 != prev {
			t.Errorf("a packed slot starts at %d, leaving row %d unclaimed", h.Y0, prev)
		}
		prev = h.Y1
	}
}

// TestStripSessionClickLandsFromAnyCellOfItsSlot drives the pointer at the four
// corners of a session's block, which is where a real hand lands when it is not
// aiming: the first and last row of the slot, at both band edges, on both sides
// of the screen.
func TestStripSessionClickLandsFromAnyCellOfItsSlot(t *testing.T) {
	for _, pos := range []string{"left", "right"} {
		for _, corner := range []struct{ lastRow, lastCol bool }{
			{false, false}, {false, true}, {true, false}, {true, true},
		} {
			name := fmt.Sprintf("%s/row=%v/col=%v", pos, corner.lastRow, corner.lastCol)
			m := stripHits(t, pos, 20)
			// The attached session: the gesture can then be driven all the way
			// through the release without a daemon to switch against, and the
			// cursor still has to travel (it starts on the badge).
			var target sidebarRowHit
			for _, h := range m.SidebarHits {
				if h.Kind == sidebarRowSession && h.SessionID == "main" {
					target = h
				}
			}
			if target.SessionID == "" {
				t.Fatalf("%s: no target for the main session", name)
			}
			x, y := target.X0, target.Y0
			if corner.lastCol {
				x = target.X1 - 1
			}
			if corner.lastRow {
				y = target.Y1 - 1
			}

			if !m.SidebarClick(x, y, false) {
				t.Fatalf("%s: the strip did not consume a click at (%d,%d)", name, x, y)
			}
			// Either the row gesture is armed on that session, or the edge column
			// is holding the click until the release proves it was not a resize.
			switch {
			case m.SidebarDrag.PressActive:
				if m.SidebarDrag.SessionID != "main" {
					t.Errorf("%s: the press armed %q, want main", name, m.SidebarDrag.SessionID)
				}
				m.SidebarRelease(x, y)
			case m.SidebarEdgeActive():
				if !m.SidebarEdge.HaveRow || m.SidebarEdge.Row.SessionID != "main" {
					t.Errorf("%s: the edge press holds %+v, want the main row", name, m.SidebarEdge.Row)
				}
				m.SidebarEdgeRelease(x, y)
			default:
				t.Fatalf("%s: the click at (%d,%d) selected nothing", name, x, y)
			}
			// Mouse and keyboard share one cursor, so "selected" is observable
			// without a daemon to switch against.
			if row, ok := m.sidebarCursorRow(); !ok || row.SessionID != "main" {
				t.Errorf("%s: the cursor is on %+v, want the main session", name, row)
			}
		}
	}
}

// TestStripEdgeColumnStillResizesWhenTheGestureMoves: the edge rule is the width
// handle, and the click borrowed from it must give it straight back the moment
// the pointer leaves the column it was pressed on.
func TestStripEdgeColumnStillResizesWhenTheGestureMoves(t *testing.T) {
	m := stripHits(t, "left", 20)
	edgeX := m.GetSidebarWidth() - 1
	top := m.GetTopMargin()
	m.DaemonClient = nil // the resize re-lays the panes, which syncs to the daemon

	if !m.SidebarClick(edgeX, top+1, false) || !m.SidebarEdgeActive() {
		t.Fatal("a press on the strip's edge rule did not arm the resize")
	}
	m.SidebarEdgeMotion(24, top+1)
	if m.SidebarEdge.HaveRow {
		t.Error("the resize drag is still holding a pending row click")
	}
	m.SidebarEdgeRelease(24, top+1)
	if m.SidebarCollapsed {
		t.Error("dragging the strip's edge out left the rail collapsed")
	}
	if config.SidebarWidth != 25 {
		t.Errorf("the drag set the width to %d, want the pointer's column", config.SidebarWidth)
	}
}

// TestStripToggleIsClickableAcrossItsWholeRow: the strip's one control is the
// only way back for a user who is not going to guess a keybind, so it takes a
// click on any of the rail's three columns, on either side of the screen.
func TestStripToggleIsClickableAcrossItsWholeRow(t *testing.T) {
	for _, pos := range []string{"left", "right"} {
		for col := range 3 {
			m := stripHits(t, pos, 20)
			var toggle sidebarRowHit
			for _, h := range m.SidebarHits {
				if h.Kind == sidebarRowCollapse {
					toggle = h
				}
			}
			// The expand re-lays the panes, which syncs to the daemon; the stub
			// client in the fixture is not one, and this test is about the click.
			m.DaemonClient = nil
			stripClick(m, toggle.X0+col, toggle.Y0)
			if m.SidebarCollapsed {
				t.Errorf("%s: a click on the toggle's column %d left the rail collapsed", pos, col)
			}
		}
	}
}

// TestStripBadgeGoesToWhatItIsCountingFrom: the badge is the largest, loudest
// object on the strip, and it used to be the only one that did nothing. It
// carries the same target the pane it counts from would.
func TestStripBadgeGoesToWhatItIsCountingFrom(t *testing.T) {
	m := stripHits(t, "left", 20)
	var badge sidebarStripRow
	for _, r := range m.sidebarStripRows {
		if r.Kind == sidebarStripBadge {
			badge = r
		}
	}
	if badge.Y1 == 0 {
		t.Fatal("the strip drew no badge")
	}
	for col := range 3 {
		hit, ok := m.sidebarRowAt(col, badge.Y0)
		if !ok {
			t.Fatalf("column %d of the badge hits nothing", col)
		}
		// stripOS puts the errored pane in the api session, which is what the
		// badge rolls up and so what it has to address.
		if hit.Kind != sidebarRowAgent || hit.SessionID != "api" || hit.WindowID != "eeeeeeee5555" {
			t.Errorf("the badge at column %d points at %+v, want the errored pane", col, hit)
		}
	}
}

// TestStripMoreMarkExpandsTheRail: the tail says a number of sessions are not
// drawn, and expanding is the only way to see them, so that is what clicking it
// does rather than nothing.
func TestStripMoreMarkExpandsTheRail(t *testing.T) {
	m, tree := manySessionsOS(t, 120, 9)
	m.sidebarPanelLinesForTree(tree)

	var more sidebarStripRow
	for _, r := range m.sidebarStripRows {
		if r.Kind == sidebarStripMore {
			more = r
		}
	}
	if more.Y1 == 0 {
		t.Fatal("the short strip drew no tail mark")
	}
	hit, ok := m.sidebarRowAt(1, more.Y0)
	if !ok || hit.Kind != sidebarRowCollapse {
		t.Fatalf("the tail mark hits %+v, want the expand target", hit)
	}
	m.DaemonClient = nil
	stripClick(m, 1, more.Y0)
	if m.SidebarCollapsed {
		t.Error("clicking the tail mark left the rail collapsed")
	}
}

// TestStripHoverPaintsTheWholeSlot: the highlight is the target made visible, so
// it has to cover the blank row the slot owns. A highlight one row tall under a
// two-row target teaches the wrong hitbox.
func TestStripHoverPaintsTheWholeSlot(t *testing.T) {
	m, tree := quietStripOS(t, 120, 20)
	m.sidebarPanelLinesForTree(tree)
	var slot sidebarRowHit
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowSession && h.SessionID == "api" {
			slot = h
		}
	}
	if slot.Y1-slot.Y0 != 2 {
		t.Fatalf("the api slot is %d rows, want 2", slot.Y1-slot.Y0)
	}

	m.SidebarHoverActive = true
	m.SidebarHoverX, m.SidebarHoverY = 1, slot.Y0
	lines, _ := m.sidebarPanelLinesForTree(tree)

	panel := panelSGR(t)
	top := m.GetTopMargin()
	for y := slot.Y0; y < slot.Y1; y++ {
		for x, cell := range stripCells(lines[y-top]) {
			if x == m.GetSidebarWidth()-1 {
				continue // the edge rule keeps the band's own ground
			}
			if bgOf(cell) == panel {
				t.Errorf("cell (%d,%d) of the hovered slot is unhighlighted", x, y-top)
			}
		}
	}
	// The row under the slot is nobody's, and must stay quiet.
	for x, cell := range stripCells(lines[slot.Y1-top]) {
		if bg := bgOf(cell); bg != panel {
			t.Errorf("cell (%d,%d) below the hovered slot picked up a fill %q", x, slot.Y1-top, bg)
		}
	}
}
