package app

import (
	"fmt"
	"image/color"
	"strings"
	"testing"

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

// TestRailMarksCurrentWithOneChip is the rail's emphasis budget: the attached
// session and the focused pane are the same "this is the current one" chip, not
// two competing treatments, and the saturated fill that used to shout from the
// focused row is gone from the rail entirely.
func TestRailMarksCurrentWithOneChip(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")
	pal := theme.UI()
	surface := bgParams(pal.Surface)

	session := treeRow(t, m, "local")
	focused := treeRow(t, m, "editor")
	if !strings.Contains(session, surface) {
		t.Errorf("the current session row is not the Surface chip: %q", session)
	}
	if !strings.Contains(focused, surface) {
		t.Errorf("the focused pane row is not the same chip: %q", focused)
	}

	lines, _ := m.sidebarPanelLines()
	loud := bgParams(color.RGBA{R: 0x48, G: 0x65, B: 0xf2, A: 0xff})
	for _, l := range lines {
		if strings.Contains(l, loud) {
			t.Fatalf("the saturated focus fill is still on the rail: %q", l)
		}
	}
}

// TestRailAttentionOutranksFocus is the ranking the old pill had backwards: a
// pane asking for a human has to stay the loudest row even when it is the pane
// you are sitting on.
func TestRailAttentionOutranksFocus(t *testing.T) {
	m, _ := attentionOS(t, 120, 40)
	m.FocusedWindow = 4 // "server", needs_input
	pal := theme.UI()

	row := treeRow(t, m, "server")
	tint := sidebarAttentionTint("needs_input", pal)
	if tint == nil {
		t.Fatal("needs_input has no attention tint")
	}
	if !strings.Contains(row, bgParams(tint)) {
		t.Errorf("the focused pane lost its attention tint: %q", row)
	}
	// The state still reads as itself inside the chip, which the saturated fill
	// used to swallow.
	if glyph := agentStateIndicator("needs_input"); !strings.Contains(row, glyph) {
		t.Errorf("the focused row dropped its state glyph: %q", row)
	}
	if !strings.Contains(row, fgParams(agentGlyphColor("needs_input", pal))) {
		t.Errorf("the state glyph is not state-colored inside the chip: %q", row)
	}
}
