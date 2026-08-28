package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// A snap animation owns its window's geometry until it finishes, and it
// deliberately leaves the emulator at the size the pane had when it started,
// catching up in one resize at the end. That is the whole reason anything else
// that writes geometry has to retire it first: left running, the next tick
// stamps the destination of a transition the user has already moved past, and
// the emulator is not resized with it. The pane then draws at one size while its
// guest writes at another, which is a shell wrapping its prompt at the wrong
// column for the rest of the session.
//
// The scrolling layout is where this bites, because it animates its slide even
// when animations are off - the viewport shift is disorienting without it - so
// there is always a snap in flight for the next action to trample.

func TestNoStaleSnapSurvivesALayoutChange(t *testing.T) {
	prevAnim := config.Global.AnimationsEnabled
	config.Global.AnimationsEnabled = false
	t.Cleanup(func() { config.Global.AnimationsEnabled = prevAnim })

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
			if len(m.Animations) == 0 {
				t.Fatal("the scrolling layout queued no snap; this test has nothing to trample")
			}

			c.apply(m)
			// The tick that used to stamp the old destination back.
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
