package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The editor is checked by what it writes into the layout string, because that
// string is the whole of its output: the rail reads it, the config file holds
// it, and a keystroke that moved the highlight and not the layout is the
// failure these tests exist to catch.

// editorOS opens the rail layout editor on a fresh config.
func editorOS(t *testing.T) *OS {
	t.Helper()
	useTempConfig(t)
	withSections(t, config.SidebarDefaultSections)
	m := NewOS(OSOptions{UserConfig: config.DefaultConfig(), Width: 120, Height: 40})
	m.UserConfig.Appearance.Sidebar.Sections = config.SidebarDefaultSections
	m.OpenSectionEditor()
	return m
}

// selectRow puts the cursor on the first row of a name and kind.
func selectRow(t *testing.T, m *OS, name string, kind railRowKind) {
	t.Helper()
	for i, row := range m.sectionEditorRows() {
		if row.Name == name && row.Kind == kind {
			m.SectionEditorSelected = i
			return
		}
	}
	t.Fatalf("no %v row called %q in %v", kind, name, m.sectionEditorRows())
}

// TestSectionEditorKeepsOneSection stops the editor writing a layout that
// cannot be drawn.
//
// A layout with no section in it falls back to the shipped one on the next
// parse, so the last removal would appear to undo itself. Being told no beats
// an edit that silently does the opposite of what it said.
//
// Negative control, confirmed red: drop the sectionCount guard from
// SectionEditorToggle. The last section comes off and the rail comes back with
// all four.
func TestSectionEditorKeepsOneSection(t *testing.T) {
	m := editorOS(t)
	for _, name := range []string{"files", "terminals", "agents"} {
		selectRow(t, m, name, railRowPlaced)
		m.SectionEditorToggle()
	}
	if got := m.sectionLayout(); got != "sessions:25" {
		t.Fatalf("after taking three sections off the layout is %q", got)
	}
	selectRow(t, m, "sessions", railRowPlaced)
	m.SectionEditorToggle()
	if got := m.sectionLayout(); got != "sessions:25" {
		t.Errorf("the last section came off the rail, leaving %q", got)
	}
	var told bool
	for _, n := range m.Notifications {
		if strings.Contains(n.Message, "keeps one section") {
			told = true
		}
	}
	if !told {
		t.Error("the editor refused the edit and said nothing about it")
	}
}

// TestSectionEditorUndoLeavesAnUnnamedLayoutUnnamed keeps the editor from
// pinning somebody to today's defaults.
//
// A config that names no layout gets the one the rail ships with, whatever that
// grows into. An undo that wrote the four sections back out as a string would
// leave the file naming a layout its author never chose, and a fifth section
// added next year would not reach them.
//
// Negative control, confirmed red: have SectionEditorRevert restore the
// resolved layout rather than the raw field. The config comes back holding the
// default written out in full.
func TestSectionEditorUndoLeavesAnUnnamedLayoutUnnamed(t *testing.T) {
	useTempConfig(t)
	withSections(t, config.SidebarDefaultSections)
	m := NewOS(OSOptions{UserConfig: config.DefaultConfig(), Width: 120, Height: 40})
	m.UserConfig.Appearance.Sidebar.Sections = ""
	m.OpenSectionEditor()

	selectRow(t, m, "files", railRowPlaced)
	m.SectionEditorToggle()
	if m.UserConfig.Appearance.Sidebar.Sections == "" {
		t.Fatal("the edit wrote no layout")
	}
	m.SectionEditorRevert()
	if got := m.UserConfig.Appearance.Sidebar.Sections; got != "" {
		t.Errorf("undo left the config naming %q, want it naming none", got)
	}
	// And the rail is back on the shipped layout rather than stuck on the edit.
	if !sidebarLayoutHas(sidebarSectionFiles, &m.Settings) {
		t.Error("undo left the files section off the rail")
	}
}

// TestSectionEditorClickHitsTheRowItDrew keeps the click on the rectangles the
// renderer recorded as it drew them.
//
// Nothing recomputes a row's line from the selection and the scroll: that is a
// second copy of the arithmetic, and it is how a scrolled list starts sending a
// click to the row above the one under the pointer.
//
// Negative control, confirmed red: record the hits from the row index rather
// than from its drawn position (drop the "- start" from the rowY in
// renderSectionEditor). A scrolled panel routes the click to the wrong row.
func TestSectionEditorClickHitsTheRowItDrew(t *testing.T) {
	m := editorOS(t)
	// A panel short enough to scroll, and the selection at the bottom of the
	// list, because a list that fits cannot tell a rectangle taken from the
	// drawn line apart from one taken from the row index. That is the scrolled
	// case this test exists for.
	m.Height, m.Width = 22, 120
	_, visible, _ := m.sectionEditorLayout()
	for len(m.sectionEditorRows()) <= visible {
		selectRow(t, m, config.SidebarSectionSpacer, railRowAvailable)
		m.SectionEditorToggle()
	}
	rows := m.sectionEditorRows()
	m.SectionEditorSelected = len(rows) - 1
	_, geo, hits := m.renderSectionEditor()
	if m.SectionEditorScroll == 0 {
		t.Fatalf("the panel did not scroll with %d rows; the test premise is wrong", len(rows))
	}
	// The first rectangle is on the panel's first body line and points at the
	// first row the scroll left visible.
	if hits[0].Rect.Y0 != geo.BodyY {
		t.Errorf("the first rectangle is at %d, want the panel's first body line %d",
			hits[0].Rect.Y0, geo.BodyY)
	}
	if hits[0].Idx < m.SectionEditorScroll {
		t.Errorf("the first rectangle points at row %d, above the scroll at %d",
			hits[0].Idx, m.SectionEditorScroll)
	}
	if len(hits) == 0 {
		t.Fatal("the editor recorded no clickable rows")
	}
	for _, h := range hits {
		if h.Idx < 0 || h.Idx >= len(rows) {
			t.Fatalf("a rectangle points at row %d of %d", h.Idx, len(rows))
		}
		if h.Rect.Y0 < geo.BodyY || h.Rect.Y1 > geo.BodyY+geo.Height {
			t.Errorf("row %d has a rectangle at %d..%d, outside the panel body",
				h.Idx, h.Rect.Y0, h.Rect.Y1)
		}
		if rows[h.Idx].Kind == railRowHeader {
			t.Errorf("row %d is a heading and was recorded as clickable", h.Idx)
		}
	}
	// A rectangle's own row is the one it acts on: click the files entry and
	// files comes off the rail.
	var want int
	for i, row := range rows {
		if row.Name == "files" && row.Kind == railRowPlaced {
			want = i
		}
	}
	var target overlayRowHit
	for _, h := range hits {
		if h.Idx == want {
			target = h
		}
	}
	m.SectionEditorSelected = target.Idx
	m.SectionEditorToggle()
	if got := m.sectionLayout(); strings.Contains(got, "files") {
		t.Errorf("the rectangle for the files row left it on the rail: %q", got)
	}
}
