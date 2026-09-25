package app

import (
	"testing"
	"time"
)

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
