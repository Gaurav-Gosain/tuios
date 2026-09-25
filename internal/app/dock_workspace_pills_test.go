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
	prevTabs, prevASCII := config.Global.DockWorkspaceTabs, config.Global.UseASCIIOnly
	config.Global.DockWorkspaceTabs, config.Global.UseASCIIOnly = true, true
	t.Cleanup(func() { config.Global.DockWorkspaceTabs, config.Global.UseASCIIOnly = prevTabs, prevASCII })

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

// dockBarRow renders the dock and returns its bar row as plain text, asserting
// the row measures one cell per rune so the caller may index it by column.
func dockBarRow(t *testing.T, m *OS) string {
	t.Helper()
	dock, _ := m.renderDockString()
	rows := strings.Split(stripANSIForTrace(dock), "\n")
	row := rows[len(rows)-1]
	if m.Settings.DockbarPosition == "top" {
		row = rows[0]
	}
	if lipgloss.Width(row) != len([]rune(row)) {
		t.Fatalf("the bar row is %d cells over %d runes, so a column is not a rune here",
			lipgloss.Width(row), len([]rune(row)))
	}
	return row
}

// pillCapsOS is pillOS with the Nerd Font glyph set on, which is the state the
// rounded caps are drawn in. Every glyph the bar uses is still one cell wide, so
// dockBarRow's rune-per-column assertion holds and a recorded rectangle can
// still be compared against the cells that were painted in it.
func pillCapsOS(t *testing.T, w int, names map[int]string, workspaces ...int) *OS {
	t.Helper()
	prev := config.Global.UseASCIIOnly
	t.Cleanup(func() { config.Global.UseASCIIOnly = prev })
	m := pillOS(t, w, names, workspaces...)
	m.Settings.UseASCIIOnly = false
	return m
}

