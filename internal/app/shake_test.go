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
