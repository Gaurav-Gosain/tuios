package input

import (
	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// handleQuickLookCopyKey runs a key in copy mode over the quick look preview,
// with the code that runs it over a pane. The preview holds no lock: its rows
// are fixed while copy mode runs.
func handleQuickLookCopyKey(msg tea.KeyPressMsg, o *app.OS, cm *terminal.CopyMode, grid terminal.CopyGrid) (*app.OS, tea.Cmd) {
	fx := &copyModeEffects{}
	runCopyModeKey(msg, copyModeAction(msg, o), cm, grid, fx, &o.Settings)
	return fx.applyQuickLook(o)
}
