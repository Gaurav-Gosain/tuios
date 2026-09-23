package app

import (
	"testing"
	"time"
)

// TestNotificationKeepsTheFrameDrawing is the regression test for a message
// that never comes down.
//
// Messages expire on a wall-clock timer, and the only thing that retires one is
// CleanupNotifications. That used to run while a frame was being composed, so
// the maintenance tick had no reason to compose a frame just because a message
// was on screen. Once the session went quiet (no animation, no PTY output, no
// keystroke), the last frame drawn was served from the render cache
// indefinitely, with whatever message happened to be up still painted over the
// panes underneath it.
//
// This is how a project tape that split a pane and echoed into it could finish
// correctly and still show an empty pane: the tape's own "Trusted ..." toast was
// sitting exactly where the new pane's first lines were, the panes then went
// idle, and no later frame ever replaced that one.
//
// The cap design moved the message into the dock, so it can no longer cover a
// pane, but the coupling it was drawn from would be just as wrong there: the
// hairline under a message burns down per frame, and a message that outlived
// its duration on a cached frame would sit on a rule frozen partway through.
// Expiry now happens on the tick, which is what this pins.
func TestNotificationKeepsTheFrameDrawing(t *testing.T) {
	win := newTestWindow(t, "notif-frame-0001", 60, 34)
	m := newTestOS(win)
	m.Width, m.Height = 120, 40

	// Baseline: an idle session with nothing on screen may skip the frame.
	if _, _ = m.Update(TickerMsg(time.Now())); !m.renderSkipped {
		t.Fatal("an idle tick with nothing on screen should skip the frame")
	}

	m.ShowNotification("built the layout", "info", 40*time.Millisecond)
	if len(m.Notifications) != 1 {
		t.Fatalf("expected the message to be live, got %d", len(m.Notifications))
	}
	if _, _ = m.Update(TickerMsg(time.Now())); m.renderSkipped {
		t.Fatal("a tick with a message on screen skipped the frame; the dock's burn-down rule would freeze partway")
	}

	// The requested 40ms is below the severity floor, so the message is still
	// up: a duration is a floor now, not the answer. Age it past the floor by
	// hand rather than sleeping six seconds for it.
	m.Notifications[0].StartTime = time.Now().Add(-2 * m.Settings.NotificationDuration)

	// The tick that retires it must still draw, or the message leaves the model
	// and stays on the screen: that is the same freeze in a different place.
	if _, _ = m.Update(TickerMsg(time.Now())); m.renderSkipped {
		t.Fatal("the tick that retired the message skipped the frame; it would stay drawn in the cached dock")
	}
	if len(m.Notifications) != 0 {
		t.Fatalf("the tick should have retired the expired message, got %d", len(m.Notifications))
	}

	// With nothing left, ticks may go back to skipping.
	if _, _ = m.Update(TickerMsg(time.Now())); !m.renderSkipped {
		t.Error("a tick with no message left should skip the frame again")
	}
}

// TestStickyErrorWaitsForDismissal pins the two halves of the sticky contract:
// an error does not expire on its own, and esc is a way out of it. Without the
// second half the first is a trap.
func TestStickyErrorWaitsForDismissal(t *testing.T) {
	win := newTestWindow(t, "notif-sticky-0001", 60, 34)
	m := newTestOS(win)
	m.Width, m.Height = 120, 40

	m.ShowNotification("Capture failed: permission denied", "error", m.Settings.NotificationDuration)
	if len(m.Notifications) != 1 || !m.Notifications[0].Sticky {
		t.Fatalf("an error should be sticky by default, got %+v", m.Notifications)
	}

	// However old it gets, it stays.
	m.Notifications[0].StartTime = time.Now().Add(-time.Hour)
	if m.CleanupNotifications() || len(m.Notifications) != 1 {
		t.Fatal("a sticky error expired on a timer the user did not start")
	}

	if !m.DismissNotifications() || len(m.Notifications) != 0 {
		t.Fatal("esc did not clear the sticky error, which leaves it with no exit at all")
	}
	if m.DismissNotifications() {
		t.Error("dismissing an empty queue should report that there was nothing to dismiss")
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

// TestNotificationTickComposesOnlyWhenTheBurnMoves pins what a live message
// costs the tick. The tick runs at the frame rate while a message is up, and it
// used to compose on every one of those ticks, although the rule under the
// message moves one cell at a time and nothing else in the block moves on its
// own: a connect toast composed 171 frames in three seconds to put 18 on the
// wire. A tick now composes when the burn has moved a cell since the frame that
// drew it, or when the top message or the queue behind it changed, and skips
// otherwise. The burn still moves: the tick that crosses a cell draws it.
//
// Negative control: putting hasNotifications back into the tick's needsRender
// fails the "no cell moved" assertions, because every tick composes again.
func TestNotificationTickComposesOnlyWhenTheBurnMoves(t *testing.T) {
	win := newTestWindow(t, "notif-burn-0001", 60, 34)
	m := newTestOS(win)
	m.Width, m.Height = 120, 40

	tick := func() bool {
		t.Helper()
		_, _ = m.Update(TickerMsg(time.Now()))
		if m.renderSkipped {
			return false
		}
		m.View()
		return true
	}

	m.ShowNotification("attached to the session", "info", m.Settings.NotificationDuration)
	if !tick() {
		t.Fatal("the first tick with a new message skipped the frame; it would never be drawn")
	}
	span := m.notifDrawn.span
	if span < 2 {
		t.Fatalf("setup: the dock drew the message across %d cells, not a rule that can burn", span)
	}
	duration := m.Notifications[0].Duration
	age := func(cells float64) {
		m.Notifications[0].StartTime = time.Now().Add(-time.Duration(cells / float64(span) * float64(duration)))
	}

	if tick() {
		t.Error("a tick right after the frame that drew the message composed again; no cell moved")
	}
	// A third of a cell into the burn: the rule still rounds to where it was.
	age(0.3)
	if tick() {
		t.Error("a tick before the burn crossed a cell composed a frame identical to the last")
	}
	// One cell in: the rule is a cell shorter, and that has to reach the screen.
	age(1)
	if !tick() {
		t.Fatal("the tick on which the burn crossed a cell skipped the frame; the rule would freeze")
	}
	if got, want := m.notifDrawn.lit, span-1; got != want {
		t.Errorf("the drawn rule has %d lit cells, want %d", got, want)
	}
	if tick() {
		t.Error("a tick after the burn step was drawn composed again; no cell moved")
	}

	// A second message takes the block: a new top message, and one queued.
	m.ShowNotification("a second message", "info", m.Settings.NotificationDuration)
	if !tick() {
		t.Error("the tick after a new message arrived skipped the frame")
	}
	if m.notifDrawn.queued != 1 {
		t.Errorf("the drawn block counts %d queued, want 1", m.notifDrawn.queued)
	}
}
