package app

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// TestUsableHeightNeverGoesNegative pins the floor rather than the crash it
// caused. Every caller of GetUsableHeight reads it as an extent - a row count to
// tile inside, to clip against, to hit-test within - and a negative extent is
// nonsense in all of them; the render loop is only where it happened to be fatal.
func TestUsableHeightNeverGoesNegative(t *testing.T) {
	for _, pos := range []string{"bottom", "top", "hidden"} {
		prev := config.Global.DockbarPosition
		config.Global.DockbarPosition = pos
		for h := range config.DockHeight + 2 {
			m := newNarrowOS(t, 80, h)
			if got := m.GetUsableHeight(); got < 0 {
				t.Errorf("dock %s, host height %d: usable height %d", pos, h, got)
			}
		}
		config.Global.DockbarPosition = prev
	}
}

// TestFrameSurvivesAHostShorterThanTheDock renders at every host size from 0x0
// up through the dock's own height. A panic here is not a recovered frame: View
// runs inside bubbletea's frame loop, outside Update's recover, so it takes the
// process down and every pane with it.
//
// The pane is left floating above the top edge because that is the arrangement
// the fuzzer shrank to: tiling off keeps a pane at the rectangle a taller host
// gave it, so the shrunk viewport leaves it starting off-screen.
func TestFrameSurvivesAHostShorterThanTheDock(t *testing.T) {
	for w := range 3 {
		for h := range config.DockHeight + 2 {
			m := newNarrowOS(t, w, h)
			m.CurrentWorkspace = 1
			m.Windows = []*terminal.Window{
				{ID: "a", X: 0, Y: -8, Width: 40, Height: 20, Workspace: 1},
				{ID: "b", X: 0, Y: 0, Width: 40, Height: 20, Workspace: 1},
			}
			m.FocusedWindow = 0
			lipgloss.Sprint(m.GetCanvas(true).Render())
		}
	}
}
