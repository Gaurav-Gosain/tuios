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

// TestRailMarksCurrentWithOneBand is the rail's emphasis budget: the attached
// session and the focused pane are the same "this is the current one" mark, a
// full-width Surface band and nothing else. No caps, no inverse pill: caps
// spent two cells and a second colour on every emphasized row, and an inverse
// fill is the loudest mark in TUI grammar to spend on where the user already
// is. The saturated focus fill is gone from the rail entirely.
func TestRailMarksCurrentWithOneBand(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")
	pal := theme.UI()
	surface := bgParams(pal.Surface)

	// "logs" is the calm session's pane: an attention tint outranks the current
	// band, so a row carrying one is not the case under test here.
	focused := treeRow(t, m, "editor")
	if !strings.Contains(focused, surface) {
		t.Errorf("the focused pane row is not on the Surface band: %q", focused)
	}

	lines, _ := m.sidebarPanelLines()
	loud := bgParams(color.RGBA{R: 0x48, G: 0x65, B: 0xf2, A: 0xff})
	for _, l := range lines {
		if strings.Contains(l, loud) {
			t.Fatalf("the saturated focus fill is still on the rail: %q", l)
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

// The attached session's band is one luminance step and yields to both louder
// bands: the pointer's, and a state that wants a human.
func TestRailCurrentSessionBandYieldsToAttention(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")
	pal := theme.UI()

	// The fixture's session rolls up a needs_input pane, so its row carries the
	// severity tint rather than the current-session step.
	session := treeRow(t, m, "local")
	if tint := sidebarAttentionTint("needs_input", pal); tint == nil {
		t.Fatal("the palette defines no attention tint")
	} else if !strings.Contains(session, bgParams(tint)) {
		t.Errorf("the attention tint lost to the current-session band: %q", session)
	}
	if strings.Contains(session, bgParams(pal.Surface)) {
		t.Errorf("the current-session band is still painted under the tint: %q", session)
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
