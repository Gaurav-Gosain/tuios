package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

func TestNoStaleSnapSurvivesALayoutChange(t *testing.T) {
	prevAnim := config.Global.Motion
	config.Global.Motion = config.MotionNone
	t.Cleanup(func() { config.Global.Motion = prevAnim })

	changes := []struct {
		name  string
		apply func(*OS)
	}{
		{"tiling-off", func(m *OS) { m.ToggleAutoTiling() }},
		{"master-stack", func(m *OS) { m.EnableMasterStackLayout() }},
		{"bsp", func(m *OS) { m.EnableBSPLayout() }},
		{"workspace-switch", func(m *OS) { m.SwitchToWorkspace(2) }},
		{"host-shrink", func(m *OS) {
			m.Width, m.Height = 70, 24
			m.TileAllWindows()
		}},
		{"host-shrink-floating", func(m *OS) {
			m.ToggleAutoTiling()
			m.Width, m.Height = 70, 24
			m.ClampWindowsToView()
		}},
	}
	for _, c := range changes {
		t.Run(c.name, func(t *testing.T) {
			m, _ := newSwitchOS(t, 200, 50, map[int]int{1: 3, 2: 1})
			m.TileAllWindows()
			m.EnableScrollingLayout()
			m.ScrollingFocusLeft()
			if len(m.Animations) == 0 {
				m.ScrollingFocusRight()
			}
			if len(m.Animations) == 0 {
				t.Fatal("the scrolling layout queued no snap; this test has nothing to trample")
			}

			c.apply(m)
			m.UpdateAnimations()

			for _, w := range m.Windows {
				if w.Workspace != m.CurrentWorkspace || w.Minimized {
					continue
				}
				cw, ch := w.ContentWidth(), w.ContentHeight()
				if ew, eh := w.Terminal.Width(), w.Terminal.Height(); ew != cw || eh != ch {
					t.Errorf("%s emulator %dx%d, drawable %dx%d", w.ID, ew, eh, cw, ch)
				}
				if aw, ah := w.AnnouncedSize(); aw != cw || ah != ch {
					t.Errorf("%s announced %dx%d, drawable %dx%d", w.ID, aw, ah, cw, ch)
				}
			}
		})
	}
}
