package config

import (
	"strings"
)

// KeybindRegistry manages the mapping between keys and actions
type KeybindRegistry struct {
	keyToAction map[string]string // Maps key string to action name
	config      *UserConfig
	normalizer  *KeyNormalizer
}

// NewKeybindRegistry creates a new keybind registry from config
func NewKeybindRegistry(cfg *UserConfig) *KeybindRegistry {
	registry := &KeybindRegistry{
		keyToAction: make(map[string]string),
		config:      cfg,
		normalizer:  NewKeyNormalizer(),
	}
	registry.buildMappings()
	return registry
}

// buildMappings builds the reverse mapping from keys to actions
func (r *KeybindRegistry) buildMappings() {
	r.keyToAction = make(map[string]string)

	// Build mappings for normal mode sections
	// Note: Prefix sections (PrefixMode, WindowPrefix, MinimizePrefix, WorkspacePrefix)
	// are NOT added here to avoid conflicts with normal mode keybindings
	// They will be looked up directly when in their respective prefix modes
	r.addSection(r.config.Keybindings.WindowManagement)
	r.addSection(r.config.Keybindings.Workspaces)
	r.addSection(r.config.Keybindings.Layout)
	r.addSection(r.config.Keybindings.ModeControl)
	r.addSection(r.config.Keybindings.System)
	r.addSection(r.config.Keybindings.Navigation)
	r.addSection(r.config.Keybindings.RestoreMinimized)
	// Prefix sections are handled separately - don't add them to the main registry:
	// - PrefixMode (used after Ctrl+B)
	// - WindowPrefix (used after Ctrl+B, t)
	// - MinimizePrefix (used after Ctrl+B, m)
	// - WorkspacePrefix (used after Ctrl+B, w)
}

// addSection adds all keybindings from a section to the registry
// Uses the key normalizer to expand platform-specific key variants
func (r *KeybindRegistry) addSection(section map[string][]string) {
	for action, keys := range section {
		// Expand keys using the normalizer (handles opt+N → unicode on macOS)
		expandedKeys := r.normalizer.ExpandKeys(keys)
		for _, key := range expandedKeys {
			// Store keys exactly as normalized (preserves case for single letters)
			// Don't lowercase here - we need case sensitivity for M vs m, etc.
			r.keyToAction[key] = action
		}
	}
}

// GetAction returns the action name for a given key in normal mode
func (r *KeybindRegistry) GetAction(key string) string {
	return r.lookupKey(key, r.keyToAction)
}

// GetPrefixAction returns the action name for a given key in the main prefix mode (Ctrl+B)
func (r *KeybindRegistry) GetPrefixAction(key string) string {
	return r.lookupKeyInSection(key, r.config.Keybindings.PrefixMode)
}

// GetWindowPrefixAction returns the action name for a given key in window prefix mode (Ctrl+B, t)
func (r *KeybindRegistry) GetWindowPrefixAction(key string) string {
	return r.lookupKeyInSection(key, r.config.Keybindings.WindowPrefix)
}

// GetMinimizePrefixAction returns the action name for a given key in minimize prefix mode (Ctrl+B, m)
func (r *KeybindRegistry) GetMinimizePrefixAction(key string) string {
	return r.lookupKeyInSection(key, r.config.Keybindings.MinimizePrefix)
}

// GetWorkspacePrefixAction returns the action name for a given key in workspace prefix mode (Ctrl+B, w)
func (r *KeybindRegistry) GetWorkspacePrefixAction(key string) string {
	return r.lookupKeyInSection(key, r.config.Keybindings.WorkspacePrefix)
}

// GetDebugPrefixAction returns the action name for a given key in debug prefix mode (Ctrl+B, D)
func (r *KeybindRegistry) GetDebugPrefixAction(key string) string {
	return r.lookupKeyInSection(key, r.config.Keybindings.DebugPrefix)
}

// GetTapePrefixAction returns the action name for a given key in tape prefix mode (Ctrl+B, T)
func (r *KeybindRegistry) GetTapePrefixAction(key string) string {
	return r.lookupKeyInSection(key, r.config.Keybindings.TapePrefix)
}

// lookupKeyInSection looks up a key in a specific config section
func (r *KeybindRegistry) lookupKeyInSection(key string, section map[string][]string) string {
	// Build a temporary map for this section
	tempMap := make(map[string]string)
	for action, keys := range section {
		expandedKeys := r.normalizer.ExpandKeys(keys)
		for _, k := range expandedKeys {
			tempMap[k] = action
		}
	}
	return r.lookupKey(key, tempMap)
}

// lookupKey performs the actual key lookup with case handling
func (r *KeybindRegistry) lookupKey(key string, keyMap map[string]string) string {
	// Trim whitespace but preserve case for single letters
	// This is important for distinguishing m vs M (shift+m)
	key = strings.TrimSpace(key)

	// For single letters, preserve case exactly as received
	// For compound keys (ctrl+x, shift+tab), normalize to lowercase
	if len(key) == 1 && ((key[0] >= 'a' && key[0] <= 'z') || (key[0] >= 'A' && key[0] <= 'Z')) {
		// Single letter - check both exact case and lowercase
		// This handles both "M" (shift+m) and "m" inputs
		if action, ok := keyMap[key]; ok {
			return action
		}
		// Fallback to lowercase for compatibility
		return keyMap[strings.ToLower(key)]
	}

	// For everything else (compound keys), normalize to lowercase
	normalizedKey := strings.ToLower(key)
	return keyMap[normalizedKey]
}

// GetKeys returns all keys bound to a given action
func (r *KeybindRegistry) GetKeys(action string) []string {
	// Search through all sections
	sections := []map[string][]string{
		r.config.Keybindings.WindowManagement,
		r.config.Keybindings.Workspaces,
		r.config.Keybindings.Layout,
		r.config.Keybindings.ModeControl,
		r.config.Keybindings.System,
		r.config.Keybindings.Navigation,
		r.config.Keybindings.RestoreMinimized,
		r.config.Keybindings.PrefixMode,
		r.config.Keybindings.WindowPrefix,
		r.config.Keybindings.MinimizePrefix,
		r.config.Keybindings.WorkspacePrefix,
		r.config.Keybindings.TerminalMode,
	}

	for _, section := range sections {
		if keys, ok := section[action]; ok {
			return keys
		}
	}
	return nil
}

// GetKeysForDisplay returns a formatted string of keys for display in help
func (r *KeybindRegistry) GetKeysForDisplay(action string) string {
	keys := r.GetKeys(action)
	if len(keys) == 0 {
		return ""
	}
	// Show first 2 keys if multiple bindings exist
	if len(keys) > 2 {
		return strings.Join(keys[:2], ", ") + ", ..."
	}
	return strings.Join(keys, ", ")
}

// HasAction checks if an action exists in the registry
func (r *KeybindRegistry) HasAction(action string) bool {
	return len(r.GetKeys(action)) > 0
}

// Reload reloads the keybind mappings from the config
func (r *KeybindRegistry) Reload(cfg *UserConfig) {
	r.config = cfg
	r.buildMappings()
}

// GetConfig returns the underlying config
func (r *KeybindRegistry) GetConfig() *UserConfig {
	return r.config
}

// IsActionExplicitlyConfigured checks if an action has explicit keybindings in the user config
// (not just auto-filled defaults). This is used to determine if hard-coded fallbacks should be disabled.
func (r *KeybindRegistry) IsActionExplicitlyConfigured(action string) bool {
	// Check if the action exists in any section of the user's config
	// If it does, it means the user explicitly configured it (even if to empty keys)

	// Map action names to their sections
	actionToSection := map[string]string{
		"new_window":      "window_management",
		"close_window":    "window_management",
		"rename_window":   "window_management",
		"minimize_window": "window_management",
		"restore_all":     "window_management",
		"next_window":     "window_management",
		"prev_window":     "window_management",
		// Add more as needed
	}

	section := actionToSection[action]
	if section == "" {
		return false
	}

	// Check if the section exists in the config and has this action
	var sectionMap map[string][]string
	switch section {
	case "window_management":
		sectionMap = r.config.Keybindings.WindowManagement
	case "workspaces":
		sectionMap = r.config.Keybindings.Workspaces
	case "layout":
		sectionMap = r.config.Keybindings.Layout
	case "mode_control":
		sectionMap = r.config.Keybindings.ModeControl
	case "system":
		sectionMap = r.config.Keybindings.System
	default:
		return false
	}

	_, exists := sectionMap[action]
	return exists
}

// ActionDescriptions maps action names to their descriptions for help menu generation.
var ActionDescriptions = map[string]string{
	// Window Management
	"new_window":      "New window",
	"close_window":    "Close window",
	"rename_window":   "Rename window",
	"minimize_window": "Minimize window",
	"restore_all":     "Restore all minimized",
	"next_window":     "Next window",
	"prev_window":     "Previous window",
	"select_window_1": "Select window 1",
	"select_window_2": "Select window 2",
	"select_window_3": "Select window 3",
	"select_window_4": "Select window 4",
	"select_window_5": "Select window 5",
	"select_window_6": "Select window 6",
	"select_window_7": "Select window 7",
	"select_window_8": "Select window 8",
	"select_window_9": "Select window 9",

	// Workspaces
	"switch_workspace_1": "Switch to workspace 1",
	"switch_workspace_2": "Switch to workspace 2",
	"switch_workspace_3": "Switch to workspace 3",
	"switch_workspace_4": "Switch to workspace 4",
	"switch_workspace_5": "Switch to workspace 5",
	"switch_workspace_6": "Switch to workspace 6",
	"switch_workspace_7": "Switch to workspace 7",
	"switch_workspace_8": "Switch to workspace 8",
	"switch_workspace_9": "Switch to workspace 9",
	"move_and_follow_1":  "Move to workspace 1 and follow",
	"move_and_follow_2":  "Move to workspace 2 and follow",
	"move_and_follow_3":  "Move to workspace 3 and follow",
	"move_and_follow_4":  "Move to workspace 4 and follow",
	"move_and_follow_5":  "Move to workspace 5 and follow",
	"move_and_follow_6":  "Move to workspace 6 and follow",
	"move_and_follow_7":  "Move to workspace 7 and follow",
	"move_and_follow_8":  "Move to workspace 8 and follow",
	"move_and_follow_9":  "Move to workspace 9 and follow",

	// Layout
	"snap_left":                 "Snap left",
	"snap_right":                "Snap right",
	"snap_fullscreen":           "Fullscreen",
	"unsnap":                    "Unsnap",
	"snap_corner_1":             "Snap to top-left",
	"snap_corner_2":             "Snap to top-right",
	"snap_corner_3":             "Snap to bottom-left",
	"snap_corner_4":             "Snap to bottom-right",
	"toggle_tiling":             "Toggle tiling mode",
	"swap_left":                 "Swap left",
	"swap_right":                "Swap right",
	"swap_up":                   "Swap up",
	"swap_down":                 "Swap down",
	"resize_master_shrink":      "Decrease master width",
	"resize_master_grow":        "Increase master width",
	"resize_height_shrink":      "Decrease focused window height",
	"resize_height_grow":        "Increase focused window height",
	"resize_master_shrink_left": "Decrease master width from left edge",
	"resize_master_grow_left":   "Increase master width from left edge",
	"resize_height_shrink_top":  "Decrease focused window height from top edge",
	"resize_height_grow_top":    "Increase focused window height from top edge",

	// BSP Tiling
	"split_horizontal": "Split window horizontally (top/bottom)",
	"split_vertical":   "Split window vertically (left/right)",
	"rotate_split":     "Rotate split direction",
	"equalize_splits":  "Equalize all split ratios",
	"preselect_left":   "Preselect left for next window",
	"preselect_right":  "Preselect right for next window",
	"preselect_up":     "Preselect up for next window",
	"preselect_down":   "Preselect down for next window",

	// Mode Control
	"enter_terminal_mode": "Enter terminal mode",
	"enter_window_mode":   "Enter window management mode",
	"toggle_help":         "Toggle help",
	"quit":                "Quit",

	// Clipboard
	"paste_clipboard": "Paste from clipboard",

	// System
	"toggle_logs":        "Toggle log viewer",
	"toggle_cache_stats": "Toggle cache statistics",
	"toggle_showkeys":    "Toggle showkeys overlay",

	// Prefix Mode
	"prefix_new_window":       "Create new window",
	"prefix_close_window":     "Close current window",
	"prefix_rename_window":    "Rename window",
	"prefix_next_window":      "Next window",
	"prefix_prev_window":      "Previous window",
	"prefix_select_0":         "Jump to window 0",
	"prefix_select_1":         "Jump to window 1",
	"prefix_select_2":         "Jump to window 2",
	"prefix_select_3":         "Jump to window 3",
	"prefix_select_4":         "Jump to window 4",
	"prefix_select_5":         "Jump to window 5",
	"prefix_select_6":         "Jump to window 6",
	"prefix_select_7":         "Jump to window 7",
	"prefix_select_8":         "Jump to window 8",
	"prefix_select_9":         "Jump to window 9",
	"prefix_toggle_tiling":    "Toggle tiling mode",
	"prefix_workspace":        "Enter workspace prefix",
	"prefix_minimize":         "Enter minimize prefix",
	"prefix_window":           "Enter window prefix",
	"prefix_detach":           "Detach (daemon: session keeps running)",
	"prefix_selection":        "Enter copy/scrollback mode",
	"prefix_help":             "Toggle help",
	"prefix_logs":             "Toggle log viewer",
	"prefix_debug":            "Enter debug prefix",
	"prefix_tape":             "Enter tape manager prefix",
	"prefix_quit":             "Quit (daemon: kills session)",
	"prefix_fullscreen":       "Fullscreen current window",
	"prefix_split_horizontal": "Split window horizontally",
	"prefix_split_vertical":   "Split window vertically",
	"prefix_rotate_split":     "Rotate split direction",
	"prefix_equalize_splits":  "Equalize all splits",

	// Tape Prefix
	"tape_prefix_manager": "Open tape manager",
	"tape_prefix_record":  "Start recording",
	"tape_prefix_stop":    "Stop recording",
	"tape_prefix_cancel":  "Cancel tape prefix",

	// Tape Actions
	"toggle_tape_manager": "Toggle tape manager",
	"stop_recording":      "Stop tape recording",

	// Debug Prefix
	"debug_prefix_logs":       "Toggle log viewer",
	"debug_prefix_cache":      "Toggle cache statistics",
	"debug_prefix_animations": "Toggle animations",
	"debug_prefix_cancel":     "Cancel debug prefix",

	// Terminal Mode (direct keybinds, no prefix required)
	"terminal_next_window": "Next window (terminal mode)",
	"terminal_prev_window": "Previous window (terminal mode)",
	"terminal_exit_mode":   "Exit terminal mode (to window mode)",
}
