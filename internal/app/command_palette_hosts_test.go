package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
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
