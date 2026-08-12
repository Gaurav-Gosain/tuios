package session

import "testing"

// TestSessionResizeLeavesPaneWinsize pins the pane size contract at the daemon:
// a pane's winsize is only ever a size a client announced for that pane over
// ResizePTY, never the session's own dimensions. Session.Resize used to push
// the new effective size into every PTY, so each guest was told the whole
// viewport's width; the retile that followed re-announced only panes whose tile
// size changed, and a pane whose geometry survived kept the lie. Its shell then
// drew prompts wider than the pane and the emulator wrapped them, one wrapped
// prompt per resize, in some panes and not their neighbours.
func TestSessionResizeLeavesPaneWinsize(t *testing.T) {
	_, sess := newTestDaemonSession(t)

	pty, err := sess.CreatePTY("win-resize-1", 65, 38, func(string) {})
	if err != nil {
		t.Fatalf("CreatePTY failed: %v", err)
	}

	// The effective session size changes: a client resized its terminal, or a
	// second client attached. Both routes land here.
	sess.Resize(130, 40)

	if w, h := sess.Size(); w != 130 || h != 40 {
		t.Errorf("session size = %dx%d, want 130x40", w, h)
	}
	if w, h := pty.Size(); w != 65 || h != 38 {
		t.Errorf("pane winsize = %dx%d after session resize, want the announced 65x38", w, h)
	}

	// A per-pane announcement still lands, and is the only thing that does.
	if err := pty.Resize(80, 20); err != nil {
		t.Fatalf("pty.Resize failed: %v", err)
	}
	sess.Resize(200, 60)
	if w, h := pty.Size(); w != 80 || h != 20 {
		t.Errorf("pane winsize = %dx%d, want the announced 80x20", w, h)
	}
}
