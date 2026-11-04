package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleHelpModeKey routes the keys help mode owns. Everything else is
// swallowed: a letter typed while the mode is on is not text for the shell, and
// a leader chord in it is not a second mode. The keys are deliberately few and
// unmodified, because the mode exists to be read, not to be learned.
func handleHelpModeKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		o.ExitHelpMode()
	case "tab":
		o.HelpNext(1)
	case "shift+tab":
		o.HelpNext(-1)
	case "down":
		o.HelpDeeper()
	case "up":
		o.HelpShallower()
	case "left":
		o.HelpNext(-1)
	case "right":
		o.HelpNext(1)
	case "h":
		o.HelpToggleKeys()
	case "s":
		o.HelpToggleRail(false)
	case "S":
		o.HelpToggleRail(true)
	}
	return o, nil
}
