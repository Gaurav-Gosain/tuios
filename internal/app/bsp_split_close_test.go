package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// splitOS is one tiled BSP pane on workspace 1, the state a fresh session with
// one window is in.
func splitOS(t *testing.T) *OS {
	t.Helper()
	prevAnim := config.Global.AnimationsEnabled
	config.Global.AnimationsEnabled = false
	t.Cleanup(func() { config.Global.AnimationsEnabled = prevAnim })

	m := &OS{
		Settings:         config.Global,
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		WorkspaceFocus:   make(map[int]int),
		Width:            200,
		Height:           60,
		EffectiveWidth:   200,
		EffectiveHeight:  60,
		AutoTiling:       true,
		UseBSPLayout:     true,
		Windows:          []*terminal.Window{{ID: "window-0001", Workspace: 1, Width: 200, Height: 60}},
		FocusedWindow:    0,
	}
	m.TileAllWindows()
	return m
}
