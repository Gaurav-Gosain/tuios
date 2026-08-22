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

	case "backspace":
		if len(o.LauncherQuery) > 0 {
			o.LauncherQuery = o.LauncherQuery[:len(o.LauncherQuery)-1]
			o.LauncherRefilter()
		}
		return o, o.LauncherIconWork()

	case "ctrl+u":
		o.LauncherQuery = ""
		o.LauncherRefilter()
		return o, o.LauncherIconWork()
	}

	// Accept printable characters. A space is a legitimate character in a
	// program name, so it types rather than being swallowed as a key.
	switch keyStr := msg.String(); {
	case keyStr == "space":
		o.LauncherQuery += " "
	case msg.Text != "":
		o.LauncherQuery += msg.Text
	case len(keyStr) == 1 && keyStr[0] >= 32 && keyStr[0] <= 126:
		o.LauncherQuery += keyStr
	default:
		return o, nil
	}
	o.LauncherRefilter()
	// Typing changes which rows are on screen, so it changes which icons are
	// wanted.
	return o, o.LauncherIconWork()
}

// isLauncherKey reports whether a key press is Alt+Space, the launcher's direct
// binding.
//
// Alt+Space is the chord desktop launchers have taught people to reach for, it
// is one a shell never wants as literal input (handleTerminalModeBinds already
// treats any Alt chord as reserved), and unlike Ctrl+Shift+P it is
// distinguishable under the legacy encoding as well as the Kitty one. The cost
// is readline's set-mark, which is the same kind of trade Ctrl+P already makes
// against fish's history-back.
//
// Matching is on the decoded key event rather than msg.String() for the reason
// isCtrlP documents: the stringified key varies with the terminal's reporting
// mode, and a raw-string comparison silently misses.
func isLauncherKey(msg tea.KeyPressMsg) bool {
	if msg.Code != tea.KeySpace && msg.Code != ' ' {
		return false
	}
	mods := msg.Mod &^ (tea.ModCapsLock | tea.ModNumLock | tea.ModScrollLock)
	return mods == tea.ModAlt
}
