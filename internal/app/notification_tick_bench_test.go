package app

import (
	"testing"
	"time"
)

// BenchmarkNotificationTick is what a live message costs the maintenance tick,
// one op per tick at the normal frame rate. Each op ages the message by one
// frame's worth of its life before ticking, so the rule burns down at the rate
// it does on screen, and a tick that composes is followed by the View that the
// program would call. When a message runs out, the next one is raised, so the
// run covers whole message lives. render/tick is the share of ticks that
// composed a frame.
func BenchmarkNotificationTick(b *testing.B) {
	m := idleOS(b, 3)
	frame := time.Second / 60
	show := func() {
		m.ShowNotification("attached to the session", "info", m.Settings.NotificationDuration)
	}
	show()
	m.Update(TickerMsg(time.Now()))
	m.View()

	_, _, before := m.TickStats()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if len(m.Notifications) == 0 {
			show()
		}
		m.Notifications[len(m.Notifications)-1].StartTime = m.Notifications[len(m.Notifications)-1].StartTime.Add(-frame)
		m.Update(TickerMsg(time.Now()))
		if !m.renderSkipped {
			m.View()
		}
	}
	b.StopTimer()

	_, _, render := m.TickStats()
	b.ReportMetric(float64(render-before)/float64(b.N), "render/tick")
}
