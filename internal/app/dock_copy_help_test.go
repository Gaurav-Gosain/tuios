package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
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
