package input

import (
	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

func handleQuickLookCopyKey(msg tea.KeyPressMsg, o *app.OS, cm *terminal.CopyMode, grid terminal.CopyGrid) (*app.OS, tea.Cmd) {
	fx := &copyModeEffects{}
	runCopyModeKey(msg, copyModeAction(msg, o), cm, grid, fx, &o.Settings)
	return fx.applyQuickLook(o)
}
