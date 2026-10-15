package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

// TestOpenRemoteSessionOnADownHostWaitsForTheLink is #720: a click on a
// session of a host that is not up says it is reconnecting, polls fast, and
// stops with a reason when the host reaches a state waiting cannot change.
// The dial itself, and the open once the link is up, are proved end to end
// in e2e/tui.
func TestOpenRemoteSessionOnADownHostWaitsForTheLink(t *testing.T) {
	m := cachedDownHostOS(t) // "build" up, "stale" connecting
	before := m.SessionName

	m.openRemoteSession("stale", "old")
	if m.SessionName != before {
		t.Errorf("ASSERTION: a click on a host that is not up changed the session")
	}
	if m.hostOpen == nil || m.hostOpen.host != "stale" || m.hostOpen.dialing {
		t.Fatalf("ASSERTION: the click does not wait for the link: %+v", m.hostOpen)
	}
	if len(m.Notifications) == 0 || !strings.Contains(m.Notifications[len(m.Notifications)-1].Message, "Connecting to stale") {
		t.Errorf("ASSERTION: the click on a host that was never up did not say it is connecting: %+v", m.Notifications)
	}
	if note := m.hostLinkNote(); !strings.Contains(note, "Connecting to stale") {
		t.Errorf("ASSERTION: the dock says %q while the click waits", note)
	}
	if after, refresh := m.federationRefreshPlan(); !refresh || after != hostOpenPoll {
		t.Errorf("ASSERTION: the host poll runs every %v (refresh %v) while the click waits, want %v", after, refresh, hostOpenPoll)
	}
	if m.TakeSidebarCmd() == nil {
		t.Error("ASSERTION: the click did not ask the daemon to dial the host")
	}

	// The link reaches a state that waiting cannot change.
	m.FederationHosts[1].Status = string(federation.StatusNoBinary)
	m.FederationHosts[1].Reason = "The link cannot find tuios on the host."
	_ = m.advanceHostOpen()
	if m.hostOpen != nil {
		t.Error("ASSERTION: the click still waits for a host that has no tuios")
	}
	if last := m.Notifications[len(m.Notifications)-1].Message; !strings.Contains(last, "cannot find tuios") {
		t.Errorf("ASSERTION: the stop does not say why: %q", last)
	}

	// The wait runs out.
	m.FederationHosts[1].Status = string(federation.StatusUnreachable)
	m.FederationHosts[1].Reason = "The host did not answer."
	m.openRemoteSession("stale", "old")
	m.hostOpen.until = m.hostOpen.until.Add(-2 * hostOpenWait)
	_ = m.advanceHostOpen()
	if m.hostOpen != nil {
		t.Error("ASSERTION: the click still waits after its time ran out")
	}
	if last := m.Notifications[len(m.Notifications)-1].Message; !strings.Contains(last, "could not connect to stale") || !strings.Contains(last, "tuios hosts") {
		t.Errorf("ASSERTION: the time out does not say what happened and what to do: %q", last)
	}

	// A host that leaves the table ends the wait at once.
	m.openRemoteSession("stale", "old")
	m.FederationHosts = m.FederationHosts[:1]
	_ = m.advanceHostOpen()
	if m.hostOpen != nil {
		t.Error("ASSERTION: the click still waits for a host that left the table")
	}

	// A local switch ends the wait, so a late dial cannot pull the person away.
	m.FederationHosts = append(m.FederationHosts, FederationHost{Name: "stale", Status: string(federation.StatusReconnecting)})
	m.openRemoteSession("stale", "old")
	gen := m.hostOpen.gen
	m.cancelHostOpen() // what SwitchToSession, a create, detach and esc call
	if cmd := m.finishHostOpen(SessionCreatedMsg{OpenGen: gen, Host: "stale", Switched: true}); cmd != nil || m.hostOpen != nil {
		t.Error("ASSERTION: a dial for a click that was replaced still acted")
	}

	// Creating a session still needs a host that is up.
	m.createRemoteSession("stale")
	if len(m.Notifications) == 0 || !strings.Contains(m.Notifications[len(m.Notifications)-1].Message, "stale is unavailable") {
		t.Errorf("ASSERTION: creating a session on a down host did not say the host is unavailable: %+v", m.Notifications)
	}
}

// TestRailShowsThisMachineAsAHostWhileAttachedElsewhere pins what the tree
// holds when the client is on another machine: the attached machine's own
// group is not drawn a second time, and this machine's sessions appear as a
// host group named local so they can be returned to. This machine is always
// a target, which is the way back.
func TestRailShowsThisMachineAsAHostWhileAttachedElsewhere(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")
	m.applyFederationSnapshot(FederationHostsMsg{
		Configured: 1,
		Snapshot: FederationSnapshot{Hosts: []FederationHost{
			{Name: federation.LocalHostName, Status: string(federation.StatusUp),
				Sessions: []FederationSession{{Name: "here", WindowCount: 2}}},
			{Name: "build", Status: string(federation.StatusUp),
				Sessions: []FederationSession{{Name: "api", WindowCount: 3}}},
		}},
	})

	// At home: the local entry is not a host group and build is.
	names := func() string {
		var out []string
		for _, n := range m.hostGroupNodes() {
			out = append(out, n.Host+"/"+n.Title)
		}
		return strings.Join(out, " ")
	}
	if got := names(); got != "build/build build/api" {
		t.Fatalf("ASSERTION: at home the host groups are %q, want only build", got)
	}

	// Away on build: local becomes a group, build is the main group.
	m.AttachedHost = "build"
	if got := names(); got != "local/local local/here" {
		t.Errorf("ASSERTION: away on build the host groups are %q, want only local", got)
	}
	if !m.hostIsUp(federation.LocalHostName) {
		t.Errorf("ASSERTION: this machine is not a target, so there is no way back")
	}
	if !m.hostIsUp("build") {
		t.Errorf("ASSERTION: the machine the client is attached to is not up")
	}
}
