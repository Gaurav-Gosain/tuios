package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// handleCopyModeSearchForward is copy_mode_search_forward: enter copy mode and
// open the / prompt in one key.
func handleCopyModeSearchForward(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.EnterCopyModeSearch(false)
	return o, nil
}

// handleCopyModeSearchBackward is copy_mode_search_backward: enter copy mode
// and open the ? prompt in one key, tmux's "copy-mode \; send-keys ?".
func handleCopyModeSearchBackward(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.EnterCopyModeSearch(true)
	return o, nil
}

// copyModeSearchActionKey runs copy_mode_search_forward or
// copy_mode_search_backward when the key is bound to one of them and the pane
// is in copy mode's normal state. It reads the sections a key reaches outside
// copy mode without a prefix: global, and terminal_mode or the window-mode
// section for the current mode. The prefix sections are not read, because
// copy mode takes the leader key itself (ctrl+b is page up there).
//
// A key typed into the search prompt, a visual selection, or the second key
// of f, t, gg or a count is copy mode's, so those states skip the lookup.
func copyModeSearchActionKey(msg tea.KeyPressMsg, o *app.OS, window *terminal.Window) (*app.OS, tea.Cmd, bool) {
	cm := window.CopyMode
	if cm == nil || cm.State != terminal.CopyModeNormal ||
		cm.PendingCharSearch || cm.PendingGCount || cm.PendingCount > 0 {
		return o, nil, false
	}
	isSearchAction := func(a string) bool {
		return a == config.ActionCopyModeSearchForward || a == config.ActionCopyModeSearchBackward
	}
	action := sectionAction(msg, o, (*config.KeybindRegistry).GetGlobalAction)
	if !isSearchAction(action) {
		if o.Mode == app.TerminalMode {
			action = sectionAction(msg, o, (*config.KeybindRegistry).GetTerminalModeAction)
		} else {
			action = sectionAction(msg, o, (*config.KeybindRegistry).GetAction)
		}
	}
	if !isSearchAction(action) {
		return o, nil, false
	}
	return dispatchAction(action, msg, o)
}
