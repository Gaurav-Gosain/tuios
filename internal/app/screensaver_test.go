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

// TestScreensaverEatsTheKeyThatDismissesIt is the whole reason the intercept
// sits where it does.
//
// Someone comes back to the desk and types. The screen is showing an animation,
// not their shell, so the first keystroke must take the animation away and go
// no further. Letting it through means typing into a prompt nobody can see yet,
// and with bare digits now bound to window selection that first keystroke can
// move focus as well.
//
// Negative control: moving the dismissal below the getInputHandler call, or
// deleting it, makes the handler run and this fail.
func TestScreensaverEatsTheKeyThatDismissesIt(t *testing.T) {
	win := newTestWindow(t, "saver-0001", 40, 10)
	m := newTestOS(win)
	m.UserConfig = enabledScreensaverConfig(t, 10)
	m.screensaver.active = true
	m.screensaver.frame = "an animation"

	reached := withSpyInputHandler(t)

	_, _ = m.Update(tea.KeyPressMsg{Code: '1', Text: "1"})

	if *reached {
		t.Error("the key that dismissed the saver reached the pane's input handler")
	}
	if m.screensaver.active {
		t.Error("the saver is still up after a keypress")
	}
	if m.screensaver.frame != "" {
		t.Error("the saver kept its frame after being dismissed")
	}
}

// TestScreensaverEatsPointerMotionToo checks the same for the mouse, since a
// nudged desk is the other way someone announces they are back.
//
// Negative control: restricting the intercept to key messages lets the motion
// through and this fails.
func TestScreensaverEatsPointerMotionToo(t *testing.T) {
	win := newTestWindow(t, "saver-0002", 40, 10)
	m := newTestOS(win)
	m.UserConfig = enabledScreensaverConfig(t, 10)
	m.screensaver.active = true
	m.screensaver.frame = "an animation"

	reached := withSpyInputHandler(t)

	_, _ = m.Update(tea.MouseMotionMsg{})

	if *reached {
		t.Error("the pointer motion that dismissed the saver reached the input handler")
	}
	if m.screensaver.active {
		t.Error("the saver is still up after pointer motion")
	}
}

// TestInputReachesThePaneWhenTheSaverIsDown checks the intercept only eats one
// event, so ordinary typing is untouched.
//
// Negative control: eating input whenever the saver is merely enabled rather
// than actually showing makes this fail, and makes tuios unusable.
func TestInputReachesThePaneWhenTheSaverIsDown(t *testing.T) {
	win := newTestWindow(t, "saver-0003", 40, 10)
	m := newTestOS(win)
	m.UserConfig = enabledScreensaverConfig(t, 10)

	reached := withSpyInputHandler(t)

	_, _ = m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})

	if !*reached {
		t.Error("an ordinary keypress did not reach the pane")
	}
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

// TestScreensaverDoesNotArmWhenDisabled checks the off switch reaches the
// timer, not just the start.
//
// Negative control: arming regardless and checking enabled only on fire leaves
// a timer running in every session that never wanted one.
func TestScreensaverDoesNotArmWhenDisabled(t *testing.T) {
	win := newTestWindow(t, "saver-0005", 40, 10)
	m := newTestOS(win)
	m.UserConfig = config.DefaultConfig()

	if cmd := m.armScreensaver(); cmd != nil {
		t.Error("a session with the saver switched off armed a timer")
	}
}

// TestScreensaverRearmsWhenTheTimerFiresEarly checks the single-timer design
// under real use: a timer armed at the first keystroke of a long typing run
// fires while the session is still busy, and must wait out the remainder
// rather than starting the saver over someone's hands.
//
// Negative control: starting the saver whenever the timer fires makes this
// report the saver active.
func TestScreensaverRearmsWhenTheTimerFiresEarly(t *testing.T) {
	win := newTestWindow(t, "saver-0006", 40, 10)
	m := newTestOS(win)
	m.UserConfig = enabledScreensaverConfig(t, 10)

	m.screensaver.armed = true
	m.screensaver.lastInput = time.Now()

	cmd := m.handleScreensaverArm()
	if cmd == nil {
		t.Fatal("an early timer did not re-arm")
	}
	if !m.screensaver.armed {
		t.Error("the saver is not armed after an early fire")
	}
	if m.screensaver.active {
		t.Error("the saver started while the session was still being typed in")
	}
}

// TestScreensaverHoldsOffWhileAPaneIsBusy checks the promise that a saver never
// hides a running build.
//
// Negative control: dropping the foreground process check starts the saver over
// the build and this reports it active.
func TestScreensaverHoldsOffWhileAPaneIsBusy(t *testing.T) {
	win := newTestWindow(t, "saver-0007", 40, 10)
	m := newTestOS(win)
	cfg := enabledScreensaverConfig(t, 10)
	m.UserConfig = cfg

	// An agent that is working is the case a pane can report without a real
	// child process, so it is the one a test can set.
	win.AgentState = "working"
	if m.screensaverMayStart(cfg.Screensaver) {
		t.Error("the saver would have started over a working agent")
	}
	win.AgentState = "needs_input"
	if m.screensaverMayStart(cfg.Screensaver) {
		t.Error("the saver would have started over an agent waiting on the user")
	}
	win.AgentState = "idle"
	if !m.screensaverMayStart(cfg.Screensaver) {
		t.Error("the saver refused to start over an idle pane")
	}

	// And the setting that says to run anyway.
	win.AgentState = "working"
	yes := true
	cfg.Screensaver.WhileBusy = &yes
	if !m.screensaverMayStart(cfg.Screensaver) {
		t.Error("while_busy did not override the busy check")
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

// TestScreensaverEffectNameFallsBackToRandom checks an unknown effect name in a
// config file does not stop the saver working.
//
// Negative control: returning the configured name unchecked makes Lookup fail
// and the saver never start, with nothing said about why.
func TestScreensaverEffectNameFallsBackToRandom(t *testing.T) {
	cfg := config.ScreensaverConfig{Effect: "not_an_effect"}
	if got := cfg.EffectName(); got != config.ScreensaverRandomEffect {
		t.Errorf("an unknown effect resolved to %q, want %q", got, config.ScreensaverRandomEffect)
	}
	name, effect := screensaverEffect(config.ScreensaverRandomEffect)
	if effect == nil {
		t.Fatal("random resolved to no effect")
	}
	if _, ok := config.LookupOption("screensaver.effect"); !ok {
		t.Fatal("screensaver.effect is not in the option registry")
	}
	if name == config.ScreensaverRandomEffect {
		t.Error("random resolved to itself rather than to a real effect")
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
