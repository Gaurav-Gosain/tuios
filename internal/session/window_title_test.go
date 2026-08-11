package session

import (
	"testing"
)

// feedVT writes bytes to a PTY's daemon-side emulator the way its output reader
// would, so a test can hand a window the escape sequence an application uses to
// set its title.
func feedVT(t *testing.T, p *PTY, data string) {
	t.Helper()
	p.terminalMu.Lock()
	defer p.terminalMu.Unlock()
	if _, err := p.terminal.Write([]byte(data)); err != nil {
		t.Fatalf("write to the daemon emulator: %v", err)
	}
}

// TestListingReportsTheLiveWindowTitle is the regression test for a sidebar row
// showing a title the window stopped having. A window's title reached the
// daemon's stored state only when the attached client happened to push a state
// sync, which it does on structural changes and never on a title change, so
// every listing (and so every other client's rail) reported the title as of the
// last time a window was moved or resized. The daemon's own emulator sees the
// title change as it happens, and that is what a listing now reports.
func TestListingReportsTheLiveWindowTitle(t *testing.T) {
	d, _ := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")

	ids := sess.ListPTYIDs()
	if len(ids) != 1 {
		t.Fatalf("precondition: want one PTY, got %d", len(ids))
	}
	feedVT(t, sess.GetPTY(ids[0]), "\x1b]0;vim\x07")

	windows := sess.Info().Windows
	if len(windows) != 1 {
		t.Fatalf("listing has %d windows, want 1", len(windows))
	}
	if windows[0].Title != "vim" {
		t.Errorf("listed title = %q, want the live %q", windows[0].Title, "vim")
	}

	// A reattach restores from the session state, so it must carry the live title
	// too or the pane comes back wearing an old name.
	state := sess.GetState()
	if len(state.Windows) != 1 || state.Windows[0].Title != "vim" {
		t.Errorf("state title = %q, want the live %q", state.Windows[0].Title, "vim")
	}
}

// TestCustomNameOutranksTheLiveTitle keeps an explicit rename in charge of the
// row: the shell may retitle itself as often as it likes, the user's name wins.
func TestCustomNameOutranksTheLiveTitle(t *testing.T) {
	d, _ := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")

	state := sess.GetState()
	state.Windows[0].CustomName = "logs"
	sess.UpdateState(state)

	feedVT(t, sess.GetPTY(sess.ListPTYIDs()[0]), "\x1b]0;vim\x07")

	if got := sess.Info().Windows[0].Title; got != "logs" {
		t.Errorf("listed title = %q, want the custom name %q", got, "logs")
	}
}
