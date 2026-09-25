package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// Zoom used to be two things at once: the pane takes the whole region, and it
// takes it in one frame. appearance.zoom_size and appearance.zoom_animation
// separate them.

// zoomPeekOS is four panes in a two by two split, which is the layout that makes
// the anchoring visible: each pane has a neighbour on exactly two sides.
func zoomPeekOS(t *testing.T) (*OS, []*terminal.Window) {
	t.Helper()
	prev := config.Global
	t.Cleanup(func() { config.Global = prev })

	var wins []*terminal.Window
	for i := range 4 {
		w := newTestWindow(t, string(rune('a'+i))+"0000000000000000000000000000000", 40, 20)
		w.Workspace = 1
		wins = append(wins, w)
	}
	// Top left, top right, bottom left, bottom right of a 120x40 region.
	wins[0].X, wins[0].Y, wins[0].Width, wins[0].Height = 0, 0, 60, 20
	wins[1].X, wins[1].Y, wins[1].Width, wins[1].Height = 60, 0, 60, 20
	wins[2].X, wins[2].Y, wins[2].Width, wins[2].Height = 0, 20, 60, 20
	wins[3].X, wins[3].Y, wins[3].Width, wins[3].Height = 60, 20, 60, 20

	m := &OS{
		Settings:         config.Global,
		Windows:          wins,
		FocusedWindow:    0,
		WorkspaceFocus:   map[int]int{},
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		Width:            120,
		Height:           40,
		PendingResizes:   map[string][2]int{},
	}
	return m, wins
}
