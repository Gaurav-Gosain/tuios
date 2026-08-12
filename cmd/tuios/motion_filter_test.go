package main

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// filterOS builds a model with the rail on the left and one pane beside it.
func filterOS(t *testing.T) *app.OS {
	t.Helper()
	pe, pp, pw := config.SidebarEnabled, config.SidebarPosition, config.SidebarWidth
	config.SidebarEnabled, config.SidebarPosition, config.SidebarWidth = true, "left", 30
	prevFFM := config.FocusFollowsMouse
	config.FocusFollowsMouse = false
	t.Cleanup(func() {
		config.SidebarEnabled, config.SidebarPosition, config.SidebarWidth = pe, pp, pw
		config.FocusFollowsMouse = prevFFM
	})

	cfg := config.DefaultConfig()
	o := app.NewOS(app.OSOptions{UserConfig: cfg, KeybindRegistry: config.NewKeybindRegistry(cfg)})
	o.Width, o.Height = 120, 40
	o.EffectiveWidth, o.EffectiveHeight = 120, 40
	o.Windows = []*terminal.Window{
		{ID: "aaaaaaaa1111", CustomName: "editor", X: 31, Y: 1, Width: 40, Height: 20, Workspace: 1},
	}
	o.CurrentWorkspace, o.FocusedWindow = 1, 0
	return o
}

// TestMotionFilterPassesRailHover is the second gate the rail's hover has to
// clear. The view asks the host for all-motion tracking so hover has events to
// work with; this whitelist decides which of them reach Update at all, and a
// motion it drops is a hover that never happens. Terminal mode is pinned
// alongside window management because that is where hover looked broken.
func TestMotionFilterPassesRailHover(t *testing.T) {
	for _, mode := range []struct {
		name string
		mode app.Mode
	}{
		{"window management", app.WindowManagementMode},
		{"terminal", app.TerminalMode},
	} {
		t.Run(mode.name, func(t *testing.T) {
			o := filterOS(t)
			o.Mode = mode.mode

			// Deep inside the rail band, well below the rows, where the footer
			// controls live.
			onRail := tea.MouseMotionMsg{X: 3, Y: 35}
			if filterMouseMotion(o, onRail) == nil {
				t.Error("motion over the rail was dropped; nothing downstream can hover")
			}

			// The pane keeps the CPU guard: a plain shell asked for no mouse
			// mode and tuios draws no hover out there, so that motion is noise.
			offRail := tea.MouseMotionMsg{X: 50, Y: 10}
			if filterMouseMotion(o, offRail) != nil {
				t.Error("motion over a plain pane was passed; the guard is what keeps a mouse sweep cheap")
			}
		})
	}
}
