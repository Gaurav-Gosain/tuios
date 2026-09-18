package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// The session tree carries the other machines' rows alongside this machine's,
// and the palette reads that tree. Neither kind of remote row is a local
// session, and treating them as one is what produced
// `switch to "\x00host/local" failed`: a machine's heading has a rail identity
// for an id, not a session name, and the palette handed it straight to the
// daemon.

// paletteHostOS is a client with two machines in its listing and one local
// session, which is the shape the report came from.
func paletteHostOS(t *testing.T) *OS {
	t.Helper()
	m := sidebarTestOS(t, 120, 40, "left")
	m.SessionName = "home"
	m.applyFederationSnapshot(FederationHostsMsg{
		Configured: 1,
		Snapshot: FederationSnapshot{Hosts: []FederationHost{
			{
				Name:     federation.LocalHostName,
				Status:   string(federation.StatusUp),
				Sessions: []FederationSession{{Name: "home", WindowCount: 1}},
			},
			{
				Name:     "build",
				Status:   string(federation.StatusUp),
				Sessions: []FederationSession{{Name: "api", WindowCount: 2}},
			},
		}},
	})
	return m
}

// TestThePaletteNeverOffersAMachineAsASession. A machine's heading is not
// somewhere you can go, so it must not be in a list of places to go.
//
// Negative control: dropping the KindHost skip puts an item named after the
// host node's id in the list and this fails.
func TestThePaletteNeverOffersAMachineAsASession(t *testing.T) {
	m := paletteHostOS(t)

	for _, item := range getSessionPaletteItems(m) {
		if strings.Contains(item.Name, "\x00host/") {
			t.Errorf("the palette offers a machine's rail identity as a session: %q", item.Name)
		}
	}
}

// TestAHostNodeIsNotASwitchTarget guards the same thing at the tree level, so a
// future surface that reads the tree has the invariant written down: a node id
// is only a session name for a node on this machine.
func TestAHostNodeIsNotASwitchTarget(t *testing.T) {
	m := paletteHostOS(t)
	tree := m.BuildSessionTree()

	sawHost := false
	for _, s := range tree.Sessions {
		if s.Kind == sessiontree.KindHost {
			sawHost = true
			if !strings.HasPrefix(s.ID, "\x00host/") {
				t.Errorf("a machine heading carries id %q, which no longer marks it as one", s.ID)
			}
		}
	}
	if !sawHost {
		t.Fatal("ASSERTION: the fixture's tree holds no machine heading, so this proves nothing")
	}
}

// TestASessionOnAnotherMachineIsOfferedAndSaysSo. It is reachable, unlike a
// heading, but it is a different connection and the row says so rather than
// looking like one of this machine's.
func TestASessionOnAnotherMachineIsOfferedAndSaysSo(t *testing.T) {
	m := paletteHostOS(t)

	found := false
	for _, item := range getSessionPaletteItems(m) {
		if strings.Contains(item.Name, "api") && strings.Contains(item.Name, "build") {
			found = true
			if item.Shortcut != "another machine" {
				t.Errorf("a session on another machine is offered as %q with shortcut %q", item.Name, item.Shortcut)
			}
		}
	}
	if !found {
		t.Errorf("a session on another machine is not offered at all:\n%s", paletteNames(getSessionPaletteItems(m)))
	}
}
