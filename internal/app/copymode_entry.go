package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

func (m *OS) enterCopyMode(w *terminal.Window) {
	if m.Settings.CopyEntry == config.CopyEntryCenter {
		w.EnterCopyModeCentered()
		return
	}
	w.EnterCopyMode()
}

// EnterCopyModeSearch enters copy mode and opens its search prompt in one step:
// a forward search (/) when backward is false, a backward search (?) when it
// is true. It is tmux's "copy-mode \; send-keys ?" as one action.
//
// A pane already in copy mode stays where it is and only opens the prompt. In
// multi copy mode the prompt opens in every pane of the mode, the same as the
// / or ? key does there.
func (m *OS) EnterCopyModeSearch(backward bool) {
	fw := m.GetFocusedWindow()
	if fw == nil {
		return
	}
	if !fw.CopyModeVisible() {
		m.EnterCopyModeFocused()
		fw = m.GetFocusedWindow()
		if fw == nil || !fw.InCopyMode() {
			return
		}
	}
	panes := []*terminal.Window{fw}
	if m.MultiCopy.Has(fw.ID) {
		panes = m.MultiCopyWindows()
	}
	for _, w := range panes {
		if !w.InCopyMode() {
			continue
		}
		w.CopyMode.BeginSearch(backward, w.ScrollbackLenSync())
		w.InvalidateCache()
	}
}
