package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
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
