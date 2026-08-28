package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

func multiSessionClient() *session.TUIClient {
	c := session.NewTUIClient()
	c.UpdateSessionCache([]session.SessionInfo{
		{Name: "attached"},
		{Name: "other", Windows: []session.WindowSummary{{ID: "ow1", Title: "vim"}}},
	})
	return c
}

// TestForeignSessionRefreshPlan pins the poll gate: fast whenever a consumer is
// on screen, since the rail titles windows this client unsubscribed from out of
// that listing, a slow fallback when foreign sessions exist unseen, and nothing
// at all for an off-screen lone session or no daemon.
func TestForeignSessionRefreshPlan(t *testing.T) {
	tests := []struct {
		name        string
		client      *session.TUIClient
		sidebar     bool
		switcher    bool
		wantRefresh bool
		wantActive  bool // true => fast cadence, false => slow
	}{
		{"no daemon", nil, true, false, false, false},
		{"lone session, sidebar on", session.NewTUIClient(), true, false, true, true},
		{"lone session, nothing visible", session.NewTUIClient(), false, false, false, false},
		{"multi, sidebar on", multiSessionClient(), true, false, true, true},
		{"multi, switcher open", multiSessionClient(), false, true, true, true},
		{"multi, nothing visible", multiSessionClient(), false, false, true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			withSidebar(t, tc.sidebar, "left", config.SidebarDefaultWidth)
			m := &OS{Settings: config.Global, Width: 120, DaemonClient: tc.client, ShowSessionSwitcher: tc.switcher}
			after, refresh := m.foreignSessionRefreshPlan()
			if refresh != tc.wantRefresh {
				t.Fatalf("refresh = %v, want %v", refresh, tc.wantRefresh)
			}
			gotActive := after == foreignSessionRefreshActive
			if gotActive != tc.wantActive {
				t.Fatalf("interval = %v, want active=%v", after, tc.wantActive)
			}
			if !gotActive && after != foreignSessionRefreshIdle {
				t.Fatalf("non-fast interval = %v, want %v", after, foreignSessionRefreshIdle)
			}
		})
	}
}

// TestForeignSessionStalenessClears is the no-staleness regression guard: the
// sidebar tree is rebuilt from the client cache every frame, so once a refresh
// updates that cache a killed foreign session is gone and a new one appears.
// Gating the poll must not change this: while a consumer is visible the plan
// still refreshes, and BuildSessionTree still reflects whatever the cache holds.
func TestForeignSessionStalenessClears(t *testing.T) {
	withSidebar(t, true, "left", config.SidebarDefaultWidth)
	client := multiSessionClient()
	m := &OS{
		Settings:     config.Global,
		Windows:      []*terminal.Window{{ID: "aw1"}},
		SessionName:  "attached",
		Width:        120,
		DaemonClient: client,
	}

	if _, refresh := m.foreignSessionRefreshPlan(); !refresh {
		t.Fatal("with sidebar visible and two sessions the plan must poll")
	}
	if !hasSession(m.BuildSessionTree().Sessions, "other") {
		t.Fatal("foreign session missing before any change")
	}

	// A refresh runs UpdateSessionCache; a killed foreign session drops out.
	client.UpdateSessionCache([]session.SessionInfo{{Name: "attached"}})
	if hasSession(m.BuildSessionTree().Sessions, "other") {
		t.Fatal("killed foreign session still in the sidebar tree (staleness)")
	}

	// A newly created foreign session appears on the next refresh.
	client.UpdateSessionCache([]session.SessionInfo{
		{Name: "attached"},
		{Name: "fresh", Windows: []session.WindowSummary{{ID: "fw1", Title: "htop"}}},
	})
	if !hasSession(m.BuildSessionTree().Sessions, "fresh") {
		t.Fatal("new foreign session absent from the sidebar tree (staleness)")
	}
}

func hasSession(sessions []sessiontree.Node, id string) bool {
	for _, s := range sessions {
		if s.ID == id {
			return true
		}
	}
	return false
}
