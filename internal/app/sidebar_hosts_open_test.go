package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

// TestOpenRemoteSessionOnADownHostSaysUnavailable is #170's toast: an action
// aimed at a host that is not up says so, rather than dialing a machine that
// cannot answer. The positive half is the up-host row being a target, proved
// in sidebar_hosts_test.go, and the attach itself is proved end to end in
// e2e/tui.
func TestOpenRemoteSessionOnADownHostSaysUnavailable(t *testing.T) {
	m := cachedDownHostOS(t) // "build" up, "stale" connecting
	before := m.SessionName

	m.openRemoteSession("stale", "old")
	if m.SessionName != before {
		t.Errorf("ASSERTION: opening a session on a host that is not up changed the session")
	}
	if len(m.Notifications) == 0 || !strings.Contains(m.Notifications[len(m.Notifications)-1].Message, "stale is unavailable") {
		t.Errorf("ASSERTION: opening a session on a down host did not say the host is unavailable: %+v", m.Notifications)
	}

	m.createRemoteSession("stale")
	if len(m.Notifications) == 0 || !strings.Contains(m.Notifications[len(m.Notifications)-1].Message, "stale is unavailable") {
		t.Errorf("ASSERTION: creating a session on a down host did not say the host is unavailable: %+v", m.Notifications)
	}

	// Sanity: the fixture really did have an up host, so the guard is what
	// stopped the down one, not a fixture with no up host at all.
	if !m.hostIsUp("build") {
		t.Fatal("the fixture's up host is not up, so this proves nothing")
	}
	_ = federation.StatusUp
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
