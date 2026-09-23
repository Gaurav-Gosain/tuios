package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The sessions section answers "who needs me". It answered it for this machine
// only: a session row on another machine wore the resting bullet whatever its
// panes were doing, and a machine's header said how many sessions it held and
// never how many wanted a person. An agent waiting for someone waits just as
// long on the build box.

// hostAgentOS is a client with one machine besides this one, whose sessions
// are in the states the argument is about.
func hostAgentOS(t *testing.T, status federation.Status, states ...string) *OS {
	t.Helper()
	m := sidebarTestOS(t, 120, 40, "left")
	m.SessionName = "home"

	sessions := make([]FederationSession, 0, len(states))
	for i, st := range states {
		sessions = append(sessions, FederationSession{
			Name:        string(rune('a'+i)) + "-session",
			WindowCount: 1,
			AgentState:  st,
		})
	}
	m.applyFederationSnapshot(FederationHostsMsg{
		Configured: 1,
		Snapshot: FederationSnapshot{Hosts: []FederationHost{
			{
				Name:     federation.LocalHostName,
				Status:   string(federation.StatusUp),
				Sessions: []FederationSession{{Name: "home", WindowCount: 1}},
			},
			{Name: "build", Status: string(status), Sessions: sessions},
		}},
	})
	return m
}

// remoteNode is the tree node for one of build's session rows.
func remoteNode(t *testing.T, m *OS, name string) sessiontree.Node {
	t.Helper()
	for _, n := range m.hostGroupNodes() {
		if n.Kind == sessiontree.KindSession && strings.Contains(n.ID, name) {
			return n
		}
	}
	t.Fatalf("no row for %q among the machine's sessions", name)
	return sessiontree.Node{}
}

// TestASessionOnAnotherMachineWearsItsAgentGlyph.
//
// Negative control: dropping AgentState from the node in hostGroupNodes leaves
// the row with no state and this fails.
func TestASessionOnAnotherMachineWearsItsAgentGlyph(t *testing.T) {
	m := hostAgentOS(t, federation.StatusUp, "needs_input")

	node := remoteNode(t, m, "a-session")
	if node.AgentState != "needs_input" {
		t.Fatalf("a session on another machine reports state %q", node.AgentState)
	}
	if agentStateIndicator(node.AgentState) == "" {
		t.Fatal("ASSERTION: that state has no glyph, so drawing it proves nothing")
	}
}

// TestAMachineThatDidNotAnswerDrawsNoGlyph. Its rows are the last listing that
// arrived, so a glyph on them reports what a pane was doing at a moment that
// has passed, on a machine nobody can reach to check.
//
// Negative control: removing the hostIsUp check from the row's glyph fails
// here with the cached alarm still drawn.
func TestAMachineThatDidNotAnswerDrawsNoGlyph(t *testing.T) {
	m := hostAgentOS(t, federation.StatusUnreachable, "needs_input")

	node := remoteNode(t, m, "a-session")
	pal := theme.UI()
	row := m.sidebarRemoteSessionRow(node, 40, sidebarVariantFull, pal, sidebarRowState{}, true)

	if strings.Contains(row, agentStateIndicator("needs_input")) {
		t.Errorf("a machine that is not answering still draws an alarm: %q", row)
	}
}

// TestAMachineHeaderCountsTheSessionsThatWantAPerson, so a folded group still
// says there is something under it.
//
// Negative control: dropping the blocked case from sidebarHostRow's switch
// fails here, drawing the session count instead.
func TestAMachineHeaderCountsTheSessionsThatWantAPerson(t *testing.T) {
	m := hostAgentOS(t, federation.StatusUp, "needs_input", "working", "errored")

	blocked, worst := m.hostAttention("build")
	if blocked != 2 {
		t.Errorf("%d of build's sessions want a person, want 2", blocked)
	}
	// errored outranks needs_input, so it is the one the figure is marked in.
	if worst != "errored" {
		t.Errorf("the header's worst state is %q, want errored", worst)
	}

	for _, collapsed := range []bool{false, true} {
		node := m.hostGroupNodes()[0]
		pal := theme.UI()
		row := m.sidebarHostRow(node, 40, pal, "", sidebarRowState{}, collapsed)
		want := "2" + agentStateIndicator("errored")
		if !strings.Contains(row, want) {
			t.Errorf("collapsed=%v: the machine header does not carry %q: %q", collapsed, want, row)
		}
	}
}

// TestAnOfflineMachineHeaderSaysMailWaitsForIt: mail queued for a machine
// whose link is down is counted on its header, in words.
func TestAnOfflineMachineHeaderSaysMailWaitsForIt(t *testing.T) {
	m := hostAgentOS(t, federation.StatusUnreachable, "idle")
	for i := range m.FederationHosts {
		if m.FederationHosts[i].Name == "build" {
			m.FederationHosts[i].Queued = 3
		}
	}
	node := m.hostGroupNodes()[0]
	row := m.sidebarHostRow(node, 40, theme.UI(), "", sidebarRowState{}, false)
	if !strings.Contains(row, "3 queued") {
		t.Errorf("the header of a machine with mail waiting does not say so: %q", row)
	}
}

// TestAQuietMachineHeaderKeepsItsOrdinaryRight. The figure means something
// only because it is absent the rest of the time, and taking the add control
// away from every machine would be a poor trade.
func TestAQuietMachineHeaderKeepsItsOrdinaryRight(t *testing.T) {
	m := hostAgentOS(t, federation.StatusUp, "working", "idle")

	if blocked, _ := m.hostAttention("build"); blocked != 0 {
		t.Fatalf("%d sessions counted as wanting a person, want 0", blocked)
	}
	node := m.hostGroupNodes()[0]
	row := m.sidebarHostRow(node, 40, theme.UI(), "+", sidebarRowState{}, false)
	if !strings.Contains(row, "+") {
		t.Errorf("a quiet machine lost its add control: %q", row)
	}
}

// TestADoneAgentOnAnotherMachineIsUnread. Whether a finished pane has been
// looked at is a fact about a viewer, and this client cannot look at a pane on
// another machine, so it rolls up as unseen rather than as already handled.
func TestADoneAgentOnAnotherMachineIsUnread(t *testing.T) {
	m := hostAgentOS(t, federation.StatusUp, "done")

	node := remoteNode(t, m, "a-session")
	if node.DoneSeen {
		t.Error("a pane on another machine is marked as one this client has looked at")
	}
	if sessiontree.AgentRank(node.AgentState, node.DoneSeen) != sessiontree.AgentRank("done", false) {
		t.Error("a finished agent on another machine does not rank as unread")
	}
}
