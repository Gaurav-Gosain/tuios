package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

func sectionAction(msg tea.KeyPressMsg, o *app.OS, table config.Table) string {
	if o.KeybindRegistry == nil {
		return ""
	}
	return tableAction(o, msg, o.KeybindRegistry, table)
}

func handleGlobalBinds(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd, bool) {
	return dispatchAction(sectionAction(msg, o, config.TableGlobal), msg, o)
}
