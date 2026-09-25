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

// TestRailLabelsAnUnnamedSessionByDirectoryAndBranch is the acceptance test on
// a rendered frame: six generated names read as six directories, a branch
// follows in a second word, a name the user chose stays, and the branch still
// follows it.
func TestRailLabelsAnUnnamedSessionByDirectoryAndBranch(t *testing.T) {
	m := bareShellOS(t, 1)
	m.SessionName = "session-0"
	m.DaemonClient = placedClient()

	rows := railText(t, m)
	if !strings.Contains(sessionRow(t, rows, "tuios"), "tuios main") {
		t.Errorf("the attached session's row does not read as its directory and branch:\n%s", strings.Join(rows, "\n"))
	}
	if row := sessionRow(t, rows, "docs"); strings.Contains(row, "session-1") {
		t.Errorf("a generated name survived next to its directory: %q", row)
	}
	if hits := paneRows(sessionRows(rows), "payments"); len(hits) != 0 {
		t.Errorf("a named session must keep its name, but was relabelled by its directory: %q", hits)
	} else if row := sessionRow(t, rows, "api"); !strings.Contains(row, "api release") {
		t.Errorf("a named session must gain the branch: %q", row)
	}
	if row := sessionRow(t, rows, "Site"); !strings.Contains(row, "Site next") || strings.Contains(row, "site next") {
		t.Errorf("a display name must win over the directory: %q", row)
	}
	for _, r := range sessionRows(rows) {
		if strings.Contains(r, "session-") {
			t.Errorf("a generated name reached the rail: %q", r)
		}
	}
}

// TestRailDropsTheBranchWhenItDoesNotFit: a rail too narrow for both keeps the
// name whole and drops the branch, rather than cutting the name to make room
// for a word that then describes nothing.
func TestRailDropsTheBranchWhenItDoesNotFit(t *testing.T) {
	m := bareShellOS(t, 1)
	m.SessionName = "session-0"
	c := session.NewTUIClient()
	c.UpdateSessionCache([]session.SessionInfo{
		{Name: "session-0", Dir: "a-directory-with-a-very-long-name", Branch: "main"},
	})
	m.DaemonClient = c

	rows := railText(t, m)
	for _, r := range rows {
		if strings.Contains(r, "a-directory") {
			if strings.Contains(r, "main") {
				t.Fatalf("the branch was drawn on a row with no room for it: %q", r)
			}
			return
		}
	}
	t.Fatalf("the directory label never reached the rail:\n%s", strings.Join(rows, "\n"))
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
