package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// TestSidebarFocusResolvesByID pins the fix for a wrong-pane hazard: rail hit
// rects carry the index a row was drawn with, and a pane closing before the
// click shifts every later index. Focusing by that stale index selected a
// different pane than the row named, and the context menu built on top of it
// then offered to close that one.
func TestSidebarFocusResolvesByID(t *testing.T) {
	a := &terminal.Window{ID: "a"}
	b := &terminal.Window{ID: "b"}
	c := &terminal.Window{ID: "c"}
	m := &OS{Settings: config.Global, Windows: []*terminal.Window{a, b, c}, FocusedWindow: 0}

	// The rail drew B at index 1. A exits before the click lands.
	hit := sidebarRowHit{Kind: sidebarRowWindow, WindowID: "b", WindowIndex: 1}
	m.Windows = []*terminal.Window{b, c}

	m.sidebarFocusWindow(hit)

	if got := m.Windows[m.FocusedWindow].ID; got != "b" {
		t.Fatalf("focused %q, want the pane the row named (b); the stale index would give %q",
			got, m.Windows[1].ID)
	}
}
