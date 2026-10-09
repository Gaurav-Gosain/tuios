package config

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/lazyre"
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

	validateCommands(cfg, result)
	validateCopyPipes(cfg, result)
	validateCopyModeKeys(cfg, result)

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

	warnEnum(result, "keybindings", "keyboard_layout", cfg.Keybindings.KeyboardLayout, KeyboardLayouts, KeyboardLayoutUS)
	warnEnum(result, "keybindings", "option_glyphs", cfg.Keybindings.OptionGlyphs, OptionGlyphModes, OptionGlyphsBind)

	// Validate all sections
	for _, section := range keySections(&cfg.Keybindings) {
		validateSection(section.name, section.keys)
	}

	// Validate enum appearance options (warn on unknown values; they fall back to defaults)
	validateAppearanceEnums(cfg, result)

	// Validate the tape section (warn on an unknown autorun mode)
	validateTapeConfig(cfg, result)
	validateResumeAgents(cfg, result)
	validateWindowSize(cfg, result)
	validateSSHAgent(cfg, result)
	validateLinkPolicies(cfg, result)
	validatePanePermissions(cfg, result)
	validateAgentWork(cfg, result)
	validatePasteBuffers(cfg, result)

	// Validate the notifications section (warn on a duration that would put a
	// message back under the accessibility floor)
	validateNotificationsConfig(cfg, result)
	validateNotify(cfg, result)

	// Keys two actions contest. The first action in each list is the one that
	// runs; the rest never fire.
	for key, actions := range findConflicts(cfg, normalizer) {
		// The message names the winner and says how to act on it. A warning
		// that only listed the actions left the reader with a fact and no
		// verb, and the verb is the whole point of reporting it.
		how := fmt.Sprintf("Run `tuios keybinds unbind <action> %s` to take the key off one of them.", key)
		if slices.ContainsFunc(actions, func(a string) bool { return strings.HasPrefix(a, CommandActionPrefix) }) {
			how = "To move a command entry, edit its key in the [[keybindings.command]] table of config.toml. For an action, run `tuios keybinds unbind <action> " + key + "`."
		}
		result.Warnings = append(result.Warnings, ValidationError{
			Field:   "keybindings",
			Key:     key,
			Message: fmt.Sprintf("%s runs %s. These never run: %s. %s", key, actions[0], strings.Join(actions[1:], ", "), how),
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
				Message: fmt.Sprintf("Essential action '%s' has no keybinding, so TUIOS may be difficult to use", action),
			})
		}
	}

	// On macOS, warn about using alt+ instead of opt+ for better UX.
	//
	// Only about a binding the user wrote. The macOS defaults deliberately pair
	// an Option chord with an alt+ spelling for terminals that cannot send one
	// (terminal_next_window is opt+tab and alt+n), and next_session ships as
	// alt+shift+n outright, so judging every alt+ binding made the shipped
	// default config warn about itself: two warnings on every macOS machine
	// whose config nobody had touched, advising the user to edit keys they had
	// never chosen. An advisory is about a choice, and those are not choices.
	if normalizer.IsMacOS() {
		shipped := defaultKeybindingPairs()
		checkMacOSAltUsage := func(sectionName string, section map[string][]string) {
			for action, keys := range section {
				for _, key := range keys {
					keyLower := strings.ToLower(strings.TrimSpace(key))
					// Warn if using alt+ (suggest opt+ instead for macOS consistency)
					if strings.HasPrefix(keyLower, "alt+") && !shipped[action+"\x00"+keyLower] {
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
	validateSidebarCustom(cfg, result)
	validateSidebarEdges(cfg.Appearance.Sidebar, result)
	validateHints(cfg, result)
	validatePanes(cfg, result)
	validateScratch(cfg, result)

	return result
}

// validateTapeConfig warns when tape.autorun holds a value outside its allowed
// set. An unknown value silently falls back to the safe default ("ask"), so a
// typo would otherwise go unnoticed. An empty value is left to the default.
func validateTapeConfig(cfg *UserConfig, result *ValidationResult) {
	warnEnum(result, "tape", "autorun", cfg.Tape.Autorun, TapeAutorunModes, "default")
}

// warnEnum warns when field.key holds a value outside allowed, and names the
// value it falls back to. An empty value is left to the default.
func warnEnum(result *ValidationResult, field, key, value string, allowed []string, fallback string) {
	if value == "" || slices.Contains(allowed, value) {
		return
	}
	result.Warnings = append(result.Warnings, ValidationError{
		Field:   field,
		Key:     key,
		Message: fmt.Sprintf("'%s' is not a valid value (allowed: %s); falling back to %s", value, strings.Join(allowed, ", "), fallback),
	})
}

// validateResumeAgents warns when daemon.resume_agents holds a value outside
// its allowed set. An unknown value falls back to "ask", so a typo of "auto"
// would otherwise go unnoticed until a restart asked instead of resuming.
func validateResumeAgents(cfg *UserConfig, result *ValidationResult) {
	warnEnum(result, "daemon", "resume_agents", cfg.Daemon.ResumeAgents, ResumeAgentsModes, "ask")
}

// validateWindowSize warns when daemon.window_size holds a value outside its
// allowed set. An unknown value falls back to smallest.
func validateWindowSize(cfg *UserConfig, result *ValidationResult) {
	warnEnum(result, "daemon", "window_size", cfg.Daemon.WindowSize, WindowSizeModes, "smallest")
}

// validateSSHAgent warns when daemon.ssh_agent holds a value outside its
// allowed set. An unknown value is off.
func validateSSHAgent(cfg *UserConfig, result *ValidationResult) {
	warnEnum(result, "daemon", "ssh_agent", cfg.Daemon.SSHAgent, SSHAgentModes, SSHAgentOff)
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

	// A mail key left out follows [notifications.agent], so the only
	// contradiction is inside the mail table once its switch is resolved.
	mail := ResolveMailAlerts(&cfg.Notifications.Mail, ResolveAgentAlerts(agent))
	if !mail.Enabled && mail.BetweenAgents {
		result.Warnings = append(result.Warnings, ValidationError{
			Field:   "notifications.mail",
			Key:     "between_agents",
			Message: "mail alerts are off, so between_agents does nothing. Set notifications.mail.enabled = true to use it.",
		})
	}
}

// validateAppearanceEnums warns when an enum appearance option holds a value
// outside its allowed set. Such values silently fall back to defaults, so a
// typo would otherwise go unnoticed. Empty values are left to the defaults.
func validateAppearanceEnums(cfg *UserConfig, result *ValidationResult) {
	checkEnum := func(key, value string, allowed []string) {
		warnEnum(result, "appearance", key, value, allowed, "default")
	}

	checkEnum("border_style", cfg.Appearance.BorderStyle, BorderStyles)
	if !cfg.Appearance.MaxFPS.Valid() {
		result.Warnings = append(result.Warnings, ValidationError{
			Field:   "appearance",
			Key:     "max_fps",
			Message: fmt.Sprintf("'%s' is not a number or auto; read as %d", cfg.Appearance.MaxFPS, DefaultFPS),
		})
	}
	checkEnum("dockbar_position", cfg.Appearance.DockbarPosition, DockbarPositions)
	checkEnum("sidebar.position", cfg.Appearance.Sidebar.Position, SidebarPositions)
	for _, problem := range SidebarSectionProblems(cfg.Appearance.Sidebar.Sections) {
		result.Warnings = append(result.Warnings, ValidationError{
			Field:   "appearance.sidebar.sections",
			Message: problem,
		})
	}
	for _, problem := range ParseSidebarAgentRow(cfg.Appearance.Sidebar.AgentRow).Problems {
		result.Warnings = append(result.Warnings, ValidationError{
			Field:   "appearance.sidebar.agent_row",
			Message: problem,
		})
	}
	if _, ok := ParseAgentRestFold(cfg.Appearance.Sidebar.AgentRestFold); !ok {
		result.Warnings = append(result.Warnings, ValidationError{
			Field:   "appearance.sidebar",
			Key:     "agent_rest_fold",
			Message: fmt.Sprintf("'%s' is not a duration such as 1h, or off; read as 1h", cfg.Appearance.Sidebar.AgentRestFold),
		})
	}
	checkEnum("sidebar.folder_click", cfg.Appearance.Sidebar.FolderClick, SidebarFolderClicks)
	checkEnum("sidebar.file_delete", cfg.Appearance.Sidebar.FileDelete, SidebarFileDeletes)
	if cfg.Appearance.Sidebar.Workspaces != "" {
		result.Warnings = append(result.Warnings, ValidationError{
			Field:   "appearance.sidebar.workspaces",
			Message: "no longer used: panes name their own workspace, and switching lives on the dock and alt+1..9",
		})
	}
	checkEnum("click_to_type", cfg.Appearance.ClickToType, ClickToTypeModes)
	checkEnum("auto_enter_terminal_on_focus", string(cfg.Appearance.AutoEnterTerminalOnFocus), AutoEnterTerminalModes)
	checkEnum("zen_mode", cfg.Appearance.ZenMode, ZenModeModes)
	checkEnum("motion", cfg.Appearance.Motion, MotionLevels)
	checkEnum("links", cfg.Appearance.Links, LinkModes)
	checkEnum("link_click", cfg.Appearance.LinkClick, LinkClickModes)
	checkEnum("window_button_style", cfg.Appearance.WindowButtonStyle, WindowButtonStyles)
	checkEnum("tiling_scheme", cfg.Appearance.TilingScheme, TilingSchemes)
	checkEnum("master_position", cfg.Appearance.MasterPosition, MasterPositions)
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
	validateDockModeIcons(cfg, result)
	validateBackgrounds(cfg, result)
}

// validateBackgrounds warns about a background option that is neither a
// keyword nor a colour, and about theme asked for with no theme to take it
// from. Either way that surface is left transparent, which is the default, so
// the frame stays drawable and the warning says why nothing was painted.
func validateBackgrounds(cfg *UserConfig, result *ValidationResult) {
	a := &cfg.Appearance
	for _, bg := range []struct {
		key, value, surface string
	}{
		{"background", a.Background, "every surface it reaches stays transparent"},
		{"pane_background", a.PaneBackground, "panes stay transparent"},
		{"desktop_background", a.DesktopBackground, "the desktop stays transparent"},
		{"window_chrome_background", a.WindowChromeBackground, "pane borders and title bars stay transparent"},
		{"dock_background", a.DockBackground, "the dock stays transparent"},
		{"sidebar.background", a.Sidebar.Background, "the rail stays transparent"},
	} {
		validateBackground(bg.key, bg.value, bg.surface, a.Theme != "", result)
	}
}

// validateBackground is validateBackgrounds for one option.
func validateBackground(key, v, surface string, themed bool, result *ValidationResult) {
	switch {
	case v == "" || v == BackgroundOff || IsHexColor(v):
		return
	case v == BackgroundTheme:
		if themed {
			return
		}
		result.Warnings = append(result.Warnings, ValidationError{
			Field: "appearance",
			Key:   key,
			Message: fmt.Sprintf("%s is theme but no theme is set, so there is no theme background "+
				"to paint and %s; set a theme or a #RRGGBB colour", key, surface),
		})
	default:
		result.Warnings = append(result.Warnings, ValidationError{
			Field: "appearance",
			Key:   key,
			Message: fmt.Sprintf("'%s' is not a valid value (allowed: %s, or #RRGGBB); %s",
				v, strings.Join(Backgrounds, ", "), surface),
		})
	}
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
var hexColorPattern = lazyre.New(`^#[0-9a-fA-F]{6}$`)

// IsHexColor reports whether s is a colour literal the config can hold. One
// spelling, so a value written by the settings panel, typed at the CLI or put in
// the file by hand is the same string either way.
func IsHexColor(s string) bool { return hexColorPattern().MatchString(s) }

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

// validateDockModeIcons warns about a mode pill icon the dock cannot lay out:
// one with a control character, or one wider than DockModeIconMaxWidth cells.
// The pill draws the built-in icon instead, so the dock row keeps its shape.
func validateDockModeIcons(cfg *UserConfig, result *ValidationResult) {
	a := cfg.Appearance
	for _, icon := range [...]struct {
		key   string
		value *string
	}{
		{"dock_mode_icon_window", a.DockModeIconWindow},
		{"dock_mode_icon_terminal", a.DockModeIconTerminal},
		{"dock_mode_icon_tiling", a.DockModeIconTiling},
	} {
		if icon.value == nil || DockModeIconUsable(*icon.value) {
			continue
		}
		result.Warnings = append(result.Warnings, ValidationError{
			Field: "appearance",
			Key:   icon.key,
			Message: fmt.Sprintf("%q is not a usable icon. Use at most %d cells and no control characters. The dock shows the default icon",
				*icon.value, DockModeIconMaxWidth),
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
var titlePlaceholderPattern = lazyre.New(`\{[^{}]*\}`)

func validateTitleFormat(format string, result *ValidationResult) {
	for _, placeholder := range titlePlaceholderPattern().FindAllString(format, -1) {
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

// findConflicts returns every key that two actions contest, keyed by the key,
// with the winner first and the dead actions after it.
//
// It delegates to KeybindRegistry.Collisions rather than deciding for itself.
// It used to keep its own idea of which actions could compete, in the form of a
// tilingModeActions and a nonTilingModeActions list, and it suppressed any
// clash that straddled the two. That partition described a scope that does not
// exist: the lookup flattens window_management and layout into one keymap and
// never consults the mode, and only the handler checks it, by which point the
// losing action's handler is not being called. It was also simply wrong about
// select_window_N, which has no tiling guard at all and works in both modes.
//
// The cost of the disagreement was four dead bindings in the shipped defaults
// that this function stayed silent about for as long as they existed, while the
// keybind report named all four. Two conflict detectors that disagree means the
// quieter one is load-bearing for nobody and misleading for everybody, so there
// is now one.
func findConflicts(cfg *UserConfig, _ *KeyNormalizer) map[string][]string {
	conflicts := make(map[string][]string)
	for _, c := range NewKeybindRegistry(cfg).Collisions() {
		actions := []string{c.Winner}
		for _, l := range c.Losers {
			actions = append(actions, l.Action)
		}
		// Keyed by the whole chord, not the bare key: "1" means one thing in
		// window mode and another after the layout chord, and a warning that
		// said only "1" could not be acted on.
		conflicts[c.Press] = actions
	}
	return conflicts
}

// keySection is one keybinding table of config.toml, by its name there.
type keySection struct {
	name string
	keys map[string][]string
}

// keySections are the keybinding tables ValidateConfig checks key by key.
func keySections(kb *KeybindingsConfig) []keySection {
	return []keySection{
		{"window_management", kb.WindowManagement},
		{"workspaces", kb.Workspaces},
		{"layout", kb.Layout},
		{"mode_control", kb.ModeControl},
		{"system", kb.System},
		{"navigation", kb.Navigation},
		{"restore_minimized", kb.RestoreMinimized},
		{"prefix_mode", kb.PrefixMode},
		{"window_prefix", kb.WindowPrefix},
		{"minimize_prefix", kb.MinimizePrefix},
		{"workspace_prefix", kb.WorkspacePrefix},
		{"debug_prefix", kb.DebugPrefix},
		{"tape_prefix", kb.TapePrefix},
		{"layout_prefix", kb.LayoutPrefix},
		{"terminal_mode", kb.TerminalMode},
		{"sidebar", kb.Sidebar},
		{"sidebar_files", kb.SidebarFiles},
		{"sidebar_agents", kb.SidebarAgents},
		{"inbox", kb.Inbox},
		{"inbox_peek", kb.InboxPeek},
		{"mail", kb.Mail},
		{"copy_mode", kb.CopyMode},
		{"global", kb.Global},
		{"script", kb.Script},
	}
}

// DroppedKey is one key DropUnreadableKeys took out of a config.
type DroppedKey struct {
	// File is the config file that sets the key, as DisplayPath writes it,
	// or "config.toml" when the files are not known.
	File string `json:"file"`
	// Section is the config table, or "keybindings" for the leader.
	Section string `json:"section"`
	// Action is the action the key was bound to, or "leader_key".
	Action string `json:"action"`
	Key    string `json:"key"`
	// Problem is what is wrong, in the validator's words.
	Problem string `json:"problem"`
	// Fallback is the key the action uses now: the default key when the
	// action had no other key, or the default leader. It is empty when the
	// action kept another key of its own, or when every default key was
	// already another action's.
	Fallback []string `json:"fallback,omitempty"`
	// TakenBy is the action that holds the default key, when the action got
	// no key back because of it.
	TakenBy string `json:"taken_by,omitempty"`
	// KeptOthers is true when the action still has another key of its own.
	KeptOthers bool `json:"kept_others,omitempty"`
}

// IsLeader reports whether the dropped key was the leader.
func (d DroppedKey) IsLeader() bool { return d.Action == "leader_key" }

// Outcome says what tuios does instead of the key, in one sentence.
func (d DroppedKey) Outcome() string {
	switch {
	case d.IsLeader():
		return fmt.Sprintf("The leader is %s until you correct it.", strings.Join(d.Fallback, ", "))
	case len(d.Fallback) > 0:
		return fmt.Sprintf("%s uses its default key, %s.", d.Action, strings.Join(d.Fallback, ", "))
	case d.TakenBy != "":
		return fmt.Sprintf("%s has no key, because its default key runs %s.", d.Action, d.TakenBy)
	case d.KeptOthers:
		return fmt.Sprintf("%s keeps its other keys.", d.Action)
	}
	return fmt.Sprintf("%s has no key.", d.Action)
}

// Warning is the line the TUI logs for the dropped key.
func (d DroppedKey) Warning() string {
	name := d.Key
	if d.IsLeader() {
		name = "leader_key = " + d.Key
	}
	return fmt.Sprintf("%s: [%s] %s: %s. tuios ignores this key. %s", d.File, d.Section, name, d.Problem, d.Outcome())
}

// DroppedWarnings is the Warning of each dropped key.
func DroppedWarnings(dropped []DroppedKey) []string {
	out := make([]string, 0, len(dropped))
	for _, d := range dropped {
		out = append(out, d.Warning())
	}
	return out
}

// DropUnreadableKeys takes out of cfg every key that ValidateConfig calls an
// error, and returns what it took out. lc names the file each key came from;
// it may be nil.
//
// Without it one such key cost the whole file: the load failed, tuios ran on
// the defaults, and the error went to a stderr the first frame wiped (issue
// #556). An action left with no key gets its default back, because an empty
// list in the file means "unbound" and nobody wrote that. A default key that
// another action already holds is left out, the same yield fillMissingKeybinds
// makes, so the fallback never takes a key from a binding the user wrote. A
// leader that cannot be read goes back to the default leader.
//
// The baseline is taken again afterwards, so a later save does not write the
// dropped keys out of the file. The file stays as the user wrote it. The
// result is kept on cfg as DroppedKeys, for keybinds doctor.
func DropUnreadableKeys(cfg *UserConfig, lc *LayeredConfig) []DroppedKey {
	if cfg == nil {
		return nil
	}
	normalizer := NewKeyNormalizer()
	defaults := DefaultConfig().Keybindings
	fileOf := func(path ...string) string {
		if lc == nil {
			return "config.toml"
		}
		holders := lc.Holders(path)
		if len(holders) == 0 {
			return lc.DisplayPath(lc.Main)
		}
		return lc.DisplayPath(holders[len(holders)-1].Path)
	}
	var dropped []DroppedKey

	kb := &cfg.Keybindings
	if kb.LeaderKey != "" {
		if ok, msg := normalizer.ValidateKey(kb.LeaderKey); !ok {
			dropped = append(dropped, DroppedKey{
				File: fileOf("keybindings", "leader_key"), Section: "keybindings", Action: "leader_key",
				Key: kb.LeaderKey, Problem: msg, Fallback: []string{defaults.LeaderKey},
			})
			kb.LeaderKey = defaults.LeaderKey
		}
	}

	defaultTables := map[string]map[string][]string{}
	for _, section := range keySections(&defaults) {
		defaultTables[section.name] = section.keys
	}
	type emptied struct {
		section keySection
		action  string
		first   int // index of the action's first entry in dropped
	}
	var refill []emptied
	for _, section := range keySections(kb) {
		actions := make([]string, 0, len(section.keys))
		for action := range section.keys {
			actions = append(actions, action)
		}
		slices.Sort(actions)
		for _, action := range actions {
			keys := section.keys[action]
			kept := keys[:0:0]
			first := len(dropped)
			for _, key := range keys {
				if ok, msg := normalizer.ValidateKey(key); !ok {
					dropped = append(dropped, DroppedKey{
						File: fileOf("keybindings", section.name, action), Section: section.name,
						Action: action, Key: key, Problem: msg,
					})
					continue
				}
				kept = append(kept, key)
			}
			if len(kept) == len(keys) {
				continue
			}
			section.keys[action] = kept
			if len(kept) > 0 {
				for i := first; i < len(dropped); i++ {
					dropped[i].KeptOthers = true
				}
				continue
			}
			refill = append(refill, emptied{section, action, first})
		}
	}
	// The defaults go back once every bad key is out, so a default is only
	// held back by a binding that works.
	for _, e := range refill {
		var back []string
		takenBy := ""
		for _, key := range defaultTables[e.section.name][e.action] {
			if other := keyHolder(cfg, e.section, e.action, key); other != "" {
				takenBy = other
				continue
			}
			back = append(back, key)
		}
		e.section.keys[e.action] = back
		for i := e.first; i < len(dropped) && dropped[i].Action == e.action && dropped[i].Section == e.section.name; i++ {
			dropped[i].Fallback = back
			if len(back) == 0 {
				dropped[i].TakenBy = takenBy
			}
		}
	}
	if len(dropped) > 0 {
		cfg.baseline, _ = MarshalUserConfig(cfg)
	}
	cfg.DroppedKeys = dropped
	return dropped
}

// keyHolder is the action other than action that holds key in the scope of
// section, or "". The window-mode tables are one keymap, so a key in any of
// them counts, as it does for yieldTakenDefaults.
func keyHolder(cfg *UserConfig, section keySection, action, key string) string {
	tables := map[string][]string(section.keys)
	if windowModeSection[section.name] {
		tables = windowModeTables(cfg)
	}
	holders := make([]string, 0, 1)
	for other, keys := range tables {
		if other == action {
			continue
		}
		if slices.ContainsFunc(keys, func(k string) bool { return sameKeyPress(k, key) }) {
			holders = append(holders, other)
		}
	}
	if len(holders) == 0 {
		return ""
	}
	slices.Sort(holders)
	return holders[0]
}

// windowModeSection are the tables windowModeTables joins.
var windowModeSection = map[string]bool{
	"window_management": true, "workspaces": true, "layout": true,
	"mode_control": true, "system": true, "navigation": true,
	"restore_minimized": true,
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

// defaultKeybindingPairs is every action-and-key pair the shipped defaults
// bind, keyed "<action>\x00<key>" with the key lowercased. It is what tells an
// advisory that a binding is tuios's own rather than the user's.
func defaultKeybindingPairs() map[string]bool {
	kb := DefaultConfig().Keybindings
	out := map[string]bool{}
	for _, section := range []map[string][]string{
		kb.WindowManagement, kb.Workspaces, kb.Layout, kb.ModeControl,
		kb.System, kb.PrefixMode, kb.WindowPrefix, kb.MinimizePrefix,
		kb.WorkspacePrefix, kb.TerminalMode,
	} {
		for action, keys := range section {
			for _, key := range keys {
				out[action+"\x00"+strings.ToLower(strings.TrimSpace(key))] = true
			}
		}
	}
	return out
}
