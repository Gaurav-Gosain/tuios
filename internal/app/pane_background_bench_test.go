package app

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// BenchmarkPaneBackground is the keystroke frame and the compositor with the
// pane background off and on, so the cost of the option is read off one run.
// "off" is the number the rest of the render benchmarks already hold; "on" is
// what painting adds. panes-1 is a lone pane that fills the screen, which with
// the option off takes the fullscreen fast path and with it on is composed.
func BenchmarkPaneBackground(b *testing.B) {
	for _, setting := range []string{config.PaneBackgroundOff, "#1e1e2e"} {
		name := "off"
		if setting != config.PaneBackgroundOff {
			name = "on"
		}
		for _, n := range []int{1, 4, 9} {
			b.Run(fmt.Sprintf("%s/keystroke/panes-%d", name, n), func(b *testing.B) {
				m := keystrokeOS(b, n, realCols, realRows)
				m.Settings.PaneBackground = setting
				if n == 1 {
					// The shape the fullscreen fast path takes: one pane over
					// the whole usable region, nothing beside it.
					m.Settings.SidebarEnabled = false
					w := m.Windows[0]
					w.X, w.Y, w.Width, w.Height = 0, m.GetTopMargin(), m.GetRenderWidth(), m.GetUsableHeight()
					w.MarkPositionDirty()
					if _, ok := m.fullscreenFastWindow(); ok != (setting == config.PaneBackgroundOff) {
						b.Fatalf("fast path eligibility %v with pane background %q", ok, setting)
					}
				}
				sink := newFrameSink(realCols, realRows)
				for _, w := range m.Windows {
					w.MarkContentDirty()
				}
				sink.emit(m.composeFrame())
				focused := m.Windows[0]
				i := 0
				b.ReportAllocs()
				for b.Loop() {
					focused.LockIO()
					_, _ = focused.Terminal.Write(fmt.Appendf(nil, "\x1b[2;3H%c", 'a'+byte(i%26)))
					focused.UnlockIO()
					focused.MarkContentDirty()
					sink.emit(m.composeFrame())
					i++
				}
			})
		}
		for _, n := range []int{1, 4, 9} {
			b.Run(fmt.Sprintf("%s/compositor-all-dirty/windows-%d", name, n), func(b *testing.B) {
				m := benchOS(b, n)
				m.Settings.PaneBackground = setting
				b.ReportAllocs()
				for b.Loop() {
					for _, w := range m.Windows {
						w.MarkContentDirty()
					}
					_ = m.GetCanvas(false)
				}
			})
		}
	}
}
