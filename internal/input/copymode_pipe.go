package input

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// Copy-pipe keys: a [[keybindings.copy_pipe]] entry yanks the selection and
// pipes it through its command. See app/copy_pipe.go for what the command
// gets and where the result goes, and config/copy_pipe.go for the entry.
//
// The entry takes its key before copy mode's own keys, so an entry on y
// replaces the plain yank. A search being typed and a pending f/t character
// still take every key as text.

// copyPipeKey returns the entry on msg's key, when copy mode in cm is ready
// for a command key.
func copyPipeKey(msg tea.KeyPressMsg, o *app.OS, cm *terminal.CopyMode) (config.CopyPipeBinding, bool) {
	if o == nil || o.UserConfig == nil || cm == nil || len(o.UserConfig.Keybindings.CopyPipe) == 0 {
		return config.CopyPipeBinding{}, false
	}
	if cm.State == terminal.CopyModeSearch || cm.PendingCharSearch {
		return config.CopyPipeBinding{}, false
	}
	return o.UserConfig.Keybindings.CopyPipeFor(commandKey(msg))
}

// copyPipeYank yanks one pane's selection through pipe. copy-pipe keeps copy
// mode and the cursor where they are and clears the selection. With cancel,
// it leaves copy mode, as q does.
func copyPipeYank(o *app.OS, window *terminal.Window, pipe config.CopyPipeBinding) (*app.OS, tea.Cmd) {
	cm := window.CopyMode
	cm.PendingCount = 0
	d := o.Settings.NotificationDuration
	text := selectionText(window)
	if text == "" {
		o.ShowNotification(fmt.Sprintf("No text is selected. Press v or V to select, then press %s.", pipe.Key), "warning", d)
		return o, nil
	}
	// The sweep is written down while the selection still holds its region.
	o.NoteCopyFlash(window)
	cm.State = terminal.CopyModeNormal
	window.InvalidateCache()
	if pipe.Cancel {
		window.ExitCopyMode()
	}
	o.ShowNotification(fmt.Sprintf("Running %s.", pipe.Label()), "info", d)
	return o, o.PipeYank(text, pipe.Command, pipe.Label())
}
