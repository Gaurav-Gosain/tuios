package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
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

// TestSectionEditorMovesASection is the reorder, which the maintainer asked for
// and which was already possible by typing the string.
//
// The claim is that the keystroke writes the layout, and that the highlight
// rides with the entry it moved. A shift that reordered the list and left the
// cursor on a fixed index is a second keystroke moving a different section.
//
// Negative control, confirmed red: drop the m.SectionEditorSelected += dir from
// SectionEditorShift. The layout is right and the second shift moves terminals
// instead, so the two-step case fails.
func TestSectionEditorMovesASection(t *testing.T) {
	m := editorOS(t)
	selectRow(t, m, "files", railRowPlaced)
	m.SectionEditorShift(-1)
	if got := m.sectionLayout(); got != "sessions:25,files:25,terminals,agents:34" {
		t.Fatalf("after one move up the layout is %q", got)
	}
	// Again, and the same entry moves: the cursor followed it.
	m.SectionEditorShift(-1)
	if got := m.sectionLayout(); got != "files:25,sessions:25,terminals,agents:34" {
		t.Errorf("after two moves up the layout is %q", got)
	}
	// And it stops at the top rather than wrapping to the bottom.
	m.SectionEditorShift(-1)
	if got := m.sectionLayout(); got != "files:25,sessions:25,terminals,agents:34" {
		t.Errorf("a move past the top changed the layout to %q", got)
	}
}

// TestSectionEditorTurnsTheFilesSectionOff is the control the maintainer asked
// for in the words he asked for it: a way to show or hide the files section.
//
// It is here rather than in a show_files boolean because the layout has to own
// membership: a layout may carry two spacers and a boolean per section has
// nowhere to put the second one. So "hide it" is "take it out of the list", and
// the way back is the second list in the same panel.
//
// Negative control, confirmed red: make the railRowPlaced branch of
// SectionEditorToggle return nil. The section stays on the rail.
func TestSectionEditorTurnsTheFilesSectionOff(t *testing.T) {
	m := editorOS(t)
	selectRow(t, m, "files", railRowPlaced)
	m.SectionEditorToggle()
	if got := m.sectionLayout(); got != "sessions:25,terminals,agents:34" {
		t.Fatalf("after switching files off the layout is %q", got)
	}
	// The rail agrees, which is the half a string comparison cannot see.
	if sidebarLayoutHas(sidebarSectionFiles) {
		t.Error("the rail still lays out a files section")
	}
	// And it is now in the second list, one keystroke from coming back.
	selectRow(t, m, "files", railRowAvailable)
	m.SectionEditorToggle()
	if got := m.sectionLayout(); got != "sessions:25,terminals,agents:34,files" {
		t.Errorf("after switching files back on the layout is %q", got)
	}
	if !sidebarLayoutHas(sidebarSectionFiles) {
		t.Error("the rail did not take the files section back")
	}
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

// TestSectionEditorAddsTwoSpacers is the ask that decided the whole design.
//
// Two spacers in two places is a layout a boolean per section cannot express at
// all, and the editor has to be able to make one: the spacer row stays in the
// second list after it is used, because it is a thing you add rather than a
// thing you move.
//
// Negative control, confirmed red: remove the spacer row from the second list
// once one is placed (return early when a spacer is in entries). The second
// enter has no row to land on and the layout keeps one spacer.
func TestSectionEditorAddsTwoSpacers(t *testing.T) {
	m := editorOS(t)
	selectRow(t, m, config.SidebarSectionSpacer, railRowAvailable)
	m.SectionEditorToggle()
	selectRow(t, m, config.SidebarSectionSpacer, railRowAvailable)
	m.SectionEditorToggle()
	got := m.sectionLayout()
	if strings.Count(got, "spacer") != 2 {
		t.Fatalf("two presses on the spacer row made %q", got)
	}
	// And they are two entries in the rail's plan, not one read twice.
	plans, _ := sidebarLayoutFor(got)
	spacers := 0
	for _, p := range plans {
		if p.Spacer {
			spacers++
		}
	}
	if spacers != 2 {
		t.Errorf("the rail read %q as %d spacers", got, spacers)
	}
}

// TestSectionEditorWalksTheShare is the percentage control, on the entry and on
// a spacer both. Zero is auto, which is the grammar's own word for the entry
// that takes what the others leave.
//
// Negative control, confirmed red: clamp the share to 1..100 instead of 0..100
// in SectionEditorShare. Walking it down never reaches auto and the last case
// fails.
func TestSectionEditorWalksTheShare(t *testing.T) {
	m := editorOS(t)
	selectRow(t, m, "sessions", railRowPlaced)
	m.SectionEditorShare(1)
	if got := m.sectionLayout(); !strings.HasPrefix(got, "sessions:30,") {
		t.Errorf("one step up put sessions at %q", got)
	}
	for range 10 {
		m.SectionEditorShare(-1)
	}
	if got := m.sectionLayout(); !strings.HasPrefix(got, "sessions,") {
		t.Errorf("walking the share to the bottom left %q, want sessions back on auto", got)
	}
	// A spacer takes one too, which is what makes a gap you can rely on.
	selectRow(t, m, config.SidebarSectionSpacer, railRowAvailable)
	m.SectionEditorToggle()
	m.SectionEditorShare(3)
	if got := m.sectionLayout(); !strings.HasSuffix(got, ",spacer:15") {
		t.Errorf("the spacer's share came out as %q", got)
	}
}

// TestSectionEditorUndoAndReset are the two ways back, matching the dock
// editor's. Undo is the layout the editor opened on; reset is the one the rail
// ships with.
//
// Negative control, confirmed red: make SectionEditorRevert commit the default
// layout rather than the remembered one. Undo then lands on the shipped layout
// instead of the one this test opened with.
func TestSectionEditorUndoAndReset(t *testing.T) {
	m := editorOS(t)
	m.UserConfig.Appearance.Sidebar.Sections = "terminals,sessions"
	config.SidebarSections = "terminals,sessions"
	m.OpenSectionEditor()

	selectRow(t, m, "sessions", railRowPlaced)
	m.SectionEditorShift(-1)
	if got := m.sectionLayout(); got != "sessions,terminals" {
		t.Fatalf("the edit did not land: %q", got)
	}
	m.SectionEditorRevert()
	if got := m.sectionLayout(); got != "terminals,sessions" {
		t.Errorf("undo left %q, want the layout the editor opened on", got)
	}
	m.SectionEditorReset()
	if got := m.sectionLayout(); got != config.SidebarDefaultSections {
		t.Errorf("reset left %q, want the shipped layout", got)
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
	if !sidebarLayoutHas(sidebarSectionFiles) {
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

// TestSectionEditorKeysMatchTheDockEditor is the habit claim. A person who has
// laid out the dock should not have to learn a second set of keys, so each key
// the dock editor takes does the same kind of thing here.
//
// Each key is driven from a state where it must do something, because "the
// editor took the key" and "the key happened to be a no-op on this row" look
// the same from outside and only one of them is the claim.
//
// Negative control, confirmed red: drop the "shift+up", "K" case from
// handleSectionEditorInput and from editorKey. The move cases fail.
func TestSectionEditorKeysMatchTheDockEditor(t *testing.T) {
	for _, tc := range []struct {
		key  string
		what string
	}{
		{"down", "select"},
		{"j", "select"},
		{"up", "select"},
		{"k", "select"},
		{"shift+down", "move"},
		{"J", "move"},
		{"shift+up", "move"},
		{"K", "move"},
		{"enter", "add or remove"},
		{"space", "add or remove"},
		{"left", "share"},
		{"right", "share"},
		{"r", "reset"},
		{"u", "undo"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			m := editorOS(t)
			// A middle entry, so a move has somewhere to go in both directions,
			// and something to undo, so undo is not a no-op.
			selectRow(t, m, "terminals", railRowPlaced)
			m.SectionEditorShare(1)
			before, wasAt := m.sectionLayout(), m.SectionEditorSelected

			cmd := editorKey(m, tc.key)
			if cmd == nil && m.sectionLayout() == before && m.SectionEditorSelected == wasAt {
				t.Errorf("the editor ignores %q, which the dock editor takes for %s", tc.key, tc.what)
			}
		})
	}
}

// TestSectionEditorClosesOnEscAndKeeps is the dock editor's other contract:
// every edit was applied and saved as it was made, so Esc has nothing to
// abandon and closing keeps the layout.
func TestSectionEditorClosesOnEscAndKeeps(t *testing.T) {
	m := editorOS(t)
	selectRow(t, m, "files", railRowPlaced)
	m.SectionEditorToggle()
	want := m.sectionLayout()

	m.CloseSectionEditor()
	if m.ShowSectionEditor {
		t.Error("closing left the editor open")
	}
	if got := m.sectionLayout(); got != want {
		t.Errorf("closing threw the edit away, leaving %q, want %q", got, want)
	}
}

// editorKey runs one key through the editor the way the input layer does. It is
// a copy of handleSectionEditorInput's switch, kept here because the input
// package holds the real one and importing it from a test in app would be a
// cycle. TestSectionEditorInputRoutesTheKeys is what keeps the two in step.
func editorKey(m *OS, key string) tea.Cmd {
	switch key {
	case "enter", "space":
		return m.SectionEditorToggle()
	case "up", "ctrl+p", "k":
		m.SectionEditorMove(-1)
	case "down", "ctrl+n", "j":
		m.SectionEditorMove(1)
	case "shift+up", "K":
		return m.SectionEditorShift(-1)
	case "shift+down", "J":
		return m.SectionEditorShift(1)
	case "left", "h":
		return m.SectionEditorShare(-1)
	case "right", "l":
		return m.SectionEditorShare(1)
	case "r":
		return m.SectionEditorReset()
	case "u":
		return m.SectionEditorRevert()
	}
	return nil
}

// TestEditorClaimsNothingItDidNotMeasure is the lesson the effect picker paid
// for, applied here.
//
// There, a measured zero became a user-facing promise: four effects reported "0
// frames until the screen is readable" only because they start from an
// untouched screen, and the panel turned that into "the screen stays visible
// from the start" for three that then blanked it completely.
//
// This panel measures nothing, so its lines must survive every height. A spacer
// with a share is the case that could go wrong: it gets its lines on a rail
// with room and nothing at all on one without, because empty space is the first
// thing given up when the sections cannot fit. So the line says what the entry
// asks for, and never what it got.
//
// Negative control, confirmed red: put "It keeps 40% of the rail." back in
// sectionEditorDetail. The wording table fails on the spacer case.
func TestEditorClaimsNothingItDidNotMeasure(t *testing.T) {
	// First, the thing the wording must not get wrong: at this height the rail
	// gives the spacer nothing, and at that one it gives it lines.
	const layout = "sessions,spacer:40,terminals,agents"
	if gap := gapAbove(t, spacerFrame(t, layout, 12), "terminals"); gap != 0 {
		t.Fatalf("the short rail left a %d-line gap; the test premise is wrong", gap)
	}
	if gap := gapAbove(t, spacerFrame(t, layout, 40), "terminals"); gap == 0 {
		t.Fatal("the tall rail left no gap; the test premise is wrong")
	}

	// So no line may claim the spacer has those lines.
	m := editorOS(t)
	pal := theme.UI()
	for _, tc := range []struct {
		name string
		row  railEditorRow
		want string
	}{
		{
			"a spacer's share is a request, not a promise",
			railEditorRow{Kind: railRowPlaced, Name: "spacer", Share: 40, Spacer: true},
			"Empty space. It asks for 40% of the rail.",
		},
		{
			"a bare spacer takes only what is left",
			railEditorRow{Kind: railRowPlaced, Name: "spacer", Spacer: true},
			"Empty space. It takes what is left over.",
		},
		{
			"a trailing spacer says where it is, not how big",
			railEditorRow{Kind: railRowPlaced, Name: "spacer", Spacer: true, Last: true},
			"Empty space at the end of the rail.",
		},
		{
			"a section's share is a ceiling",
			railEditorRow{Kind: railRowPlaced, Name: "files", Share: 25},
			"It takes 25% of the rail at most.",
		},
		{
			"a flexible section takes the leavings",
			railEditorRow{Kind: railRowPlaced, Name: "terminals"},
			"It takes the lines the others leave.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The panel is wide enough that nothing is cut, so the comparison is
			// against the whole sentence rather than its first half.
			got := strings.TrimSpace(stripANSIForTrace(
				m.sectionEditorDetailFor(tc.row, pal, len(tc.want)+8)))
			if got != tc.want {
				t.Errorf("the editor says %q, want %q", got, tc.want)
			}
		})
	}

	// A share of zero is the degenerate value, and it reads as a word rather
	// than as "0%", which would be a number claiming a size.
	if got := sectionShareLabel(0); got != "auto" {
		t.Errorf("a share of zero shows as %q, want a word rather than a size", got)
	}
}

// TestEditorSaysWholeSentences keeps the description line inside the panel.
//
// A sentence cut at the panel edge is the same failure in a smaller way: the
// reader is shown most of a claim and left to guess the end of it, and the half
// that is cut is the half that carries the qualification. "Empty space. It
// takes the lines nothing els…" was the first draft of one of these.
//
// Negative control, confirmed red: put that sentence back. It runs five columns
// past the body and this fails.
func TestEditorSaysWholeSentences(t *testing.T) {
	m := editorOS(t)
	width, _, _ := m.sectionEditorLayout()
	pal := theme.UI()

	// Every row the editor can select, in every state that changes its line.
	rows := []railEditorRow{
		{Kind: railRowEmpty},
		{Kind: railRowAvailable, Name: "files"},
		{Kind: railRowAvailable, Name: "spacer", Spacer: true},
		{Kind: railRowPlaced, Name: "spacer", Spacer: true},
		{Kind: railRowPlaced, Name: "spacer", Spacer: true, Last: true},
		{Kind: railRowPlaced, Name: "terminals"},
	}
	for share := 0; share <= 100; share += 5 {
		rows = append(rows,
			railEditorRow{Kind: railRowPlaced, Name: "files", Share: share},
			railEditorRow{Kind: railRowPlaced, Name: "spacer", Share: share, Spacer: true})
	}
	for _, row := range rows {
		got := stripANSIForTrace(m.sectionEditorDetailFor(row, pal, width))
		if strings.Contains(got, "…") {
			t.Errorf("the line for %+v is cut at the panel edge: %q", row, strings.TrimSpace(got))
		}
	}
}
