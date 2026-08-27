package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// TestSessionCycleFollowsTheRailAndWraps pins next/prev to the order the rail
// draws, including the user's drag order, and checks both ends wrap. Cycling off
// the daemon's own listing instead would walk the sessions in an order the user
// cannot see.
func TestSessionCycleFollowsTheRailAndWraps(t *testing.T) {
	client := session.NewTUIClient()
	client.UpdateSessionCache([]session.SessionInfo{{Name: "alpha"}, {Name: "bravo"}, {Name: "charlie"}})
	m := &OS{
		Settings:     config.Global,
		Windows:      []*terminal.Window{{ID: "w1"}},
		Width:        120,
		DaemonClient: client,
	}

	for _, tc := range []struct {
		current    string
		next, prev string
	}{
		{"alpha", "bravo", "charlie"},
		{"bravo", "charlie", "alpha"},
		{"charlie", "alpha", "bravo"},
	} {
		m.SessionName = tc.current
		if got := m.railNeighbourSession(1); got != tc.next {
			t.Errorf("next from %s = %q, want %q", tc.current, got, tc.next)
		}
		if got := m.railNeighbourSession(-1); got != tc.prev {
			t.Errorf("prev from %s = %q, want %q", tc.current, got, tc.prev)
		}
	}

	// A rail the user has reordered is the order next/prev must walk.
	m.SidebarOrder = []string{"charlie", "alpha", "bravo"}
	m.SessionName = "charlie"
	if got := m.railNeighbourSession(1); got != "alpha" {
		t.Errorf("next after a reorder = %q, want alpha", got)
	}
	if got := m.railNeighbourSession(-1); got != "bravo" {
		t.Errorf("prev after a reorder = %q, want bravo", got)
	}
}

// TestSessionCycleAloneHasNowhereToGo covers standalone (no daemon) and a daemon
// with a single session: both must report rather than switch.
func TestSessionCycleAloneHasNowhereToGo(t *testing.T) {
	standalone := &OS{Settings: config.Global, Windows: []*terminal.Window{{ID: "w1"}}, Width: 120}
	if got := standalone.railNeighbourSession(1); got != "" {
		t.Errorf("standalone offered %q to switch to", got)
	}

	client := session.NewTUIClient()
	client.UpdateSessionCache([]session.SessionInfo{{Name: "only"}})
	solo := &OS{Settings: config.Global, Windows: []*terminal.Window{{ID: "w1"}}, Width: 120, SessionName: "only", DaemonClient: client}
	if got := solo.railNeighbourSession(-1); got != "" {
		t.Errorf("a lone session offered %q to switch to", got)
	}

	solo.CycleSession(1)
	if len(solo.Notifications) == 0 {
		t.Fatal("cycling with nowhere to go said nothing")
	}
}
