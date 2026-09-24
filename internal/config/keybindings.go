package config

// Keybinding represents a single keybinding entry
type Keybinding struct {
	Key         string
	Description string
}

// The prefix menu's agent lines. Named so IsAgentPrefixKeybinding can find
// them by what they say rather than by a key a config may have moved.
const (
	whichKeyInbox         = "Inbox"
	whichKeyOldestWaiting = "Oldest waiting (repeat: next)"
	whichKeyInboxMail     = "Inbox: mail"
	whichKeyReview        = "Review changes"
	whichKeyNewestDone    = "Newest finished (repeat: older)"
)

// IsAgentPrefixKeybinding reports whether a prefix menu line is one that only
// means something to a person running agents: the Inbox, the oldest waiting
// item, the Inbox on its mail, reviewing a pane's changes and the newest
// finished turn. The client leaves them out of the menu until an agent has
// been seen; the keys work either way.
func IsAgentPrefixKeybinding(k Keybinding) bool {
	switch k.Description {
	case whichKeyInbox, whichKeyOldestWaiting, whichKeyInboxMail, whichKeyReview, whichKeyNewestDone:
		return true
	}
	return false
}

// GetPrefixKeybindings returns keybindings for the prefix overlay.
// isDaemonSession indicates whether we're running in daemon mode (affects detach/quit descriptions).
func GetPrefixKeybindings(prefixType string, isDaemonSession ...bool) []Keybinding {
	daemonMode := len(isDaemonSession) > 0 && isDaemonSession[0]
	switch prefixType {
	case "workspace":
		return []Keybinding{
			{"1-9", "Switch to workspace"},
			{"Shift+1-9", "Move window to workspace"},
			{"r", "Rename workspace"},
			{"Esc", "Cancel"},
		}
	case "minimize":
		return []Keybinding{
			{"m", "Minimize focused window"},
			{"1-9", "Restore window"},
			{"Shift+M", "Restore all"},
			{"Esc", "Cancel"},
		}
	case "window":
		return []Keybinding{
			{"n", "New window"},
			{"x", "Close window"},
			{"r", "Rename window"},
			{"Tab", "Next window"},
			{"Shift+Tab", "Previous window"},
			{"t", "Toggle tiling mode"},
			{"Esc", "Cancel"},
		}
	case "debug":
		return []Keybinding{
			{"l", "Toggle log viewer"},
			{"c", "Toggle cache statistics"},
			{"k", "Toggle showkeys overlay"},
			{"a", "Toggle animations"},
			{"Esc", "Cancel"},
		}
	case "tape":
		return []Keybinding{
			{"m", "Open tape manager"},
			{"t", "Review project tape"},
			{"r", "Start recording"},
			{"s", "Stop recording"},
			{"Esc", "Cancel"},
		}
	case "layout":
		return []Keybinding{
			{"l", "Load layout"},
			{"s", "Save layout"},
			{"1-4", "Snap window to a corner"},
			{"5-9", "Resize focused window width (%)"},
			{"Shift+5-9", "Resize focused window height (%)"},
			{"Esc", "Cancel"},
		}
	default: // general prefix
		bindings := []Keybinding{
			{"c", "Create window"},
			{"x", "Close window"},
			{"r", "Rename window"},
			{",", "Settings"},
			{"k", "Keybind manager"},
			{"n", "Next window"},
			{"p", "Previous window"},
			// The arrows walk panes, and the prefix stays armed for a moment
			// so a run of them costs one prefix press. See the repeat window
			// in internal/input/prefix_repeat.go.
			{"←/→/↑/↓", "Focus pane in a direction"},
			{"a", "Launcher"},
			{"(/)", "Previous/next session"},
			{"0-9", "Jump to window"},
			{"z", "Toggle zoom"},
			{"space", "Toggle tiling"},
			{"-", "Split horizontal"},
			{"|/\\", "Split vertical"},
			{"R", "Rotate split"},
			{"=", "Equalize splits"},
			{"w", "Workspace commands..."},
			{"m", "Minimize commands..."},
			{"t", "Window commands..."},
			{"D", "Debug commands..."},
			{"T", "Tape manager..."},
			{"P", "Command palette"},
			{"S", "Session switcher"},
			{"W", "Workspace switcher"},
			{"L", "Layout commands..."},
			{"b", "Toggle sidebar"},
			{"e", "Focus/leave sidebar"},
			{"j", "Jump to newest message"},
			{"i", whichKeyInbox},
			{"o", whichKeyOldestWaiting},
			{"M", whichKeyInboxMail},
			{"O", whichKeyNewestDone},
			{"v", whichKeyReview},
			{"X", "Close session"},
		}

		// In daemon mode, d and Esc have different behaviors
		if daemonMode {
			bindings = append(bindings,
				Keybinding{"d", "Detach session"},
				Keybinding{"Esc", "Window mode"},
			)
		} else {
			// In local mode, both d and Esc do the same thing
			bindings = append(bindings,
				Keybinding{"d/Esc", "Window mode"},
			)
		}

		bindings = append(bindings,
			Keybinding{"[", "Scrollback mode"},
			Keybinding{"s", "Scrollback browser"},
			Keybinding{"C", "Take a screenshot"},
			Keybinding{"?", "Toggle help"},
		)

		// Quit description differs based on mode
		if daemonMode {
			bindings = append(bindings, Keybinding{"q", "Quit menu"})
		} else {
			bindings = append(bindings, Keybinding{"q", "Quit application"})
		}

		return bindings
	}
}
