package app

import (
	"testing"
	"time"
)

// TestNotificationKeepsTheFrameDrawing is the regression test for a toast that
// never comes down.
//
// Notifications expire on a wall-clock timer, but the only thing that retires
// one is CleanupNotifications, and that runs while a frame is being composed.
// The maintenance tick used to have no reason to compose a frame just because a
// notification was on screen, so once the session went quiet - no animation, no
// PTY output, no keystroke - the last frame drawn was served from the render
// cache indefinitely, with whatever toast happened to be up still painted over
// the panes underneath it.
//
// This is how a project tape that split a pane and echoed into it could finish
// correctly and still show an empty pane: the tape's own "Trusted ..." toast was
// sitting exactly where the new pane's first lines were, the panes then went
// idle, and no later frame ever replaced that one.
func TestNotificationKeepsTheFrameDrawing(t *testing.T) {
	win := newTestWindow(t, "notif-frame-0001", 60, 34)
	m := newTestOS(win)
	m.Width, m.Height = 120, 40

	// Baseline: an idle session with nothing on screen may skip the frame.
	if _, _ = m.Update(TickerMsg(time.Now())); !m.renderSkipped {
		t.Fatal("an idle tick with nothing on screen should skip the frame")
	}

	m.ShowNotification("built the layout", "info", 40*time.Millisecond)
	if _, _ = m.Update(TickerMsg(time.Now())); m.renderSkipped {
		t.Fatal("a tick with a notification on screen skipped the frame; the toast would stay painted over the panes under it")
	}

	// Once it has expired and been retired, ticks may go back to skipping.
	time.Sleep(60 * time.Millisecond)
	m.CleanupNotifications()
	if len(m.Notifications) != 0 {
		t.Fatalf("expected the notification to have expired, got %d", len(m.Notifications))
	}
	if _, _ = m.Update(TickerMsg(time.Now())); !m.renderSkipped {
		t.Error("a tick with no notification left should skip the frame again")
	}
}

// TestFinishedScriptExitDrawsOnce covers the same class of freeze for the tape
// completion indicator. maybeExitFinishedScript takes "DONE" off screen, and its
// return value exists so the tick that does it draws; ignoring it left the
// indicator in the cached frame with nothing scheduled to redraw.
func TestFinishedScriptExitDrawsOnce(t *testing.T) {
	win := newTestWindow(t, "script-done-0001", 60, 34)
	m := newTestOS(win)
	m.Width, m.Height = 120, 40
	m.ScriptMode = true
	m.ScriptFinishedTime = time.Now().Add(-2 * scriptDoneLinger)

	if _, _ = m.Update(TickerMsg(time.Now())); m.renderSkipped {
		t.Fatal("the tick that left script mode skipped the frame; the DONE indicator would stay on screen")
	}
	if m.ScriptMode {
		t.Fatal("expected the finished script's mode to have been left")
	}
}
