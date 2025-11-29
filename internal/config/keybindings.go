package config

import "fmt"

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
	case "debug":
		return []Keybinding{
			{"l", "Toggle log viewer"},
			{"c", "Toggle cache statistics"},
			{"k", "Toggle showkeys overlay"},
			{"Esc", "Cancel"},
		}
	case "tape":
		return []Keybinding{
			{"m", "Open tape manager"},
			{"r", "Start recording"},
			{"s", "Stop recording"},
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
			{"D", "Debug commands..."},
			{"T", "Tape manager..."},
			{"d/Esc", "Detach (exit terminal)"},
			{"[", "Scrollback mode"},
			{"?", "Toggle help"},
			{"q", "Quit application"},
		}
	}
}

// GetKeybindings returns all keybinding sections for the help menu
// If registry is provided, it generates bindings dynamically from user config
// If registry is nil, it falls back to hard-coded defaults
func GetKeybindings(registry *KeybindRegistry) []KeybindingSection {
	// If no registry provided, use static defaults
	if registry == nil {
		return getDefaultKeybindings()
	}

	// Generate dynamic help from config
	sections := []KeybindingSection{}

	// Window Management section
	windowMgmt := KeybindingSection{
		Title:    "WINDOW MANAGEMENT",
		Bindings: []Keybinding{},
	}
	addBinding(&windowMgmt, registry, "new_window", "New window")
	addBinding(&windowMgmt, registry, "close_window", "Close window")
	addBinding(&windowMgmt, registry, "rename_window", "Rename window")
	addBinding(&windowMgmt, registry, "minimize_window", "Minimize window")
	addBinding(&windowMgmt, registry, "restore_all", "Restore all")
	addBinding(&windowMgmt, registry, "next_window", "Next window")
	addBinding(&windowMgmt, registry, "prev_window", "Previous window")
	if len(windowMgmt.Bindings) > 0 {
		sections = append(sections, windowMgmt)
	}

	// Workspaces section
	workspaces := KeybindingSection{
		Title:    "WORKSPACES",
		Bindings: []Keybinding{},
	}
	// Show all workspace switches (1-9)
	for i := 1; i <= 9; i++ {
		actionSwitch := fmt.Sprintf("switch_workspace_%d", i)
		descSwitch := fmt.Sprintf("Switch to workspace %d", i)
		addBinding(&workspaces, registry, actionSwitch, descSwitch)
	}
	// Show all move and follow (1-9)
	for i := 1; i <= 9; i++ {
		actionMove := fmt.Sprintf("move_and_follow_%d", i)
		descMove := fmt.Sprintf("Move to workspace %d and follow", i)
		addBinding(&workspaces, registry, actionMove, descMove)
	}
	if len(workspaces.Bindings) > 0 {
		sections = append(sections, workspaces)
	}

	// Modes section
	modes := KeybindingSection{
		Title:    "MODES",
		Bindings: []Keybinding{},
	}
	addBinding(&modes, registry, "enter_terminal_mode", "Insert mode")
	addBinding(&modes, registry, "toggle_tiling", "Toggle tiling")
	addBinding(&modes, registry, "toggle_help", "Toggle help")
	if len(modes.Bindings) > 0 {
		sections = append(sections, modes)
	}

	// Return the rest as static for now (tiling, selection, etc.)
	sections = append(sections, getStaticHelpSections()...)
	return sections
}

// addBinding adds a keybinding to a section if the action has keys configured
func addBinding(section *KeybindingSection, registry *KeybindRegistry, action, description string) {
	keys := registry.GetKeysForDisplay(action)
	if keys != "" {
		section.Bindings = append(section.Bindings, Keybinding{
			Key:         keys,
			Description: description,
		})
	}
}

// getDefaultKeybindings returns the original hard-coded keybindings (used as fallback)
func getDefaultKeybindings() []KeybindingSection {
	sections := []KeybindingSection{
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
				{"%s+1-9", "Switch workspace"},             // %s will be replaced with modifier key
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
	}
	sections = append(sections, getStaticHelpSections()...)
	return sections
}

// getStaticHelpSections returns help sections that don't need dynamic binding info
// (mouse actions, special modes, etc.)
func getStaticHelpSections() []KeybindingSection {
	return []KeybindingSection{
		{
			Title:     "TILING:",
			Condition: "tiling",
			Bindings: []Keybinding{
				{"Shift+H/L, Ctrl+←/→", "Swap left/right"},
				{"Shift+K/J, Ctrl+↑/↓", "Swap up/down"},
				{"< / >", "Resize master width"},
				{"{ / }", "Resize focused window height"},
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
			Title: "TAPE PREFIX (Ctrl+B, T):",
			Bindings: []Keybinding{
				{"m", "Open tape manager"},
				{"r", "Start recording"},
				{"s", "Stop recording"},
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
