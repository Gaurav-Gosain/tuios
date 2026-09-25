package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// listingOf builds a client whose cached listing is one session holding the
// given windows, which is what a complete refresh leaves behind.
func listingOf(name string, ids ...string) *session.TUIClient {
	c := session.NewTUIClient()
	windows := make([]session.WindowSummary, 0, len(ids))
	for _, id := range ids {
		windows = append(windows, session.WindowSummary{ID: id, Title: id})
	}
	c.UpdateSessionCache([]session.SessionInfo{
		{Name: name, WindowCount: len(windows), Windows: windows},
	})
	return c
}

// TestPruneHoldsOffWhileTheListingIsIncomplete: the union is only complete when
// the listing can account for every session's windows. Standalone knows nothing
// about other sessions, an older daemon sends a count with no summaries, and a
// cache from before a session switch has not caught up; pruning against any of
// them would take live panes' colours with it.
func TestPruneHoldsOffWhileTheListingIsIncomplete(t *testing.T) {
	withSidebar(t, true, "left", config.SidebarDefaultWidth)

	cases := map[string]func() *session.TUIClient{
		"no daemon": func() *session.TUIClient { return nil },
		"windows not fetched yet": func() *session.TUIClient {
			c := session.NewTUIClient()
			c.UpdateSessionCache([]session.SessionInfo{
				{Name: "s", WindowCount: 1, Windows: []session.WindowSummary{{ID: "w1"}}},
				{Name: "other", WindowCount: 2},
			})
			return c
		},
		"attached session not listed yet": func() *session.TUIClient {
			return listingOf("other", "f1")
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			m := &OS{
				Settings:     config.Global,
				Windows:      []*terminal.Window{{ID: "w1", CustomName: "one"}},
				Width:        120,
				Height:       40,
				SessionName:  "s",
				DaemonClient: build(),
			}
			m.SetWindowAccent("elsewhere", SlotAccent(2))
			m.markAgentSeen("elsewhere")

			m.pruneWindowKeyedState()

			if _, ok := m.WindowAccent("elsewhere"); !ok {
				t.Error("accent dropped against a listing that cannot say what exists")
			}
			if !m.agentSeen("elsewhere") {
				t.Error("unread bit dropped against a listing that cannot say what exists")
			}
		})
	}
}

// TestPruneRefusesAnotherDaemonsState is the guard on the one configuration
// where pruning would destroy user data rather than tidy it.
//
// Window IDs are unique within a daemon, and the state file is keyed by the XDG
// state directory, so two daemons on different sockets sharing one state
// directory each hold IDs the other's listing cannot account for. Without the
// socket check each would read the other's live panes as dead and delete their
// colours, which is worse than the leak the prune exists to fix.
func TestPruneRefusesAnotherDaemonsState(t *testing.T) {
	withSidebar(t, true, "left", config.SidebarDefaultWidth)

	m := &OS{
		Settings:     config.Global,
		Windows:      []*terminal.Window{{ID: "w1", CustomName: "one"}},
		Width:        120,
		Height:       40,
		SessionName:  "s",
		DaemonClient: listingOf("s", "w1"),
		SidebarAccents: map[string]Accent{
			"w1":      SlotAccent(2),
			"foreign": SlotAccent(3),
		},
		SidebarAgentSeen: map[string]bool{"foreign": true},
		// A socket no run of this test could be talking to.
		sidebarStateSocket: "/nonexistent/other-daemon.sock",
	}

	m.pruneWindowKeyedState()

	if _, ok := m.SidebarAccents["foreign"]; !ok {
		t.Error("prune deleted an accent belonging to another daemon's window")
	}
	if !m.SidebarAgentSeen["foreign"] {
		t.Error("prune deleted an unread bit belonging to another daemon's window")
	}
	if _, ok := m.SidebarAccents["w1"]; !ok {
		t.Error("prune deleted a live window's accent")
	}
}
