package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

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
