package config

// Keybinding represents a single keybinding entry
type Keybinding struct {
	Key         string
	Description string
}

// KeybindingSection represents a section of related keybindings
type KeybindingSection struct {
	Title     string
	Condition string // Empty for always shown, "tiling" for tiling mode, "!tiling" for non-tiling
	Bindings  []Keybinding
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
