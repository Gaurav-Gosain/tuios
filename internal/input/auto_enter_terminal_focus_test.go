package input

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// twoPaneWM is two tiled panes in window-management mode, with the real
// keybind registry, so Tab / 1-9 / Alt+arrows travel the same HandleKeyPress
// path cmd/tuios registers.
func twoPaneWM(t *testing.T) *app.OS {
	t.Helper()
	o := osWithBindings(t, func(*config.KeybindingsConfig) {})
	o.Width, o.Height = 120, 40
	o.AutoTiling = true
	ws := o.CurrentWorkspace
	o.Windows = []*terminal.Window{
		{ID: "a", X: 0, Y: 0, Width: 60, Height: 40, Workspace: ws},
		{ID: "b", X: 60, Y: 0, Width: 60, Height: 40, Workspace: ws},
	}
	o.FocusedWindow = 0
	o.Mode = app.WindowManagementMode
	return o
}
