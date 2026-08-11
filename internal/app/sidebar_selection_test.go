package app

import (
	"fmt"
	"image/color"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// bgParams and fgParams are the truecolor parameters lipgloss emits for a
// color, matched without the escape so a row that sets foreground and
// background in one combined sequence still counts.
func bgParams(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("48;2;%d;%d;%d", r>>8, g>>8, b>>8)
}

func fgParams(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("38;2;%d;%d;%d", r>>8, g>>8, b>>8)
}

// treeRow returns the styled session-tree row containing want. A pane running
// an agent is also listed in the agents section above, so the last match is the
// tree row.
func treeRow(t *testing.T, m *OS, want string) string {
	t.Helper()
	lines, _ := m.sidebarPanelLines()
	found := ""
	for _, l := range lines {
		if strings.Contains(stripANSIForTrace(l), want) {
			found = l
		}
	}
	if found == "" {
		t.Fatalf("no row contains %q", want)
	}
	return found
}

// gutterCell is the first content column of a styled rail row, which is the
// gutter mark, stripped of styling.
func gutterCell(row string) string {
	plain := []rune(stripANSIForTrace(row))
	if len(plain) == 0 {
		return ""
	}
	return string(plain[0])
}

// TestRailMarksCurrentWithAGutterMark is the rail's emphasis budget: the
// attached session and the focused pane are the same "this is the current one"
// mark, one accent cell in column 0, and nothing painted behind the row. Three
// stacked full-width bands read as zebra striping rather than emphasis, and the
// loudest of them marked the thing the user already knows.
func TestRailMarksCurrentWithAGutterMark(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")
	pal := theme.UI()

	focused := treeRow(t, m, "editor")
	if got := gutterCell(focused); got != "▎" {
		t.Errorf("the focused pane row has no gutter mark, column 0 is %q: %q", got, focused)
	}
	if !strings.Contains(focused, fgParams(pal.Accent)) {
		t.Errorf("the focused pane's gutter mark is not accent: %q", focused)
	}

	lines, _ := m.sidebarPanelLines()
	// Nothing on a resting rail paints a row: no Surface band, no severity tint,
	// no saturated focus fill.
	loud := bgParams(color.RGBA{R: 0x48, G: 0x65, B: 0xf2, A: 0xff})
	for _, l := range lines {
		if strings.Contains(l, loud) {
			t.Fatalf("the saturated focus fill is still on the rail: %q", l)
		}
		if strings.Contains(l, bgParams(pal.Surface)) {
			t.Fatalf("a standing Surface band is still on the rail: %q", l)
		}
	}
	for _, cap := range []string{config.DockPillLeftChar, config.DockPillRightChar} {
		for _, l := range lines {
			if strings.Contains(l, cap) {
				t.Fatalf("a pill cap %q is still on the rail: %q", cap, l)
			}
		}
	}
}

// The current session rolls up a pane that wants a human. Identity and
// attention no longer fight for the row background: the gutter says which
// session you are attached to, while the state keeps the coloured glyph and the
// rail's one bold.
func TestRailCurrentSessionKeepsIdentityUnderAttention(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")
	pal := theme.UI()

	session := treeRow(t, m, "local")
	if got := gutterCell(session); got != "▎" {
		t.Errorf("the attached session row has no gutter mark, column 0 is %q: %q", got, session)
	}
	if !strings.Contains(session, fgParams(pal.Accent)) {
		t.Errorf("the attached session's gutter mark is not accent: %q", session)
	}
	if !strings.Contains(session, fgParams(agentGlyphColor("needs_input", pal))) {
		t.Errorf("the rolled-up attention glyph lost its severity colour: %q", session)
	}
	if !strings.Contains(session, "\x1b[1m") && !strings.Contains(session, ";1m") &&
		!strings.Contains(session, "[1;") {
		t.Errorf("the attention row is not bold: %q", session)
	}
}

// TestRailAttentionMarksAnUnfocusedPane: a pane asking for a human that you are
// not sitting on wears the severity gutter mark, its coloured glyph, and bold.
// A pane you are already on marks identity instead, because being there is the
// answer to what the alarm was asking.
func TestRailAttentionMarksAnUnfocusedPane(t *testing.T) {
	m, _ := attentionOS(t, 120, 40)
	m.FocusedWindow = 0
	pal := theme.UI()

	row := treeRow(t, m, "server")
	if got := gutterCell(row); got != "▎" {
		t.Errorf("the attention row has no gutter mark, column 0 is %q: %q", got, row)
	}
	if !strings.Contains(row, fgParams(pal.Warning)) {
		t.Errorf("the attention row's gutter mark is not severity-coloured: %q", row)
	}
	if strings.Contains(row, bgParams(pal.Surface)) {
		t.Errorf("the attention row is painted rather than marked: %q", row)
	}
	if glyph := agentStateIndicator("needs_input"); !strings.Contains(row, glyph) {
		t.Errorf("the attention row dropped its state glyph: %q", row)
	}

	// Focused, the same pane keeps the alarm in its glyph and its bold while the
	// gutter switches to identity.
	m.FocusedWindow = 4 // "server", needs_input
	m.sidebarCache.invalidate()
	row = treeRow(t, m, "server")
	if !strings.Contains(row, fgParams(pal.Accent)) {
		t.Errorf("the focused attention row lost its identity mark: %q", row)
	}
	if !strings.Contains(row, fgParams(agentGlyphColor("needs_input", pal))) {
		t.Errorf("the focused attention row lost its state colour: %q", row)
	}
}
