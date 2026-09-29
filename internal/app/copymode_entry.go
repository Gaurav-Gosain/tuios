package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// enterCopyMode puts one pane into copy mode with its cursor where
// appearance.selection.copy_entry says: on the pane's terminal cursor (the
// default, as tmux does), or in the middle row. In multi copy mode each pane
// goes through here, so each one starts on its own cursor.
func (m *OS) enterCopyMode(w *terminal.Window) {
	if m.Settings.CopyEntry == config.CopyEntryCenter {
		w.EnterCopyModeCentered()
		return
	}
	w.EnterCopyMode()
}
