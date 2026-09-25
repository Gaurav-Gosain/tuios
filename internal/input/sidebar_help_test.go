package input

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// railOS builds an OS with the rail focused and one pane, routed through the
// real registry so the help the test reads is the help a user would get.
func railOS(t *testing.T) *app.OS {
	t.Helper()
	withSidebarGlobals(t, "left")
	o := osWithBindings(t, func(*config.KeybindingsConfig) {})
	o.Width, o.Height = 120, 40
	o.CurrentWorkspace = 1
	o.Windows = []*terminal.Window{{ID: "w1", X: 0, Y: 0, Width: 120, Height: 39, Workspace: 1}}
	o.FocusedWindow = 0
	o.EnterSidebarFocus()
	return o
}
