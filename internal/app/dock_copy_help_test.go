package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// copyModeDockOS is a session in copy mode at a given width, which is what puts
// the help line in the dock's right-hand block.
func copyModeDockOS(t *testing.T, width int, state terminal.CopyModeState) *OS {
	t.Helper()
	win := newTestWindow(t, "copy-help", 40, 12)
	win.Workspace = 1
	m := newTestOS(win)
	m.Width, m.Height = width, 30
	m.CurrentWorkspace = 1
	win.CopyMode = &terminal.CopyMode{Active: true, State: state}
	return m
}

// TestCopyModeHelpUsesTheChipFormat is the last of the four hint dialects. The
// dock said what a key does as "hjkl:move w/b/e:word", its own format, while
// every panel footer said it as a key and a label.
func TestCopyModeHelpUsesTheChipFormat(t *testing.T) {
	for _, width := range []int{80, 120, 200} {
		m := copyModeDockOS(t, width, terminal.CopyModeNormal)
		dock, _ := m.renderDockString()
		plain := ansi.Strip(dock)

		if strings.Contains(plain, ":move") || strings.Contains(plain, ":search") {
			t.Errorf("w=%d: the dock still names keys in its own format: %q", width, plain)
		}
		if !strings.Contains(plain, "hjkl move") {
			t.Errorf("w=%d: the help line lost its keys: %q", width, plain)
		}

		// The key carries the footer's ink, which is what makes it read as a key.
		fg, _ := inkBefore(t, dock, "hjkl")
		if want := theme.UI().AccentBright; !sameColor(fg, want) {
			t.Errorf("w=%d: the key is drawn %v, the footers draw keys %v", width, fg, want)
		}
	}
}

// TestCopyModeHelpDropsDetailBeforeItOverflows checks the tiers still work: a
// narrow dock gets fewer pairs rather than a line running off the end.
func TestCopyModeHelpDropsDetailBeforeItOverflows(t *testing.T) {
	wide, _ := copyModeDockOS(t, 200, terminal.CopyModeNormal).renderDockString()
	narrow, _ := copyModeDockOS(t, 70, terminal.CopyModeNormal).renderDockString()

	wideRow := lastRow(wide)
	narrowRow := lastRow(narrow)
	if !strings.Contains(wideRow, "w/b/e word") {
		t.Errorf("a wide dock did not draw the full tier: %q", wideRow)
	}
	if strings.Contains(narrowRow, "w/b/e word") {
		t.Errorf("a 70-column dock drew the full tier: %q", narrowRow)
	}
	for _, row := range []string{wideRow, narrowRow} {
		if strings.Contains(row, "hjkl") && !strings.Contains(row, "quit") {
			t.Errorf("a tier dropped the way out of copy mode: %q", row)
		}
	}
}

// lastRow is the dock's content row, stripped.
func lastRow(dock string) string {
	lines := strings.Split(ansi.Strip(dock), "\n")
	return lines[len(lines)-1]
}

// sameColor compares two colours by their RGBA words.
func sameColor(a, b interface{ RGBA() (r, g, bl, al uint32) }) bool {
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	return ar == br && ag == bg && ab == bb
}
