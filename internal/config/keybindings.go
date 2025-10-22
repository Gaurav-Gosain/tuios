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

// GetPrefixKeybindings returns keybindings for the prefix overlay
func GetPrefixKeybindings(prefixType string) []Keybinding {
	switch prefixType {
	case "workspace":
		return []Keybinding{
			{"1-9", "Switch to workspace"},
			{"Shift+1-9", "Move window to workspace"},
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
	default: // general prefix
		return []Keybinding{
			{"c", "Create window"},
			{"x", "Close window"},
			{",", "Rename window"},
			{"n", "Next window"},
			{"p", "Previous window"},
			{"0-9", "Jump to window"},
			{"w", "Workspace commands..."},
			{"m", "Minimize commands..."},
			{"t", "Window commands..."},
			{"d/Esc", "Detach (exit terminal)"},
			{"s", "Selection mode"},
			{"[", "Scrollback mode"},
			{"q", "Quit application"},
			{"?", "Toggle help"},
		}
	}
}

// GetKeybindings returns all keybinding sections for the help menu
func GetKeybindings() []KeybindingSection {
	return []KeybindingSection{
		{
			Title: "WINDOW MANAGEMENT",
			Bindings: []Keybinding{
				{"n", "New window"},
				{"x", "Close window"},
				{"r", "Rename window"},
				{"m", "Minimize window"},
				{"Shift+M", "Restore all"},
				{"Tab", "Next window"},
				{"Shift+Tab", "Previous window"},
				{"1-9", "Select window"},
			},
		},
		{
			Title: "WORKSPACES",
			Bindings: []Keybinding{
				{"%s+1-9", "Switch workspace"},        // %s will be replaced with modifier key
				{"%s+Shift+1-9", "Move window and follow"}, // %s will be replaced with modifier key
				{"Ctrl+B, w, 1-9", "Switch workspace (prefix)"},
				{"Ctrl+B, w, Shift+1-9", "Move window (prefix)"},
			},
		},
		{
			Title: "MODES",
			Bindings: []Keybinding{
				{"i, Enter", "Insert mode"},
				{"t", "Toggle tiling"},
				{"?", "Toggle help"},
			},
		},
		{
			Title:     "TILING:",
			Condition: "tiling",
			Bindings: []Keybinding{
				{"Shift+H/L, Ctrl+←/→", "Swap left/right"},
				{"Shift+K/J, Ctrl+↑/↓", "Swap up/down"},
			},
		},
		{
			Title:     "WINDOW SNAPPING:",
			Condition: "!tiling",
			Bindings: []Keybinding{
				{"h, l", "Snap left/right"},
				{"1-4", "Snap to corners"},
				{"f", "Fullscreen"},
				{"u", "Unsnap"},
			},
		},
		{
			Title: "TEXT SELECTION:",
			Bindings: []Keybinding{
				{"s", "Toggle selection mode"},
				{"Ctrl+S", "Toggle selection (from terminal)"},
				{"Single click", "Character selection"},
				{"Double click", "Word selection"},
				{"Triple click", "Line selection"},
				{"Mouse drag", "Select text (mouse)"},
				{"Arrow keys", "Move cursor"},
				{"Shift+Arrow", "Extend selection"},
				{"c", "Copy selected text"},
				{"Ctrl+V", "Paste from clipboard"},
				{"Esc", "Clear selection"},
			},
		},
		{
			Title: "SCROLLBACK:",
			Bindings: []Keybinding{
				{"Ctrl+B, [", "Enter scrollback mode"},
				{"Mouse wheel ↑", "Enter scrollback mode"},
				{"↑/↓, j/k", "Scroll up/down one line"},
				{"PgUp/PgDn", "Scroll half screen"},
				{"Ctrl+U/D", "Scroll half screen"},
				{"g, Home", "Go to oldest line"},
				{"G, End", "Go to newest line (exit)"},
				{"q, Esc", "Exit scrollback mode"},
			},
		},
		{
			Title: "WINDOW NAVIGATION:",
			Bindings: []Keybinding{
				{"Ctrl+↑/↓", "Swap/maximize windows"},
			},
		},
		{
			Title: "SYSTEM:",
			Bindings: []Keybinding{
				{"Ctrl+L", "Toggle log viewer"},
			},
		},
		{
			Title: "PREFIX (Ctrl+B) - Works in all modes:",
			Bindings: []Keybinding{
				{"c", "Create window"},
				{"x", "Close window"},
				{",/r", "Rename window"},
				{"n/Tab", "Next window"},
				{"p/Shift+Tab", "Previous window"},
				{"0-9", "Jump to window"},
				{"space", "Toggle tiling"},
				{"w", "Workspace commands"},
				{"m", "Minimize commands"},
				{"t", "Window commands"},
				{"d/Esc", "Detach from terminal"},
				{"s", "Toggle selection mode"},
				{"[", "Enter scrollback mode"},
				{"q", "Quit application"},
				{"Ctrl+B", "Send literal Ctrl+B"},
			},
		},
		{
			Title: "WINDOW PREFIX (Ctrl+B, t):",
			Bindings: []Keybinding{
				{"n", "New window"},
				{"x", "Close window"},
				{"r", "Rename window"},
				{"Tab/Shift+Tab", "Next/Previous window"},
				{"t", "Toggle tiling mode"},
			},
		},
		{
			Title: "",
			Bindings: []Keybinding{
				{"q, Ctrl+C", "Quit"},
			},
		},
	}
}
