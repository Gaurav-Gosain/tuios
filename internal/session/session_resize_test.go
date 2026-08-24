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

// TestResizeToTheSameSizeIsANoOp pins that a pane told the size it is already
// at is told nothing.
//
// It is not a rare case. Every client of a session announces every pane's size
// for itself, so a second client attaching, or any client re-announcing after a
// retile that moved nothing, arrives with the size the pane already has. Each
// of those used to mark the ring, broadcast a width to every subscriber, resize
// the daemon's emulator - which drops the scroll region a full-screen program
// set - and SIGWINCH the guest into repainting its prompt. Under a reflowing
// emulator a repaint that lands one row short is a line of lost scrollback, so
// a resize that changes nothing was never free.
//
// NEGATIVE CONTROL: fails without the guard at the top of PTY.Resize, where the
// second call appends a second resize mark and broadcasts a width nobody asked
// for.
func TestResizeToTheSameSizeIsANoOp(t *testing.T) {
	_, sess := newTestDaemonSession(t)

	pty, err := sess.CreatePTY("win-resize-noop", 65, 38, func(string) {})
	if err != nil {
		t.Fatalf("CreatePTY failed: %v", err)
	}

	if err := pty.Resize(80, 20); err != nil {
		t.Fatalf("first resize: %v", err)
	}
	pty.outputMu.Lock()
	marks := len(pty.resizeMarks)
	pty.outputMu.Unlock()

	// The same size again, twice, from what would be two other clients.
	for range 2 {
		if err := pty.Resize(80, 20); err != nil {
			t.Fatalf("repeat resize: %v", err)
		}
	}

	pty.outputMu.Lock()
	after := len(pty.resizeMarks)
	pty.outputMu.Unlock()
	if after != marks {
		t.Errorf("two resizes to the size the pane was already at added %d marks to the stream, want 0",
			after-marks)
	}
	if w, h := pty.Size(); w != 80 || h != 20 {
		t.Errorf("pane winsize = %dx%d after the repeats, want 80x20", w, h)
	}

	// A real change still lands.
	if err := pty.Resize(80, 21); err != nil {
		t.Fatalf("changed resize: %v", err)
	}
	pty.outputMu.Lock()
	changed := len(pty.resizeMarks)
	pty.outputMu.Unlock()
	if changed == after {
		t.Error("a resize that did change the size was swallowed too")
	}
}
