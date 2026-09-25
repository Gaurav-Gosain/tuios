package app

import (
	tea "charm.land/bubbletea/v2"

	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// placedClient is a listing in which the daemon has said where each session's
// focused pane is.
func placedClient() *session.TUIClient {
	c := session.NewTUIClient()
	c.UpdateSessionCache([]session.SessionInfo{
		{Name: "session-0", Dir: "tuios", Branch: "main"},
		{Name: "session-1", Dir: "docs"},
		{Name: "api", Dir: "payments", Branch: "release"},
		{Name: "session-2", Dir: "site", Branch: "next", DisplayName: "Site"},
	})
	return c
}

// sessionRows is the sessions section of a rendered rail: the rows between its
// header and the terminals header, where a pane titled after the same
// directory would otherwise match too.
func sessionRows(rows []string) []string {
	var out []string
	in := false
	for _, r := range rows {
		text := strings.TrimSpace(r)
		switch {
		case strings.HasPrefix(text, "sessions"):
			in = true
			continue
		case strings.HasPrefix(text, "terminals"):
			return out
		}
		if in {
			out = append(out, r)
		}
	}
	return out
}

// sessionRow is the rendered rail row for one session, by the text it must carry.
func sessionRow(t *testing.T, rows []string, want string) string {
	t.Helper()
	hits := paneRows(sessionRows(rows), want)
	if len(hits) != 1 {
		t.Fatalf("want exactly one row containing %q, got %d:\n%s", want, len(hits), strings.Join(rows, "\n"))
	}
	return hits[0]
}

// TestRailPlaceRidesTheCache: the place is a render input, so a shell that
// changes branch has to rebuild the rail rather than leave a stale word on it.
func TestRailPlaceRidesTheCache(t *testing.T) {
	m := bareShellOS(t, 1)
	m.SessionName = "session-0"
	m.DaemonClient = placedClient()
	sidebarText(t, m)
	sig := m.sidebarCache.sig

	// The same sessions, and only one branch differs: a listing that changed
	// in nothing else is the case the comparison has to catch.
	m.DaemonClient.UpdateSessionCache([]session.SessionInfo{
		{Name: "session-0", Dir: "tuios", Branch: "fix/rail"},
		{Name: "session-1", Dir: "docs"},
		{Name: "api", Dir: "payments", Branch: "release"},
		{Name: "session-2", Dir: "site", Branch: "next", DisplayName: "Site"},
	})
	sidebarText(t, m)
	if m.sidebarCache.sig == sig {
		t.Fatal("a branch change did not rebuild the rail")
	}
	if !strings.Contains(sessionRow(t, railText(t, m), "tuios"), "fix/rail") {
		t.Error("the new branch never reached the drawn row")
	}
}

// TestOpeningTheRailReplansTheListingPoll: the poll armed at attach for a lone
// session with the rail hidden is slow and refreshes nothing, and it re-plans
// only when it fires. Opening the rail has to arm a fresh poll at once, and the
// timer it replaces has to be retired rather than left to double the polling.
func TestOpeningTheRailReplansTheListingPoll(t *testing.T) {
	m := bareShellOS(t, 1)
	m.SessionName = "session-0"
	m.DaemonClient = placedClient()
	withSidebar(t, false, "left", config.SidebarDefaultWidth)
	m.Settings = config.Global

	// A no-op message with the rail closed arms nothing.
	if _, cmd := m.Update(ForeignSessionRefreshTickMsg{Gen: m.foreignTickGen}); cmd == nil {
		t.Fatal("the tick handler must re-arm its own timer")
	}
	before := m.foreignTickGen

	m.ToggleSidebar()
	if !m.SidebarActive() {
		t.Fatal("the toggle did not open the rail")
	}
	_, cmd := m.Update(tea.FocusMsg{})
	if cmd == nil {
		t.Fatal("opening the rail returned no command, so the poll was not re-planned")
	}
	if m.foreignTickGen == before {
		t.Fatal("opening the rail did not arm a new poll timer")
	}

	// The timer the re-plan replaced fires later. It must not re-arm.
	if _, cmd := m.Update(ForeignSessionRefreshTickMsg{Gen: before}); cmd != nil {
		t.Fatal("a retired poll timer re-armed itself, which doubles the polling")
	}
	if _, cmd := m.Update(ForeignSessionRefreshTickMsg{Gen: m.foreignTickGen}); cmd == nil {
		t.Fatal("the live poll timer did not re-arm")
	}
}
