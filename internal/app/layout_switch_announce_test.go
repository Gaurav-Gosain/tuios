package app

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The same rule TestWorkspaceSwitchSendsNoSpuriousWinch pins for a workspace
// switch, stated for a layout-mode switch: a pane whose drawable size the switch
// did not move must not be told a size, and a pane the switch did move must be
// told the settled one exactly once.
//
// A layout switch used to clear every pane's Tiled flag first, which announced a
// bordered box at the rectangle the pane still had, and then place the pane,
// which announced the real one. Re-selecting the mode a workspace was already in
// therefore repainted every full-screen guest on screen twice for nothing.

// switchLayoutMode selects a mode by name through the same entry points the
// keybindings use.
func switchLayoutMode(m *OS, mode string) {
	switch mode {
	case LayoutModeBSP:
		m.EnableBSPLayout()
	case LayoutModeMasterStack:
		m.EnableMasterStackLayout()
	default:
		m.EnableScrollingLayout()
	}
}

func TestLayoutModeSwitchAnnouncesEachSizeOnce(t *testing.T) {
	prevAnim, prevShared := config.Global.AnimationsEnabled, config.Global.SharedBorders
	config.Global.AnimationsEnabled = false
	t.Cleanup(func() {
		config.Global.AnimationsEnabled, config.Global.SharedBorders = prevAnim, prevShared
	})

	modes := []string{LayoutModeBSP, LayoutModeMasterStack, LayoutModeScrolling}
	for _, shared := range []bool{false, true} {
		for _, from := range modes {
			for _, to := range modes {
				t.Run(fmt.Sprintf("shared=%v/%s->%s", shared, from, to), func(t *testing.T) {
					config.Global.SharedBorders = shared
					m, told := newSwitchOS(t, 200, 50, map[int]int{1: 3})
					switchLayoutMode(m, from)

					before, counts := drawableSizes(m), callCounts(told)
					switchLayoutMode(m, to)
					after := drawableSizes(m)

					for _, w := range m.Windows {
						fired := told[w.ID].calls - counts[w.ID]
						switch {
						case before[w.ID] == after[w.ID] && fired != 0:
							t.Errorf("%s kept its drawable %dx%d yet its PTY was told a size %d time(s)",
								w.ID, after[w.ID][0], after[w.ID][1], fired)
						case before[w.ID] != after[w.ID] && fired != 1:
							t.Errorf("%s moved from %dx%d to %dx%d and its PTY was told %d time(s), want 1",
								w.ID, before[w.ID][0], before[w.ID][1], after[w.ID][0], after[w.ID][1], fired)
						}
						if rec := told[w.ID]; rec.calls > 0 && (rec.w != after[w.ID][0] || rec.h != after[w.ID][1]) {
							t.Errorf("%s was last told %dx%d, it can draw in %dx%d",
								w.ID, rec.w, rec.h, after[w.ID][0], after[w.ID][1])
						}
					}
				})
			}
		}
	}
}
