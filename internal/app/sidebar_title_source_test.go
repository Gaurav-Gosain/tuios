package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// railTitleClient seeds a client cache with the attached session's windows as
// the daemon reports them.
func railTitleClient(windows ...session.WindowSummary) *session.TUIClient {
	c := session.NewTUIClient()
	c.UpdateSessionCache([]session.SessionInfo{{Name: "attached", Windows: windows}})
	return c
}

// TestRailRetitlesWindowsItStoppedWatching is the regression test for a rail row
// holding a title the window no longer has. Leaving a workspace unsubscribes its
// panes, so their output never reaches this client's emulator again and the
// title it holds is frozen, while the rail goes on listing them. The daemon
// reads every byte, so its listing is what those rows must be titled from.
func TestRailRetitlesWindowsItStoppedWatching(t *testing.T) {
	watched := &terminal.Window{ID: "w1", PTYID: "p1", DaemonMode: true}
	watched.SetTitle("bash")
	dropped := &terminal.Window{ID: "w2", PTYID: "p2", DaemonMode: true}
	dropped.SetTitle("bash") // what it was called when the client stopped watching

	m := &OS{
		Settings:    config.Global,
		SessionName: "attached",
		Windows:     []*terminal.Window{watched, dropped},
		DaemonClient: railTitleClient(
			session.WindowSummary{ID: "w1", Title: "stale-from-daemon"},
			session.WindowSummary{ID: "w2", Title: "vim"},
		),
		SubscribedPTYs: map[string]bool{"p1": true},
	}

	m.updateRailTitles()

	if got := m.railTitleShown(dropped); got != "vim" {
		t.Errorf("rail title for an unwatched window = %q, want the daemon's %q", got, "vim")
	}
	// A window this client still watches is titled from its own emulator, which
	// is live and ahead of any listing.
	if got := m.railTitleShown(watched); got != "bash" {
		t.Errorf("rail title for a watched window = %q, want the local %q", got, "bash")
	}
}

// TestRailKeepsCustomNameOverDaemonTitle guards the user's own naming: a renamed
// window is named here, so no listing may overwrite it.
func TestRailKeepsCustomNameOverDaemonTitle(t *testing.T) {
	w := &terminal.Window{ID: "w1", PTYID: "p1", DaemonMode: true, CustomName: "logs"}
	w.SetTitle("bash")
	m := &OS{
		Settings:       config.Global,
		SessionName:    "attached",
		Windows:        []*terminal.Window{w},
		DaemonClient:   railTitleClient(session.WindowSummary{ID: "w1", Title: "vim"}),
		SubscribedPTYs: map[string]bool{},
	}

	m.updateRailTitles()

	if got := m.railTitleShown(w); got != "logs" {
		t.Errorf("rail title = %q, want the custom name %q", got, "logs")
	}
}

// TestRailTitleTickWakesOnMovedListing pins the idle gate: a listing is the only
// way an unwatched window's title can change, so a moved listing has to count as
// work or the row settles on the old title until something unrelated happens.
func TestRailTitleTickWakesOnMovedListing(t *testing.T) {
	w := &terminal.Window{ID: "w1", PTYID: "p1", DaemonMode: true}
	w.SetTitle("bash")
	client := railTitleClient(session.WindowSummary{ID: "w1", Title: "bash"})
	m := &OS{
		Settings:       config.Global,
		SessionName:    "attached",
		Windows:        []*terminal.Window{w},
		DaemonClient:   client,
		SubscribedPTYs: map[string]bool{},
	}

	m.updateRailTitles()
	if m.tickNeedsWork() {
		t.Fatal("the tick has work with nothing changed, so the client never goes idle")
	}

	client.UpdateSessionCache([]session.SessionInfo{
		{Name: "attached", Windows: []session.WindowSummary{{ID: "w1", Title: "vim"}}},
	})
	if !m.tickNeedsWork() {
		t.Fatal("a moved listing did not wake the tick, so the rail keeps the old title")
	}
}
