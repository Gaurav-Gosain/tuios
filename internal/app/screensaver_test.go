package app

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// withSpyInputHandler swaps in a handler that records whether it was reached,
// and puts the old one back afterwards.
func withSpyInputHandler(t *testing.T) *bool {
	t.Helper()
	previous := getInputHandler()
	reached := false
	SetInputHandler(func(_ tea.Msg, m *OS) (tea.Model, tea.Cmd) {
		reached = true
		return m, nil
	})
	t.Cleanup(func() {
		if previous != nil {
			SetInputHandler(previous)
		}
	})
	return &reached
}

func enabledScreensaverConfig(t *testing.T, minutes int) *config.UserConfig {
	t.Helper()
	cfg := config.DefaultConfig()
	on := true
	cfg.Screensaver.Enabled = &on
	cfg.Screensaver.IdleMinutes = minutes
	return cfg
}

// TestScreensaverArmsOneTimerAtATime checks the idle promise: the whole design
// rests on input never queueing a timer per keystroke.
//
// Negative control: dropping the armed check from armScreensaver returns a
// command for every call, which is one live timer per keypress.
func TestScreensaverArmsOneTimerAtATime(t *testing.T) {
	win := newTestWindow(t, "saver-0004", 40, 10)
	m := newTestOS(win)
	m.UserConfig = enabledScreensaverConfig(t, 10)

	if cmd := m.armScreensaver(); cmd == nil {
		t.Fatal("the first arm produced no timer")
	}
	for i := range 50 {
		if cmd := m.armScreensaver(); cmd != nil {
			t.Fatalf("arm %d produced a second live timer", i+2)
		}
	}
}

// TestScreensaverFrameMessageDoesNotResurrectADismissedSaver checks a stale
// timer cannot restart the animation.
//
// A frame timer is in flight whenever the saver runs, and dismissal cannot
// cancel it. Without the guard the message that lands a moment later would draw
// another frame and schedule the next, and the animation would come back over
// the shell someone had just started typing into.
//
// Negative control: dropping the active check makes this return a command and
// leaves a frame timer running forever.
func TestScreensaverFrameMessageDoesNotResurrectADismissedSaver(t *testing.T) {
	win := newTestWindow(t, "saver-0008", 40, 10)
	m := newTestOS(win)
	m.UserConfig = enabledScreensaverConfig(t, 10)

	if cmd := m.handleScreensaverFrame(); cmd != nil {
		t.Error("a frame message for a dismissed saver scheduled another frame")
	}
	if m.screensaver.active {
		t.Error("a frame message brought a dismissed saver back")
	}
}

// TestArmedScreensaverDoesNothingToTheIdleTick is the guard on the constraint
// that shaped this whole feature.
//
// tuios must not tick at idle, and a screen saver is by definition a thing that
// waits for idle. It gets away with it because arming is one deferred timer and
// the running animation drives its own frames, so nothing on the maintenance
// tick's fast path ever reads screensaver state. This asserts that directly: a
// run of idle ticks with the saver armed must do the same nothing it does with
// the saver switched off.
//
// Negative control: adding a screensaver term to tickNeedsWork makes work climb
// with every tick and this fails.
func TestArmedScreensaverDoesNothingToTheIdleTick(t *testing.T) {
	m := idleOS(t, 3)
	m.UserConfig = enabledScreensaverConfig(t, 10)
	if cmd := m.armScreensaver(); cmd == nil {
		t.Fatal("the saver did not arm")
	}
	if !m.screensaver.armed {
		t.Fatal("the saver reports itself unarmed")
	}

	for range 5 {
		m.Update(TickerMsg(time.Now()))
	}
	_, workBefore, renderBefore := m.TickStats()

	const ticks = 100
	for range ticks {
		m.Update(TickerMsg(time.Now()))
	}
	_, workAfter, renderAfter := m.TickStats()

	if workAfter != workBefore {
		t.Errorf("an armed saver did %d ticks of work over %d idle ticks, want 0",
			workAfter-workBefore, ticks)
	}
	if renderAfter != renderBefore {
		t.Errorf("an armed saver drew %d frames over %d idle ticks, want 0",
			renderAfter-renderBefore, ticks)
	}
	if !m.screensaver.armed {
		t.Error("the idle ticks disarmed the saver")
	}
	if m.screensaver.active {
		t.Error("an idle tick started the saver, which only its own timer may do")
	}
}
