package config

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// clockFormatSample renders a layout against a fixed time, so a warning can say
// what the user's layout actually produces rather than name the rule it broke.
// The reference instant is Go's own, which is what makes a layout a layout.
func clockFormatSample(format string) string {
	return time.Date(2006, 1, 2, 15, 4, 5, 0, time.UTC).Format(format)
}

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
		for _, keys := range section {
			// An action with an empty list is not a mistake to report: it is the
			// only way the file has of saying "I took this key away", and
			// fillMissingKeybinds reads it as one. Warning about it meant the
			// documented way to unbind something cost a startup warning for as
			// long as the config lived, which taught users the unbind had not
			// worked.
			if len(keys) == 0 {
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
	validateSection("tape_prefix", cfg.Keybindings.TapePrefix)
	validateSection("layout_prefix", cfg.Keybindings.LayoutPrefix)
	validateSection("terminal_mode", cfg.Keybindings.TerminalMode)
	validateSection("sidebar", cfg.Keybindings.Sidebar)
	validateSection("global", cfg.Keybindings.Global)
	validateSection("script", cfg.Keybindings.Script)

	// Validate enum appearance options (warn on unknown values; they fall back to defaults)
	validateAppearanceEnums(cfg, result)

	// Validate the tape section (warn on an unknown autorun mode)
	validateTapeConfig(cfg, result)

	// Validate the notifications section (warn on a duration that would put a
	// message back under the accessibility floor)
	validateNotificationsConfig(cfg, result)

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

	validateDock(cfg, result)

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

// minReadableNotification is the shortest message lifetime this config will
// accept without complaint. Below about four seconds a status line is a time
// limit on reading content with no way to extend it, which is the WCAG 2.2.1
// failure the old 1500ms default was. The value is not enforced, because a user
// who has read the warning and wants a faster bar is entitled to one; it is
// reported so the choice is a choice.
const minReadableNotification = 4

// validateNotificationsConfig warns when a configured message lifetime is short
// enough to be unreadable. Negative and zero values are not warned about: they
// mean "unset" and leave the default in place.
func validateNotificationsConfig(cfg *UserConfig, result *ValidationResult) {
	check := func(key string, seconds int) {
		if seconds <= 0 || seconds >= minReadableNotification {
			return
		}
		result.Warnings = append(result.Warnings, ValidationError{
			Field: "notifications",
			Key:   key,
			Message: fmt.Sprintf("%ds is shorter than the %ds needed to read a message; it is applied as written but is an accessibility (WCAG 2.2.1) failure",
				seconds, minReadableNotification),
		})
	}
	check("duration", cfg.Notifications.Duration)
	check("warning_duration", cfg.Notifications.WarningDuration)
	check("error_duration", cfg.Notifications.ErrorDuration)

	agent := &cfg.Notifications.Agent
	if _, _, err := ParseQuietHours(agent.QuietHours); err != nil {
		result.Warnings = append(result.Warnings, ValidationError{
			Field:   "notifications.agent",
			Key:     "quiet_hours",
			Message: fmt.Sprintf("%v; ignored, so alerts are never silenced by the clock", err),
		})
	}
	if _, ok := ParseAgentSoundMode(agent.SoundMode); !ok {
		result.Warnings = append(result.Warnings, ValidationError{
			Field: "notifications.agent",
			Key:   "sound_mode",
			Message: fmt.Sprintf("%q is not one of %s; falling back to %q",
				agent.SoundMode, strings.Join(AgentSoundModeNames, ", "), defaultAgentSoundMode),
		})
	}
	if agent.SoundCooldownSeconds != nil && *agent.SoundCooldownSeconds < 0 {
		result.Warnings = append(result.Warnings, ValidationError{
			Field:   "notifications.agent",
			Key:     "sound_cooldown_seconds",
			Message: "a negative gap is not a thing; falling back to the default",
		})
	}
	if agent.SettleSeconds != nil && *agent.SettleSeconds < 0 {
		result.Warnings = append(result.Warnings, ValidationError{
			Field:   "notifications.agent",
			Key:     "settle_seconds",
			Message: "a negative wait is not a thing; falling back to the default",
		})
	}
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

	checkEnum("border_style", cfg.Appearance.BorderStyle, BorderStyles)
	checkEnum("dockbar_position", cfg.Appearance.DockbarPosition, DockbarPositions)
	checkEnum("sidebar.position", cfg.Appearance.Sidebar.Position, SidebarPositions)
	if cfg.Appearance.Sidebar.Workspaces != "" {
		result.Warnings = append(result.Warnings, ValidationError{
			Field:   "appearance.sidebar.workspaces",
			Message: "no longer used: panes name their own workspace, and switching lives on the dock and alt+1..9",
		})
	}
	checkEnum("click_to_type", cfg.Appearance.ClickToType, ClickToTypeModes)
	checkEnum("zen_mode", cfg.Appearance.ZenMode, ZenModeModes)
	checkEnum("window_button_style", cfg.Appearance.WindowButtonStyle, WindowButtonStyles)
	checkEnum("window_button_position", cfg.Appearance.WindowButtonPosition, WindowButtonPositions)
	checkEnum("scrollbar.style", cfg.Appearance.Scrollbar.Style, ScrollbarStyles)
	checkEnum("whichkey_position", cfg.Appearance.WhichKeyPosition, WhichKeyPositions)
	checkEnum("window_title_position", cfg.Appearance.WindowTitlePosition, WindowTitlePositions)
	validateTitleFormat(cfg.Appearance.WindowTitleFormat, result)
	validateGlyphSet(cfg, result)
	validateDimUnfocused(cfg, result)
	validateClockFormat(cfg.Appearance.ClockFormat, result)
	validateBorderColors(cfg, result)
	validateScrollbar(cfg, result)
}

// validateGlyphSet warns about a set that does not resolve, and repeats the
// lines the directory read produced.
//
// The set's own problems are surfaced here rather than only logged because they
// are the answer to "why is my set half applied": a role dropped for being the
// wrong width is silent on screen, and it is exactly the mistake a hand-written
// set makes.
func validateGlyphSet(cfg *UserConfig, result *ValidationResult) {
	id := cfg.Appearance.Glyphs
	if id != "" && !theme.GlyphSetExists(id) {
		result.Warnings = append(result.Warnings, ValidationError{
			Field:   "appearance",
			Key:     "glyphs",
			Message: fmt.Sprintf("'%s' is not a glyph set (see list-glyphs); the built-in glyphs are used instead", id),
		})
	}
	for _, p := range theme.GlyphSetProblems() {
		result.Warnings = append(result.Warnings, ValidationError{
			Field: "appearance", Key: "glyphs", Message: p,
		})
	}
}

// validateDimUnfocused warns when the dim is asked for and cannot do its whole
// job.
//
// With no theme set, tuios emits colour indices and the host terminal decides
// what they look like, so a cell drawn in the terminal's own default has no RGB
// here to carry anywhere. Those cells are left alone rather than guessed at,
// which on a plain shell prompt is most of them, so the setting looks broken
// unless somebody says this out loud.
func validateDimUnfocused(cfg *UserConfig, result *ValidationResult) {
	if cfg.Appearance.DimUnfocused <= 0 || cfg.Appearance.Theme != "" {
		return
	}
	result.Warnings = append(result.Warnings, ValidationError{
		Field: "appearance",
		Key:   "dim_unfocused",
		Message: "no theme is set, so a cell drawn in the terminal's own default colour has no colour " +
			"tuios knows and is left undimmed; only cells a program coloured itself are quieted",
	})
}

// validateClockFormat warns about a layout that formats to nothing.
//
// Go's time layouts have no syntax to be wrong at: any string is a layout, and
// one with no reference-time component in it formats to itself. That is a
// legitimate thing to want ("REC" as a clock is a fixed label), so this warns
// only about the case a user cannot have meant, which is a layout whose output
// carries no digit at all after a real time is put through it.
func validateClockFormat(format string, result *ValidationResult) {
	if format == "" {
		return
	}
	rendered := clockFormatSample(format)
	if strings.ContainsFunc(rendered, unicode.IsDigit) {
		return
	}
	result.Warnings = append(result.Warnings, ValidationError{
		Field: "appearance",
		Key:   "clock_format",
		Message: fmt.Sprintf("'%s' formats to '%s', which has no time in it; see the Go time layout reference",
			format, rendered),
	})
}

// hexColorPattern matches the one colour literal the config accepts.
var hexColorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// IsHexColor reports whether s is a colour literal the config can hold. One
// spelling, so a value written by the settings panel, typed at the CLI or put in
// the file by hand is the same string either way.
func IsHexColor(s string) bool { return hexColorPattern.MatchString(s) }

// validateBorderColors warns about a border override that is not a colour.
//
// The override is handed to lipgloss as-is and an unparseable one resolves to
// no colour at all, so the pane it was meant to mark loses its border ink
// instead. The value is left in place: the warning says which key is wrong, and
// the border falls back to the theme's own colour meanwhile.
func validateBorderColors(cfg *UserConfig, result *ValidationResult) {
	for _, c := range [...]struct{ key, value string }{
		{"border_focused_color", cfg.Appearance.BorderFocusedColor},
		{"border_unfocused_color", cfg.Appearance.BorderUnfocusedColor},
	} {
		if c.value == "" || IsHexColor(c.value) {
			continue
		}
		result.Warnings = append(result.Warnings, ValidationError{
			Field:   "appearance",
			Key:     c.key,
			Message: fmt.Sprintf("'%s' is not a colour (expected #RRGGBB); the theme's border colour is used instead", c.value),
		})
	}
}

// validateScrollbar warns about the scrollbar's free-form keys, which no enum
// covers: the two glyphs have to measure exactly one cell or they would shift
// the content column they float over, and the tint is either a keyword or a hex
// literal. Each falls back to the style's default, so the frame stays drawable.
func validateScrollbar(cfg *UserConfig, result *ValidationResult) {
	sb := cfg.Appearance.Scrollbar

	checkGlyph := func(key, value string) {
		if value == "" || lipgloss.Width(value) == 1 {
			return
		}
		result.Warnings = append(result.Warnings, ValidationError{
			Field:   "appearance",
			Key:     "scrollbar." + key,
			Message: fmt.Sprintf("'%s' is %d cells wide; the scrollbar is one column, so the default glyph is used instead", value, lipgloss.Width(value)),
		})
	}
	checkGlyph("thumb", sb.Thumb)
	if sb.Track != ScrollbarTrackNone {
		checkGlyph("track", sb.Track)
	}

	if sb.Tint == "" || slices.Contains(ScrollbarTints, sb.Tint) || IsHexColor(sb.Tint) {
		return
	}
	result.Warnings = append(result.Warnings, ValidationError{
		Field: "appearance",
		Key:   "scrollbar.tint",
		Message: fmt.Sprintf("'%s' is not a valid value (allowed: %s, or #RRGGBB); falling back to default",
			sb.Tint, strings.Join(ScrollbarTints, ", ")),
	})
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
		"copy_selection", "paste_clipboard", "clear_selection",
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
