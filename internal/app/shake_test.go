package app

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The gesture is cheap to detect and expensive to get wrong. Every test here is
// about the second half: what must not count as a shake. The one positive test
// is there so the negatives are known to be testing something.

// shakeOS is a client with the gesture turned on and the beam off.
func shakeOS(t *testing.T) *OS {
	t.Helper()
	win := newTestWindow(t, "shake", 40, 10)
	m := newTestOS(win)
	m.Width, m.Height = 90, 30
	m.UserConfig = config.DefaultConfig()
	m.UserConfig.Spotlight.Shake = true
	m.Settings = config.DefaultSettings()
	return m
}

// shakeMove is one pointer sample: a column and how long after the last one it
// arrived.
type shakeMove struct {
	x     int
	after time.Duration
}

// wiggle is the sample sequence a person's hand makes: legs of the given width,
// alternating, one sample per leg, at a steady rate. It emits one leg more than
// the turns asked for, because the first leg only says which way the pointer is
// going and turns nothing round.
func wiggle(startX, leg, turns int, every time.Duration) []shakeMove {
	moves := []shakeMove{{x: startX, after: time.Second}}
	x, dir := startX, 1
	for range turns + 1 {
		x += dir * leg
		dir = -dir
		moves = append(moves, shakeMove{x: x, after: every})
	}
	return moves
}

// play feeds a sequence to the detector on a clock the test owns, so the pause
// between two gestures is a duration rather than a sleep. The button is the one
// held throughout, so a drag is the same call.
func play(m *OS, clock *time.Time, button tea.MouseButton, moves []shakeMove) {
	for _, mv := range moves {
		*clock = clock.Add(mv.after)
		m.noteShakeMotion(tea.MouseMotionMsg{X: mv.x, Y: 5, Button: button}, *clock)
	}
}

// TestShakingThePointerTogglesTheSpotlight is the positive half every test
// below needs. Without it a negative test could be reading its answer off a
// fixture that can never fire at all.
func TestShakingThePointerTogglesTheSpotlight(t *testing.T) {
	m := shakeOS(t)
	clock := time.Now()

	play(m, &clock, tea.MouseNone, wiggle(40, 20, shakeReversalsToFire, 100*time.Millisecond))

	if !m.SpotlightOn() {
		t.Fatal("four fast reversals of twenty columns did not turn the beam on")
	}
	if m.UserConfig.Spotlight.Enabled == nil || !*m.UserConfig.Spotlight.Enabled {
		t.Error("the gesture moved the beam without going through ToggleSpotlight, " +
			"so the key, the palette and the settings row are now out of step with it")
	}
	if len(m.Notifications) == 0 {
		t.Error("the beam changed with no word for it; a gesture nobody meant to make " +
			"leaves the user with a dark screen and no explanation")
	}
}

// TestASlowWiggleIsNotAShake protects a person moving the pointer about while
// they think. The same four reversals, with a pause between each, are not a
// gesture: what makes a shake a shake is that it has no pause in it.
func TestASlowWiggleIsNotAShake(t *testing.T) {
	m := shakeOS(t)
	clock := time.Now()

	play(m, &clock, tea.MouseNone, wiggle(40, 20, 8, shakeMaxReversalGap+50*time.Millisecond))

	if m.SpotlightOn() {
		t.Error("eight reversals a third of a second apart toggled the beam; " +
			"any pointer wandering across a screen would set this off")
	}
}

// TestPointerJitterIsNotAShake protects a hand resting on a mouse, and every
// high-DPI device whose acceleration turns a tremor into several columns. The
// reversals are there and they are fast; they are one or two columns wide.
func TestPointerJitterIsNotAShake(t *testing.T) {
	m := shakeOS(t)
	clock := time.Now()

	play(m, &clock, tea.MouseNone, wiggle(40, shakeMinLegColumns-1, 40, 10*time.Millisecond))

	if m.SpotlightOn() {
		t.Errorf("forty reversals of %d columns toggled the beam; "+
			"the amplitude threshold is not holding and a resting hand fires the gesture",
			shakeMinLegColumns-1)
	}
}

// TestASweepAcrossTheScreenAndBackIsNotAShake protects the commonest pointer
// move there is: going somewhere and coming back. It is one reversal, and it is
// as fast and as wide as a shake.
func TestASweepAcrossTheScreenAndBackIsNotAShake(t *testing.T) {
	m := shakeOS(t)
	clock := time.Now()

	play(m, &clock, tea.MouseNone, []shakeMove{
		{x: 5, after: time.Second},
		{x: 30, after: 30 * time.Millisecond},
		{x: 60, after: 30 * time.Millisecond},
		{x: 85, after: 30 * time.Millisecond},
		{x: 60, after: 30 * time.Millisecond},
		{x: 30, after: 30 * time.Millisecond},
		{x: 5, after: 30 * time.Millisecond},
	})

	if m.SpotlightOn() {
		t.Error("crossing the screen and coming back toggled the beam")
	}
}

// TestTwoOvershootCorrectionsAreNotAShake is the case a total time window
// misses and the gap between reversals catches. Aiming at a target, overshooting
// and correcting is two reversals; doing it twice inside a second is four. The
// dwell where the hand picks the second target is what tells them apart.
func TestTwoOvershootCorrectionsAreNotAShake(t *testing.T) {
	m := shakeOS(t)
	clock := time.Now()

	play(m, &clock, tea.MouseNone, []shakeMove{
		{x: 10, after: time.Second},
		// Out to the first target, past it, and back.
		{x: 70, after: 40 * time.Millisecond},
		{x: 55, after: 40 * time.Millisecond},
		{x: 68, after: 40 * time.Millisecond},
		// The hand picks the next target.
		{x: 20, after: 400 * time.Millisecond},
		{x: 35, after: 40 * time.Millisecond},
		{x: 22, after: 40 * time.Millisecond},
	})

	if m.SpotlightOn() {
		t.Error("two ordinary target acquisitions inside a second toggled the beam; " +
			"the count is bounded by a total window rather than by the gap between turns")
	}
}

// TestAShakeWithAButtonHeldIsNotAShake is the suppression the whole gesture
// rests on. Every fast wide pointer movement a person makes while working has a
// button down: sizing a pane by its divider, moving a window, selecting text,
// dragging a rail row. This is the unit half; the end-to-end half drives real
// drags through the real handlers, in internal/input.
func TestAShakeWithAButtonHeldIsNotAShake(t *testing.T) {
	for _, button := range []tea.MouseButton{tea.MouseLeft, tea.MouseMiddle, tea.MouseRight} {
		m := shakeOS(t)
		clock := time.Now()
		play(m, &clock, button, wiggle(40, 20, 12, 60*time.Millisecond))
		if m.SpotlightOn() {
			t.Errorf("shaking with button %v held toggled the beam", button)
		}
	}
}

// TestAShakeCannotSpanAButtonPress is the other half of that suppression. A
// gesture half-made before a drag must not be completed by the motion after it,
// and the motion during the drag must not start one.
func TestAShakeCannotSpanAButtonPress(t *testing.T) {
	m := shakeOS(t)
	clock := time.Now()

	// Two turns with the button up.
	play(m, &clock, tea.MouseNone, wiggle(40, 20, 2, 60*time.Millisecond))
	// A drag.
	play(m, &clock, tea.MouseLeft, wiggle(40, 20, 6, 60*time.Millisecond))
	// Two more turns with the button up. Four in all, if the drag were not
	// there.
	play(m, &clock, tea.MouseNone, wiggle(40, 20, 2, 60*time.Millisecond))

	if m.SpotlightOn() {
		t.Error("a drag in the middle of it did not clear the turns counted either side")
	}
}

// TestTheGestureIsOffUntilItIsAskedFor. It ships off, so the shipped default
// must not fire on the sequence that does fire when it is on.
func TestTheGestureIsOffUntilItIsAskedFor(t *testing.T) {
	m := shakeOS(t)
	clock := time.Now()
	m.UserConfig.Spotlight.Shake = false

	play(m, &clock, tea.MouseNone, wiggle(40, 20, 12, 60*time.Millisecond))

	if m.SpotlightOn() {
		t.Error("the gesture fired with spotlight.shake off")
	}
	if config.DefaultConfig().Spotlight.ShakeToggles() {
		t.Error("spotlight.shake ships on; it is a gesture a person can make by accident")
	}
}

// TestOneShakeTogglesOnce protects the user from their own hand. Nobody stops
// moving on the reversal that crosses the threshold, so without a rest the tail
// of the same shake counts four more turns and puts the beam back where it was.
func TestOneShakeTogglesOnce(t *testing.T) {
	m := shakeOS(t)
	clock := time.Now()

	// A long vigorous shake: three times the turns the gesture needs.
	play(m, &clock, tea.MouseNone, wiggle(40, 20, 3*shakeReversalsToFire, 60*time.Millisecond))

	if !m.SpotlightOn() {
		t.Fatal("a long shake left the beam off; it fired an even number of times")
	}
}

// TestASecondShakeTogglesBack is the positive half of the one above. Resting
// the pointer ends the gesture, and the next shake must work.
func TestASecondShakeTogglesBack(t *testing.T) {
	m := shakeOS(t)
	clock := time.Now()

	play(m, &clock, tea.MouseNone, wiggle(40, 20, shakeReversalsToFire, 60*time.Millisecond))
	if !m.SpotlightOn() {
		t.Fatal("the first shake did not turn the beam on")
	}
	// The hand comes off the mouse, then shakes again.
	play(m, &clock, tea.MouseNone, wiggle(40, 20, shakeReversalsToFire, 60*time.Millisecond))
	if m.SpotlightOn() {
		t.Error("a second shake after a rest did not turn the beam off; " +
			"the detector never re-arms and the gesture works once a session")
	}
}

// TestTheScreenSaverEatsTheShake. The pointer movement that dismisses the saver
// is somebody coming back to the desk, and it must not also change what their
// screen looks like.
func TestTheScreenSaverEatsTheShake(t *testing.T) {
	m := shakeOS(t)
	clock := time.Now()
	m.screensaver.active = true

	play(m, &clock, tea.MouseNone, wiggle(40, 20, 12, 60*time.Millisecond))

	if m.SpotlightOn() {
		t.Error("a shake that woke the screen saver also toggled the beam")
	}
}

// TestTheDetectorAllocatesNothing. It runs on every motion event, and motion
// arrives one event per cell the pointer crosses.
func TestTheDetectorAllocatesNothing(t *testing.T) {
	m := shakeOS(t)
	now := time.Now()
	x := 40
	// Turns wide enough and slow enough to be counted and then dropped, so the
	// whole path runs on every call and the gesture never fires: the toggle
	// itself renders the config, which is not what this measures.
	if got := testing.AllocsPerRun(2000, func() {
		x = 100 - x
		now = now.Add(shakeMaxReversalGap + 10*time.Millisecond)
		m.noteShakeMotion(tea.MouseMotionMsg{X: x, Y: 5}, now)
	}); got != 0 {
		t.Errorf("the detector allocated %v times per motion event, want 0", got)
	}
}
