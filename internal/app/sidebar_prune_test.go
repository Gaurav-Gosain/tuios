package app

import (
	"strconv"
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

// TestClientWindowStateDoesNotOutliveTheWindows is the leak guard for the two
// client-side maps keyed by window ID. Both are persisted, so an entry a closed
// pane leaves behind is not merely resident: it is reloaded every start, and the
// rail's signature folds every unread bit on every frame.
func TestClientWindowStateDoesNotOutliveTheWindows(t *testing.T) {
	withSidebar(t, true, "left", config.SidebarDefaultWidth)

	m := &OS{
		Windows: []*terminal.Window{
			{ID: "w1", CustomName: "one", AgentState: "done"},
			{ID: "w2", CustomName: "two", AgentState: "done"},
			{ID: "w3", CustomName: "three", AgentState: "done"},
		},
		FocusedWindow: 0,
		Width:         120,
		Height:        40,
		SessionName:   "s",
		DaemonClient:  listingOf("s", "w1", "w2", "w3"),
	}
	for _, w := range m.Windows {
		m.SetWindowAccent(w.ID, SlotAccent(3))
		m.markAgentSeen(w.ID)
	}

	// A local close, then a close pushed by the daemon, which is the only
	// teardown a daemon session performs.
	m.DeleteWindow(2)
	if err := m.ApplyStateSync(&session.SessionState{
		Name:            "s",
		FocusedWindowID: "w1",
		Windows:         []session.WindowState{{ID: "w1", Workspace: 1, AgentState: session.AgentStateDone}},
	}); err != nil {
		t.Fatalf("ApplyStateSync: %v", err)
	}
	if len(m.Windows) != 1 || m.Windows[0].ID != "w1" {
		t.Fatalf("windows = %d, want only w1", len(m.Windows))
	}

	// The next session-list refresh sees the closes, and the tick that drives it
	// is where the client's window-keyed state is reconciled.
	m.DaemonClient.UpdateSessionCache([]session.SessionInfo{
		{Name: "s", WindowCount: 1, Windows: []session.WindowSummary{{ID: "w1"}}},
	})
	m.Update(ForeignSessionRefreshTickMsg{})

	for _, id := range []string{"w2", "w3"} {
		if _, ok := m.WindowAccent(id); ok {
			t.Errorf("accent for closed window %s survived", id)
		}
		if m.agentSeen(id) {
			t.Errorf("unread bit for closed window %s survived", id)
		}
	}
	if _, ok := m.WindowAccent("w1"); !ok {
		t.Error("the live window lost its accent")
	}
	if !m.agentSeen("w1") {
		t.Error("the live window lost its unread bit")
	}
}

// TestForeignSessionStateSurvivesThePrune holds the line the prune must not
// cross: a pane of a session this client is not attached to is ranked and
// coloured out of the same two maps, so it counts as known even though it is
// not in m.Windows.
func TestForeignSessionStateSurvivesThePrune(t *testing.T) {
	withSidebar(t, true, "left", config.SidebarDefaultWidth)

	client := session.NewTUIClient()
	client.UpdateSessionCache([]session.SessionInfo{
		{Name: "s", WindowCount: 1, Windows: []session.WindowSummary{{ID: "w1", Title: "one"}}},
		{Name: "other", WindowCount: 1, Windows: []session.WindowSummary{{ID: "f1", Title: "far"}}},
	})
	m := &OS{
		Windows:       []*terminal.Window{{ID: "w1", CustomName: "one", AgentState: "done"}},
		FocusedWindow: 0,
		Width:         120,
		Height:        40,
		SessionName:   "s",
		DaemonClient:  client,
	}
	m.SetWindowAccent("f1", SlotAccent(5))
	m.markAgentSeen("f1")
	m.SetWindowAccent("gone", SlotAccent(6))
	m.markAgentSeen("gone")

	m.pruneWindowKeyedState()

	if _, ok := m.WindowAccent("f1"); !ok {
		t.Error("a foreign session's pane lost its accent")
	}
	if !m.agentSeen("f1") {
		t.Error("a foreign session's pane lost its unread bit")
	}
	if _, ok := m.WindowAccent("gone"); ok {
		t.Error("a window no session knows about kept its accent")
	}
	if m.agentSeen("gone") {
		t.Error("a window no session knows about kept its unread bit")
	}
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

// benchSignatureOS is a rail-sized model whose unread map carries n entries for
// windows that no longer exist.
func benchSignatureOS(stale int) *OS {
	wins := make([]*terminal.Window, 0, 6)
	for i := range 6 {
		wins = append(wins, &terminal.Window{ID: "w" + string(rune('a'+i)), CustomName: "window"})
	}
	m := &OS{Windows: wins, Width: 120, Height: 40, SessionName: "s"}
	m.SidebarAgentSeen = make(map[string]bool, stale)
	for i := range stale {
		m.SidebarAgentSeen["window-"+strconv.Itoa(i)] = true
	}
	return m
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

// BenchmarkSidebarSignatureStaleSeen measures what an unpruned unread map costs
// the rail every frame: the fold runs over every entry, live or not, so the
// per-frame cost grows with the map rather than with the windows on screen.
func BenchmarkSidebarSignatureStaleSeen(b *testing.B) {
	config.SidebarEnabled = true
	config.SidebarPosition = "left"
	config.SidebarWidth = config.SidebarDefaultWidth
	defer func() { config.SidebarEnabled = false }()

	for _, stale := range []int{0, 1000, 20000} {
		b.Run(strconv.Itoa(stale), func(b *testing.B) {
			m := benchSignatureOS(stale)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				m.sidebarSignature()
			}
		})
	}
}
