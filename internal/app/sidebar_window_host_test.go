package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// A session can hold panes whose processes are on different machines. The rail
// lists those panes, so it has to be able to say which is which: a command
// typed into a pane does a different thing depending on the machine that
// answers it, and the row is where someone looks before typing.

func hostRowEntry(host, tag string) sidebarTerminalEntry {
	return sidebarTerminalEntry{
		SessionID:   "work",
		WindowID:    "w1",
		Title:       "shell",
		Host:        host,
		Tag:         tag,
		WindowIndex: -1,
	}
}

// TestAPaneOnAnotherMachineNamesItInTheRail.
//
// Negative control: dropping the Host arm from the row's right-hand slot
// leaves the row with nothing about the machine and this fails.
func TestAPaneOnAnotherMachineNamesItInTheRail(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")
	row := m.sidebarTerminalRow(hostRowEntry("build", ""), 30, theme.UI(), sidebarRowState{}, false)
	if !strings.Contains(row, "build") {
		t.Errorf("a pane whose process is on build does not say so: %q", row)
	}
}

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

// TestTheMachineOutranksTheWorkspaceWhenOnlyOneFits.
//
// Both are orientation, but a workspace is where a pane is filed and a machine
// decides what a command typed into it does. On a rail too narrow for both,
// that is the one to keep.
//
// Negative control: checking Tag before Host drops the machine here and keeps
// the workspace.
func TestTheMachineOutranksTheWorkspaceWhenOnlyOneFits(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")
	row := m.sidebarTerminalRow(hostRowEntry("build", "w4"), 16, theme.UI(), sidebarRowState{}, false)
	if !strings.Contains(row, "build") {
		t.Errorf("the machine gave way to the workspace on a narrow rail: %q", row)
	}
}
