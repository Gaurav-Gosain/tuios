package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
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
