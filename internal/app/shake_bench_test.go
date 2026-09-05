package app

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// BenchmarkShakeDetector is the gesture's whole cost, on the sample sequence
// that reaches every branch bar the fire.
//
// It runs on an event that has already arrived and already composes a frame:
// the same motion event costs about 1.5 ms and 1,666 allocations on a nine-pane
// screen, measured with the gesture off and with it on, and the two are the same
// number. This is what the gesture adds to that.
func BenchmarkShakeDetector(b *testing.B) {
	m := benchOS(b, 9)
	m.UserConfig = config.DefaultConfig()
	m.UserConfig.Spotlight.Shake = true
	m.Settings = config.DefaultSettings()
	now := time.Now()
	x := 40
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		x = 100 - x
		now = now.Add(shakeMaxReversalGap + time.Millisecond)
		m.noteShakeMotion(tea.MouseMotionMsg{X: x, Y: 5}, now)
	}
}
