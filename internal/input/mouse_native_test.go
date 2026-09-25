package input

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// twoPaneOS builds an OS with two visible floating panes side by side.
func twoPaneOS(t *testing.T) *app.OS {
	t.Helper()
	cfg := config.DefaultConfig()
	o := app.NewOS(app.OSOptions{
		UserConfig:      cfg,
		KeybindRegistry: config.NewKeybindRegistry(cfg),
	})
	o.Width, o.Height = 120, 40
	o.EffectiveWidth, o.EffectiveHeight = 120, 40
	o.Windows = []*terminal.Window{
		{ID: "a", CustomName: "left", X: 0, Y: 0, Width: 50, Height: 30, Workspace: 1},
		{ID: "b", CustomName: "right", X: 55, Y: 0, Width: 50, Height: 30, Workspace: 1},
	}
	o.CurrentWorkspace, o.FocusedWindow = 1, 0
	return o
}
