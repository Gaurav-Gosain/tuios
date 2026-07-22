package config

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// ValidationError represents a validation error or warning
type ValidationError struct {
	Field   string
	Key     string
	Message string
}

// ValidationResult contains all validation errors and warnings
type ValidationResult struct {
	Errors   []ValidationError
	Warnings []ValidationError
}

// HasErrors returns true if there are any errors
func (vr *ValidationResult) HasErrors() bool {
	return len(vr.Errors) > 0
}

// HasWarnings returns true if there are any warnings
func (vr *ValidationResult) HasWarnings() bool {
	return len(vr.Warnings) > 0
}

// ValidateConfig validates the user configuration
func ValidateConfig(cfg *UserConfig) *ValidationResult {
	result := &ValidationResult{
		Errors:   []ValidationError{},
		Warnings: []ValidationError{},
	}

	normalizer := NewKeyNormalizer()

	// Validate all keybinding sections
	validateSection := func(sectionName string, section map[string][]string) {
		for action, keys := range section {
			// Check if action has at least one key bound (warn if empty)
			if len(keys) == 0 {
				// Some actions might intentionally be unbound, so this is just a warning
				result.Warnings = append(result.Warnings, ValidationError{
					Field:   sectionName,
					Key:     action,
					Message: fmt.Sprintf("Action '%s' has no keybindings", action),
				})
				continue
			}

			// Validate each key
			for _, key := range keys {
				valid, errMsg := normalizer.ValidateKey(key)
				if !valid {
					result.Errors = append(result.Errors, ValidationError{
						Field:   sectionName,
						Key:     key,
						Message: errMsg,
					})
				}
			}
		}
	}

	// Validate leader key
	if cfg.Keybindings.LeaderKey != "" {
		valid, errMsg := normalizer.ValidateKey(cfg.Keybindings.LeaderKey)
		if !valid {
			result.Errors = append(result.Errors, ValidationError{
				Field:   "keybindings",
				Key:     "leader_key",
				Message: errMsg,
			})
		}
	}

	// Validate all sections
	validateSection("window_management", cfg.Keybindings.WindowManagement)
	validateSection("workspaces", cfg.Keybindings.Workspaces)
	validateSection("layout", cfg.Keybindings.Layout)
	validateSection("mode_control", cfg.Keybindings.ModeControl)
	validateSection("system", cfg.Keybindings.System)
	validateSection("navigation", cfg.Keybindings.Navigation)
	validateSection("restore_minimized", cfg.Keybindings.RestoreMinimized)
	validateSection("prefix_mode", cfg.Keybindings.PrefixMode)
	validateSection("window_prefix", cfg.Keybindings.WindowPrefix)
	validateSection("minimize_prefix", cfg.Keybindings.MinimizePrefix)
	validateSection("workspace_prefix", cfg.Keybindings.WorkspacePrefix)
	validateSection("debug_prefix", cfg.Keybindings.DebugPrefix)
	validateSection("tape_prefix", cfg.Keybindings.TapePrefix)
	validateSection("terminal_mode", cfg.Keybindings.TerminalMode)

	// Validate enum appearance options (warn on unknown values; they fall back to defaults)
	validateAppearanceEnums(cfg, result)

	// Validate the tape section (warn on an unknown autorun mode)
	validateTapeConfig(cfg, result)

	// Check for keybinding conflicts (same key bound to multiple actions)
	conflicts := findConflicts(cfg, normalizer)
	for key, actions := range conflicts {
		// Only warn if the conflict is in the same mode/context
		// (keys in different modes can overlap, like 'n' in window mode vs terminal mode)
		result.Warnings = append(result.Warnings, ValidationError{
			Field:   "keybindings",
			Key:     key,
			Message: fmt.Sprintf("Key '%s' is bound to multiple actions: %s", key, strings.Join(actions, ", ")),
		})
	}

	// Check for essential actions that should have keybindings
	essentialActions := map[string]string{
		"new_window":          "window_management",
		"close_window":        "window_management",
		"enter_terminal_mode": "mode_control",
		"enter_window_mode":   "mode_control",
		"quit":                "mode_control",
	}

	for action, section := range essentialActions {
		if !hasKeybinding(cfg, section, action) {
			result.Warnings = append(result.Warnings, ValidationError{
				Field:   section,
				Key:     action,
				Message: fmt.Sprintf("Essential action '%s' has no keybinding - TUIOS may be difficult to use", action),
			})
		}
	}

	// On macOS, warn about using alt+ instead of opt+ for better UX
	if normalizer.IsMacOS() {
		checkMacOSAltUsage := func(sectionName string, section map[string][]string) {
			for action, keys := range section {
				for _, key := range keys {
					keyLower := strings.ToLower(strings.TrimSpace(key))
					// Warn if using alt+ (suggest opt+ instead for macOS consistency)
					if strings.HasPrefix(keyLower, "alt+") {
						result.Warnings = append(result.Warnings, ValidationError{
							Field:   sectionName,
							Key:     key,
							Message: fmt.Sprintf("Action '%s': On macOS, consider using 'opt+' instead of 'alt+' for consistency with your keyboard (⌥ Option key)", action),
						})
					}
				}
			}
		}

		// Check all sections for alt+ usage on macOS
		checkMacOSAltUsage("window_management", cfg.Keybindings.WindowManagement)
		checkMacOSAltUsage("workspaces", cfg.Keybindings.Workspaces)
		checkMacOSAltUsage("layout", cfg.Keybindings.Layout)
		checkMacOSAltUsage("mode_control", cfg.Keybindings.ModeControl)
		checkMacOSAltUsage("system", cfg.Keybindings.System)
		checkMacOSAltUsage("prefix_mode", cfg.Keybindings.PrefixMode)
		checkMacOSAltUsage("window_prefix", cfg.Keybindings.WindowPrefix)
		checkMacOSAltUsage("minimize_prefix", cfg.Keybindings.MinimizePrefix)
		checkMacOSAltUsage("workspace_prefix", cfg.Keybindings.WorkspacePrefix)
	}

	return result
}

// validateTapeConfig warns when tape.autorun holds a value outside its allowed
// set. An unknown value silently falls back to the safe default ("ask"), so a
// typo would otherwise go unnoticed. An empty value is left to the default.
func validateTapeConfig(cfg *UserConfig, result *ValidationResult) {
	value := cfg.Tape.Autorun
	if value == "" || slices.Contains(TapeAutorunModes, value) {
		return
	}
	result.Warnings = append(result.Warnings, ValidationError{
		Field:   "tape",
		Key:     "autorun",
		Message: fmt.Sprintf("'%s' is not a valid value (allowed: %s); falling back to default", value, strings.Join(TapeAutorunModes, ", ")),
	})
}

// validateAppearanceEnums warns when an enum appearance option holds a value
// outside its allowed set. Such values silently fall back to defaults, so a
// typo would otherwise go unnoticed. Empty values are left to the defaults.
func validateAppearanceEnums(cfg *UserConfig, result *ValidationResult) {
	checkEnum := func(key, value string, allowed []string) {
		if value == "" {
			return
		}
		if slices.Contains(allowed, value) {
			return
		}
		result.Warnings = append(result.Warnings, ValidationError{
			Field:   "appearance",
			Key:     key,
			Message: fmt.Sprintf("'%s' is not a valid value (allowed: %s); falling back to default", value, strings.Join(allowed, ", ")),
		})
	}

	checkEnum("border_style", cfg.Appearance.BorderStyle,
		[]string{"rounded", "normal", "thick", "double", "hidden", "block", "ascii", "outer-half-block", "inner-half-block"})
	checkEnum("dockbar_position", cfg.Appearance.DockbarPosition,
		[]string{"bottom", "top", "hidden"})
	checkEnum("whichkey_position", cfg.Appearance.WhichKeyPosition,
		[]string{"bottom-right", "bottom-left", "top-right", "top-left", "center"})
	checkEnum("window_title_position", cfg.Appearance.WindowTitlePosition,
		[]string{"bottom", "top", "hidden"})
	validateTitleFormat(cfg.Appearance.WindowTitleFormat, result)
}

// knownTitlePlaceholders are the placeholders FormatWindowTitle expands.
var knownTitlePlaceholders = []string{"{title}", "{index}", "{cwd}"}

// titlePlaceholderPattern matches anything written as a placeholder, so a typo
// like {name} can be reported instead of being rendered literally in the title.
var titlePlaceholderPattern = regexp.MustCompile(`\{[^{}]*\}`)

func validateTitleFormat(format string, result *ValidationResult) {
	for _, placeholder := range titlePlaceholderPattern.FindAllString(format, -1) {
		if slices.Contains(knownTitlePlaceholders, placeholder) {
			continue
		}
		result.Warnings = append(result.Warnings, ValidationError{
			Field: "appearance",
			Key:   "window_title_format",
			Message: fmt.Sprintf("'%s' is not a known placeholder (allowed: %s); it will be shown literally",
				placeholder, strings.Join(knownTitlePlaceholders, ", ")),
		})
	}
}

// findConflicts finds keys that are bound to multiple actions within the same context
func findConflicts(cfg *UserConfig, normalizer *KeyNormalizer) map[string][]string {
	// Define action groups by context - actions in different contexts can share keys
	tilingModeActions := []string{
		"select_window_1", "select_window_2", "select_window_3", "select_window_4",
		"select_window_5", "select_window_6", "select_window_7", "select_window_8", "select_window_9",
		"swap_left", "swap_right", "swap_up", "swap_down",
	}

	nonTilingModeActions := []string{
		"snap_corner_1", "snap_corner_2", "snap_corner_3", "snap_corner_4",
		"snap_left", "snap_right", "snap_fullscreen", "unsnap",
	}

	selectionModeActions := []string{
		"toggle_selection", "toggle_selection_term", "copy_selection", "paste_clipboard", "clear_selection",
	}

	// Create sets for quick lookup
	tilingSet := make(map[string]bool)
	for _, action := range tilingModeActions {
		tilingSet[action] = true
	}
	nonTilingSet := make(map[string]bool)
	for _, action := range nonTilingModeActions {
		nonTilingSet[action] = true
	}
	selectionSet := make(map[string]bool)
	for _, action := range selectionModeActions {
		selectionSet[action] = true
	}

	// Collect all keybindings
	allSections := []map[string][]string{
		cfg.Keybindings.WindowManagement,
		cfg.Keybindings.Workspaces,
		cfg.Keybindings.Layout,
		cfg.Keybindings.ModeControl,
		cfg.Keybindings.System,
		cfg.Keybindings.Navigation,
	}

	// Map keys to actions within each context
	keyToActions := make(map[string][]string)

	for _, section := range allSections {
		for action, keys := range section {
			expandedKeys := normalizer.ExpandKeys(keys)
			for _, key := range expandedKeys {
				// Normalize keys, but preserve case for single letters (M vs m are different keys)
				normalizedKey := normalizeKeyForConflictDetection(key)
				keyToActions[normalizedKey] = append(keyToActions[normalizedKey], action)
			}
		}
	}

	// Find conflicts - only warn if actions are in the same context
	conflicts := make(map[string][]string)
	for key, actions := range keyToActions {
		if len(actions) > 1 {
			// Remove duplicates
			uniqueActions := make(map[string]bool)
			for _, action := range actions {
				uniqueActions[action] = true
			}

			// Check if any actions conflict (are in the same context)
			var conflictingActions []string
			for action := range uniqueActions {
				// Determine action's context
				contexts := []bool{
					tilingSet[action],
					nonTilingSet[action],
					selectionSet[action],
				}

				// If action is in a specific context, check for conflicts with other actions in same context
				for otherAction := range uniqueActions {
					if action == otherAction {
						continue
					}

					// Check if both are in the same context (not counting "always active" actions)
					inTiling := tilingSet[action] && tilingSet[otherAction]
					inNonTiling := nonTilingSet[action] && nonTilingSet[otherAction]
					inSelection := selectionSet[action] && selectionSet[otherAction]

					// Both are "always active" (not in any specific context)
					bothAlwaysActive := !contexts[0] && !contexts[1] && !contexts[2] &&
						!tilingSet[otherAction] && !nonTilingSet[otherAction] && !selectionSet[otherAction]

					if inTiling || inNonTiling || inSelection || bothAlwaysActive {
						// Real conflict - same context
						conflictingActions = append(conflictingActions, action)
						break
					}
				}
			}

			// Only add to conflicts if we found real conflicts
			if len(conflictingActions) > 0 {
				var actionList []string
				for action := range uniqueActions {
					actionList = append(actionList, action)
				}
				conflicts[key] = actionList
			}
		}
	}

	return conflicts
}

// normalizeKeyForConflictDetection normalizes keys for conflict detection
// Preserves case for single letters (M vs m are different keys in Bubbletea)
// Lowercases everything else for consistent comparison
func normalizeKeyForConflictDetection(key string) string {
	trimmed := strings.TrimSpace(key)

	// If it's a single letter, preserve case (M and m are different keys)
	if len(trimmed) == 1 && ((trimmed[0] >= 'a' && trimmed[0] <= 'z') || (trimmed[0] >= 'A' && trimmed[0] <= 'Z')) {
		return trimmed
	}

	// For everything else (ctrl+m, shift+tab, etc.), normalize to lowercase
	return strings.ToLower(trimmed)
}

// hasKeybinding checks if an action has at least one keybinding in a specific section
func hasKeybinding(cfg *UserConfig, sectionName, action string) bool {
	var section map[string][]string

	switch sectionName {
	case "window_management":
		section = cfg.Keybindings.WindowManagement
	case "workspaces":
		section = cfg.Keybindings.Workspaces
	case "layout":
		section = cfg.Keybindings.Layout
	case "mode_control":
		section = cfg.Keybindings.ModeControl
	case "system":
		section = cfg.Keybindings.System
	case "prefix_mode":
		section = cfg.Keybindings.PrefixMode
	case "window_prefix":
		section = cfg.Keybindings.WindowPrefix
	case "minimize_prefix":
		section = cfg.Keybindings.MinimizePrefix
	case "workspace_prefix":
		section = cfg.Keybindings.WorkspacePrefix
	default:
		return false
	}

	if keys, ok := section[action]; ok && len(keys) > 0 {
		return true
	}

	return false
}
