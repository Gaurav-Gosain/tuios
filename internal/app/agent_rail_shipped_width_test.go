package app

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/charmbracelet/x/ansi"
)

// The rail rows the agent work added (a machine's header with the sessions
// that want a person and the mail queued for it, a session on another machine
// with its agent glyph, a pane whose link is lost) were measured on the 28
// column rail, and the other tests here still render them 40 wide. v0.8.0
// ships the rail on at 24 columns. These render each one on the shipped rail,
// at the width the rail actually takes, and check it still names what it is
// about.

// shippedRailOS is hostAgentOS on the looks v0.8.0 ships, and the content
// width of its rail's rows.
func shippedRailOS(t *testing.T, status federation.Status, states ...string) (*OS, int) {
	t.Helper()
	m := hostAgentOS(t, status, states...)
	m.Settings = config.DefaultSettings()
	w := m.GetSidebarWidth()
	if w != config.SidebarDefaultWidth || m.Settings.SidebarPosition != "right" {
		t.Fatalf("not the shipped rail: width %d, position %q", w, m.Settings.SidebarPosition)
	}
	return m, w - 1 // the row's content columns beside the edge rule
}

// TestAnOfflineMachineHeaderNamesItOnTheShippedRail. The header of a machine
// whose link is down said "3 queued, seen 3m ago" and cut the machine's name
// to its first letter to make room, on the 28 column rail too. On 24 it also
// lost the end of the label. Now the count goes on alone when both do not fit.
//
// Negative control: without the narrow fallback in sidebarHostRow the row
// reads "b 3 queued, seen 3m" and this fails.
func TestAnOfflineMachineHeaderNamesItOnTheShippedRail(t *testing.T) {
	m, cw := shippedRailOS(t, federation.StatusUnreachable, "idle")
	for i := range m.FederationHosts {
		if m.FederationHosts[i].Name == "build" {
			m.FederationHosts[i].Queued = 3
			m.FederationHosts[i].LastOK = time.Now().Add(-3 * time.Minute).Unix()
		}
	}
	node := m.hostGroupNodes()[0]

	row := ansi.Strip(m.sidebarHostRow(node, cw, theme.UI(), "", sidebarRowState{}, false))
	for _, want := range []string{"build", "3 queued"} {
		if !strings.Contains(row, want) {
			t.Errorf("on the shipped rail the header does not say %q: %q", want, row)
		}
	}
	if w := ansi.StringWidth(row); w > cw {
		t.Errorf("the header is %d cells, wider than the rail's %d", w, cw)
	}

	// With room for it the whole label stays.
	wide := ansi.Strip(m.sidebarHostRow(node, 40, theme.UI(), "", sidebarRowState{}, false))
	if !strings.Contains(wide, "build") || !strings.Contains(wide, "3 queued, seen 3m ago") {
		t.Errorf("40 wide the header lost part of its label: %q", wide)
	}
}

// TestABlockedMachineHeaderFitsTheShippedRail: the count of sessions that want
// a person and the machine's name both survive 24 columns.
func TestABlockedMachineHeaderFitsTheShippedRail(t *testing.T) {
	m, cw := shippedRailOS(t, federation.StatusUp, "needs_input", "errored")
	node := m.hostGroupNodes()[0]
	for _, collapsed := range []bool{false, true} {
		row := ansi.Strip(m.sidebarHostRow(node, cw, theme.UI(), "+", sidebarRowState{}, collapsed))
		want := "2" + agentStateIndicator("errored")
		if !strings.Contains(row, "build") || !strings.Contains(row, want) {
			t.Errorf("collapsed=%v: the header does not carry build and %q: %q", collapsed, want, row)
		}
	}
	sess := remoteNode(t, m, "a-session")
	row := ansi.Strip(m.sidebarRemoteSessionRow(sess, cw, sidebarVariantFull, theme.UI(), sidebarRowState{}, true))
	if !strings.Contains(row, "a-session") || !strings.Contains(row, agentStateIndicator("needs_input")) {
		t.Errorf("a blocked session on another machine lost its name or glyph on the shipped rail: %q", row)
	}
}

// TestAPaneWhoseLinkIsLostSaysSoOnTheShippedRail. The row said "build
// reconnecting" beside the pane's name, which on 24 columns left one letter of
// the name run into the label. When both do not fit the link's state goes on
// alone, since the pane's frame still names the machine.
//
// Negative control: joining the machine and the link's state into Host again,
// as the row did, fails here with the name cut to "s".
func TestAPaneWhoseLinkIsLostSaysSoOnTheShippedRail(t *testing.T) {
	m, cw := shippedRailOS(t, federation.StatusUp, "idle")
	nodes := []sessiontree.Node{{
		Kind: sessiontree.KindSession, ID: "work",
		Children: []sessiontree.Node{{Kind: sessiontree.KindWindow, ID: "w1", Title: "shell", Host: "build", HostLink: "reconnecting"}},
	}}
	entries := m.sidebarTerminals(nodes, "work")
	if len(entries) != 1 {
		t.Fatalf("got %d rows", len(entries))
	}
	row := ansi.Strip(m.sidebarTerminalRow(entries[0], cw, theme.UI(), sidebarRowState{}, false))
	for _, want := range []string{"shell", "reconnecting"} {
		if !strings.Contains(row, want) {
			t.Errorf("on the shipped rail the row does not say %q: %q", want, row)
		}
	}
	wide := ansi.Strip(m.sidebarTerminalRow(entries[0], 40, theme.UI(), sidebarRowState{}, false))
	if !strings.Contains(wide, "build reconnecting") {
		t.Errorf("40 wide the row does not name the machine beside the link: %q", wide)
	}
}
