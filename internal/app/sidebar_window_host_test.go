package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// TestAPaneWhoseLinkIsLostSaysSoInTheRail: while the link to its machine is
// lost, the row says reconnecting beside the machine, in words.
func TestAPaneWhoseLinkIsLostSaysSoInTheRail(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")
	nodes := []sessiontree.Node{{
		Kind: sessiontree.KindSession, ID: "work",
		Children: []sessiontree.Node{{Kind: sessiontree.KindWindow, ID: "w1", Title: "shell", Host: "build", HostLink: "reconnecting"}},
	}}
	entries := m.sidebarTerminals(nodes, "work")
	if len(entries) != 1 {
		t.Fatalf("got %d rows", len(entries))
	}
	row := m.sidebarTerminalRow(entries[0], 40, theme.UI(), sidebarRowState{}, false)
	if !strings.Contains(row, "build reconnecting") {
		t.Errorf("a pane whose link is lost does not say so: %q", row)
	}
}
