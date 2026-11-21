package input

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

func copyPipeKey(msg tea.KeyPressMsg, o *app.OS, cm *terminal.CopyMode) (config.CopyPipeBinding, bool) {
	if o == nil || o.UserConfig == nil || cm == nil || len(o.UserConfig.Keybindings.CopyPipe) == 0 {
		return config.CopyPipeBinding{}, false
	}
	if cm.State == terminal.CopyModeSearch || waitingForChar(cm) {
		return config.CopyPipeBinding{}, false
	}
	return o.UserConfig.Keybindings.CopyPipeFor(commandKey(msg))
}

func copyPipeYank(o *app.OS, window *terminal.Window, pipe config.CopyPipeBinding) (*app.OS, tea.Cmd) {
	cm := window.CopyMode
	cm.PendingCount = 0
	d := o.Settings.NotificationDuration
	text := selectionText(window)
	if text == "" {
		o.ShowNotification(fmt.Sprintf("No text is selected. Press v or V to select, then press %s.", pipe.Key), "warning", d)
		return o, nil
	}
	o.NoteCopyFlash(window)
	cm.State = terminal.CopyModeNormal
	window.InvalidateCache()
	if pipe.Cancel {
		window.ExitCopyMode()
	}
	o.ShowNotification(fmt.Sprintf("Running %s.", pipe.Label()), "info", d)
	return o, o.PipeYank(text, pipe.Command, pipe.Label())
}
