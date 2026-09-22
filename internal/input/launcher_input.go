package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleLauncherInput handles keyboard input when the launcher is open.
//
// Enter and Tab are the two verbs on a row: Enter execs the program as the new
// pane's process, Tab opens a shell pane with the command line waiting to be
// added to. Tab rather than a modifier on Enter because a modified Enter is not
// expressible under the legacy keyboard encoding, and because Tab already means
// "give me the rest of this to finish".
func handleLauncherInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	switch msg.String() {
	case "esc":
		o.CloseLauncher()
		return o, nil

	case "enter":
		return o, o.LauncherRun(o.LauncherSelected)

	case "tab":
		return o, o.LauncherType(o.LauncherSelected)

	case "up", "ctrl+p":
		o.LauncherMove(-1)
		return o, o.LauncherIconWork()

	case "down", "ctrl+n":
		o.LauncherMove(1)
		return o, o.LauncherIconWork()
	}

	// A space is a legitimate character in a program name, so it types rather
	// than being swallowed as a key.
	changed, consumed := editFilterQuery(msg, &o.LauncherQuery, true)
	if !consumed {
		return o, nil
	}
	if changed {
		o.LauncherRefilter()
	}
	// Typing changes which rows are on screen, so it changes which icons are
	// wanted.
	return o, o.LauncherIconWork()
}
