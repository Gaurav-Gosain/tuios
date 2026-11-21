package config

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/lazyre"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

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

	validateSection := func(sectionName string, section map[string][]string) {
		for _, keys := range section {
			if len(keys) == 0 {
				continue
			}

			for _, key := range keys {
				valid, errMsg := normalizer.ValidateSectionKey(sectionName, key)
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
	validateReviewKeys(cfg, result)

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

	if k := cfg.Keybindings.NestExitKey; k != "" {
		if valid, errMsg := normalizer.ValidateKey(k); !valid {
			result.Errors = append(result.Errors, ValidationError{
				Field:   "keybindings",
				Key:     "nest_exit_key",
				Message: errMsg,
			})
		} else if KeyTypes(k) {
			result.Warnings = append(result.Warnings, ValidationError{
				Field:   "keybindings",
				Key:     "nest_exit_key",
				Message: fmt.Sprintf("%q is a key programs type, so nest mode ends each time it is typed. Use a function key, such as %s, or a key with ctrl or alt", k, DefaultNestExitKey),
			})
		} else if leader := cfg.Keybindings.LeaderKey; leader != "" && IsKeyPress(CanonicalKey(leader), k) {
			result.Warnings = append(result.Warnings, ValidationError{
				Field:   "keybindings",
				Key:     "nest_exit_key",
				Message: fmt.Sprintf("%q is the leader, so the program in the pane never gets it in nest mode. Use another key, such as %s", k, DefaultNestExitKey),
			})
		}
	}

	if p := cfg.Keybindings.Preset; p != "" && !slices.Contains(Presets(), p) {
		result.Warnings = append(result.Warnings, ValidationError{
			Field: "keybindings",
			Key:   "preset",
			Message: fmt.Sprintf("%q is not a keybind preset. Use one of: %s. tuios uses its own keys",
				p, strings.Join(Presets(), ", ")),
		})
	}
	warnEnum(result, "keybindings", "leader_twice", cfg.Keybindings.LeaderTwice, LeaderTwiceModes, LeaderTwiceSend)
	warnEnum(result, "keybindings", "keyboard_layout", cfg.Keybindings.KeyboardLayout, KeyboardLayouts, KeyboardLayoutUS)
	warnEnum(result, "keybindings", "option_glyphs", cfg.Keybindings.OptionGlyphs, OptionGlyphModes, OptionGlyphsAuto)
	validatePrefixRepeat(cfg, result)

	for _, section := range keySections(&cfg.Keybindings) {
		validateSection(section.name, section.keys)
	}
	validateMenus(cfg, result)

	validateOptions(cfg, result)
	validateAppearance(cfg, result)
	validateMotion(cfg, result)
	validateSSHAgent(cfg, result)
	validateSSH(cfg, result)
	validateDisplay(cfg, result)
	validateLinkPolicies(cfg, result)
	validatePanePermissions(cfg, result)
	validateAgentWork(cfg, result)
	validateWorktree(cfg, result)
	validateTasks(cfg, result)
	validateUserActions(cfg, result)
	validateMenusConfig(cfg, result)
	validateProfile(cfg, result)

	validateNotificationsConfig(cfg, result)
	validateNotify(cfg, result)

	registry := NewKeybindRegistry(cfg)
	for _, c := range registry.Collisions() {
		key := c.Press
		actions := []string{c.Winner}
		where := []string{c.Winner + " in " + c.WinnerSource.String()}
		for _, l := range c.Losers {
			actions = append(actions, l.Action)
			where = append(where, l.Action+" in "+l.Source.String())
		}
		how := fmt.Sprintf("Run `tuios keybinds unbind <action> %s` to take the key off one of them.", key)
		if slices.ContainsFunc(actions, func(a string) bool { return strings.HasPrefix(a, CommandActionPrefix) }) {
			how = "To move a command entry, edit its key in the [[keybindings.command]] table of config.toml. For an action, run `tuios keybinds unbind <action> " + key + "`."
		}
		result.Warnings = append(result.Warnings, ValidationError{
			Field:   "keybindings",
			Key:     key,
			Message: fmt.Sprintf("%s runs %s. These never run: %s. The bindings are set here: %s. %s", key, actions[0], strings.Join(actions[1:], ", "), strings.Join(where, ", "), how),
		})
	}
	for _, c := range registry.LeaderClashes() {
		result.Warnings = append(result.Warnings, ValidationError{
			Field: "keybindings",
			Key:   c.Press,
			Message: fmt.Sprintf("%s runs %s in %s (%s), and it is also the leader key (%s). Only one of the two works. Move one of them to another key.",
				c.Press, c.Action, strings.ToLower(c.ScopeName), c.Source, c.LeaderSource),
		})
	}

	essentialActions := []struct {
		section, action string
		alternatives    [][2]string
	}{
		{section: "window_management", action: "new_window"},
		{section: "window_management", action: "close_window"},
		{section: "mode_control", action: "enter_terminal_mode"},
		{section: "mode_control", action: "enter_window_mode", alternatives: [][2]string{
			{"prefix_mode", "prefix_exit_mode"},
			{"terminal_mode", "terminal_exit_mode"},
		}},
		{section: "mode_control", action: "quit"},
	}

	for _, e := range essentialActions {
		bound := hasKeybinding(cfg, e.section, e.action)
		for _, alt := range e.alternatives {
			bound = bound || hasKeybinding(cfg, alt[0], alt[1])
		}
		if !bound {
			result.Warnings = append(result.Warnings, ValidationError{
				Field:   e.section,
				Key:     e.action,
				Message: fmt.Sprintf("Essential action '%s' has no keybinding, so TUIOS may be difficult to use", e.action),
			})
		}
	}

	if normalizer.IsMacOS() {
		shipped := defaultKeybindingPairs()
		checkMacOSAltUsage := func(sectionName string, section map[string][]string) {
			for action, keys := range section {
				for _, key := range keys {
					keyLower := strings.ToLower(strings.TrimSpace(key))
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
	validateSidebarPane(cfg, result)
	validateSidebarEdges(cfg.Appearance.Sidebar, result)
	validateHints(cfg, result)
	validateLinks(cfg, result)
	validatePanes(cfg, result)
	validateScratch(cfg, result)
	validatePiP(cfg, result)
	validateFilesOpen(cfg, result)

	return result
}

func validatePrefixRepeat(cfg *UserConfig, result *ValidationResult) {
	for _, action := range cfg.Keybindings.Repeat {
		if len(cfg.Keybindings.PrefixMode[action]) > 0 {
			continue
		}
		result.Warnings = append(result.Warnings, ValidationError{
			Field:   "keybindings",
			Key:     "repeat",
			Message: fmt.Sprintf("No key runs '%s' after the leader, so it does not repeat. Bind it in [keybindings.prefix_mode] or remove it from the list.", action),
		})
	}
}

func validateOptions(cfg *UserConfig, result *ValidationResult) {
	for _, o := range Options() {
		if o.Color || o.Deprecated != "" {
			continue
		}
		value, ok := GetOptionValue(cfg, o.Path)
		if !ok {
			continue
		}
		field, key := optionWarningPlace(o)
		switch {
		case o.Type == OptionString && len(o.Accepted) > 0:
			if o.Normalize != nil {
				value = o.Normalize(value)
			}
			warnEnum(result, field, key, value, o.Accepted, cmp.Or(o.Fallback, "default"))
		case o.Type == OptionInt && o.Max > 0:
			n, err := strconv.Atoi(value)
			if err != nil || (n == 0 && o.Min > 0) || (n >= o.Min && n <= o.Max) {
				continue
			}
			result.Warnings = append(result.Warnings, ValidationError{
				Field:   field,
				Key:     key,
				Message: fmt.Sprintf("%d is not in the range %d to %d. Use a value in this range.", n, o.Min, o.Max),
			})
		}
	}
}

func optionWarningPlace(o Option) (field, key string) {
	if rest, ok := strings.CutPrefix(o.Path, "appearance."); ok && o.Type == OptionString && len(o.Accepted) > 0 {
		return "appearance", rest
	}
	i := strings.LastIndex(o.Path, ".")
	return o.Path[:i], o.Path[i+1:]
}

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

func validateSSHAgent(cfg *UserConfig, result *ValidationResult) {
	warnEnum(result, "daemon", "ssh_agent", cfg.Daemon.SSHAgent, SSHAgentModes, SSHAgentOff)
}

func validateDisplay(cfg *UserConfig, result *ValidationResult) {
	warnEnum(result, "daemon", "display", cfg.Daemon.Display, DisplayModes, DisplayOff)
}

func validateNotificationsConfig(cfg *UserConfig, result *ValidationResult) {
	agent := &cfg.Notifications.Agent
	if _, _, err := ParseQuietHours(agent.QuietHours); err != nil {
		result.Warnings = append(result.Warnings, ValidationError{
			Field:   "notifications.agent",
			Key:     "quiet_hours",
			Message: fmt.Sprintf("%v; ignored, so alerts are never silenced by the clock", err),
		})
	}

	mail := ResolveMailAlerts(&cfg.Notifications.Mail, ResolveAgentAlerts(agent))
	if !mail.Enabled && mail.BetweenAgents {
		result.Warnings = append(result.Warnings, ValidationError{
			Field:   "notifications.mail",
			Key:     "between_agents",
			Message: "mail alerts are off, so between_agents does nothing. Set notifications.mail.enabled = true to use it.",
		})
	}
}

func validateAppearance(cfg *UserConfig, result *ValidationResult) {
	if !cfg.Appearance.MaxFPS.Valid() {
		result.Warnings = append(result.Warnings, ValidationError{
			Field:   "appearance",
			Key:     "max_fps",
			Message: fmt.Sprintf("'%s' is not a number or auto; read as %d", cfg.Appearance.MaxFPS, DefaultFPS),
		})
	}
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
	validateSidebarSubagents(cfg.Appearance.Sidebar, result)
	if cfg.Appearance.Sidebar.Workspaces != "" {
		result.Warnings = append(result.Warnings, ValidationError{
			Field:   "appearance.sidebar.workspaces",
			Message: "no longer used: panes name their own workspace, and switching lives on the dock and alt+1..9",
		})
	}
	if _, ok := ParseDropPreviewDelay(cfg.Appearance.DropPreviewDelay); !ok {
		result.Warnings = append(result.Warnings, ValidationError{
			Field:   "appearance",
			Key:     "drop_preview_delay",
			Message: fmt.Sprintf("'%s' is not a duration such as 120ms, or 0; read as %s", cfg.Appearance.DropPreviewDelay, DefaultDropPreviewDelay),
		})
	}
	validateTitleFormat("appearance", "window_title_format", cfg.Appearance.WindowTitleFormat, knownTitlePlaceholders, result)
	validateTitleFormat("appearance", "host_title", cfg.Appearance.HostTitle, knownHostTitlePlaceholders, result)
	validateTitleFormat("screenshot", "title_format", cfg.Screenshot.TitleFormat, knownTitlePlaceholders, result)
	validateGlyphSet(cfg, result)
	validateOmarchyTheme(cfg, result)
	validateClockFormat(cfg.Appearance.ClockFormat, result)
	validateBorderColors(cfg, result)
	validateSelectionColors(cfg, result)
	validateScrollbar(cfg, result)
	validateScrollColumnPresets(cfg, result)
	validateDockModeIcons(cfg, result)
	validateBackgrounds(cfg, result)
	warnColours(result, "appearance", "The cursor row keeps the theme's background",
		"sidebar.cursor_background", cfg.Appearance.Sidebar.CursorBackground)
}

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
		validateBackground(bg.key, bg.value, bg.surface, theme.Themed(a.Theme), result)
	}
}

func validateBackground(key, v, surface string, themed bool, result *ValidationResult) {
	switch {
	case v == "" || v == BackgroundOff || theme.IsColor(v):
		return
	case v == BackgroundTheme:
		if themed {
			return
		}
		result.Warnings = append(result.Warnings, ValidationError{
			Field:   "appearance",
			Key:     key,
			Message: fmt.Sprintf("%s is theme, but no theme is set. %s. Set a theme or a colour", key, sentence(surface)),
		})
	default:
		result.Warnings = append(result.Warnings, ValidationError{
			Field:   "appearance",
			Key:     key,
			Message: fmt.Sprintf("%s. %s", ColourProblem(v, Backgrounds...), sentence(surface)),
		})
	}
}

func validateOmarchyTheme(cfg *UserConfig, result *ValidationResult) {
	if cfg.Appearance.Theme != theme.OmarchyID || theme.OmarchyInstalled() {
		return
	}
	result.Warnings = append(result.Warnings, ValidationError{
		Field:   "appearance",
		Key:     "theme",
		Message: "Omarchy is not installed on this machine. tuios uses the colours of your terminal.",
	})
}

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

var hexColorPattern = lazyre.New(`^#[0-9a-fA-F]{6}$`)

// IsHexColor reports whether s is #rrggbb, the one spelling the colour picker
// writes. A config value may be any colour theme.ParseColor reads; this only
// tells a picked hex from a name.
func IsHexColor(s string) bool { return hexColorPattern().MatchString(s) }

// ColourProblem is the warning for a colour value v that is not a colour: what
// is wrong and what to write instead. keywords are the words the key takes
// besides a colour, such as off and theme.
func ColourProblem(v string, keywords ...string) string {
	msg := fmt.Sprintf("%q is not a colour. %s", v, theme.ColorHelp)
	if len(keywords) > 0 {
		msg += ". You can also use one of: " + strings.Join(keywords, ", ")
	}
	return msg
}

func sentence(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func validateBorderColors(cfg *UserConfig, result *ValidationResult) {
	a := &cfg.Appearance
	warnColours(result, "appearance", "The border uses the theme colour",
		"border_focused_color", a.BorderFocusedColor,
		"border_focused_color_window", a.BorderFocusedColorWindow,
		"border_focused_color_terminal", a.BorderFocusedColorTerminal,
		"border_unfocused_color", a.BorderUnfocusedColor)
	warnColours(result, "appearance", "Overlay frames keep their default colour",
		"overlay_border_color", a.OverlayBorderColor)
	if a.BorderFocusedGradient == "" {
		return
	}
	stops := GradientStops(a.BorderFocusedGradient)
	for _, v := range stops {
		if !theme.IsColor(v) {
			result.Warnings = append(result.Warnings, ValidationError{
				Field: "appearance", Key: "border_focused_gradient",
				Message: ColourProblem(v) + ". The focused border uses one colour",
			})
			return
		}
	}
	if len(stops) < 2 {
		result.Warnings = append(result.Warnings, ValidationError{
			Field: "appearance", Key: "border_focused_gradient",
			Message: "A gradient needs two colours or more. The focused border uses one colour",
		})
	}
}

// GradientStops splits appearance.border_focused_gradient into its colour
// values: separated by spaces, commas or both.
func GradientStops(v string) []string {
	return strings.FieldsFunc(v, func(r rune) bool { return r == ' ' || r == ',' || r == '\t' })
}

func validateSelectionColors(cfg *UserConfig, result *ValidationResult) {
	sel := &cfg.Appearance.Selection
	warnColours(result, "appearance", "The mark is drawn without this colour",
		"selection.bg", sel.Bg, "selection.fg", sel.Fg,
		"selection.search_bg", sel.SearchBg, "selection.search_fg", sel.SearchFg,
		"selection.match_bg", sel.MatchBg, "selection.match_fg", sel.MatchFg,
		"selection.cursor_bg", sel.CursorBg, "selection.cursor_fg", sel.CursorFg,
		"selection.flash_color", sel.FlashColor)
}

func warnColours(result *ValidationResult, field, then string, kv ...string) {
	for i := 0; i+1 < len(kv); i += 2 {
		if v := kv[i+1]; v != "" && !theme.IsColor(v) {
			result.Warnings = append(result.Warnings, ValidationError{
				Field: field, Key: kv[i], Message: ColourProblem(v) + ". " + then,
			})
		}
	}
}

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

func validateScrollColumnPresets(cfg *UserConfig, result *ValidationResult) {
	for _, p := range cfg.Appearance.ScrollColumnPresets {
		if p > 0 && p <= ScrollColumnWidthCeiling {
			continue
		}
		result.Warnings = append(result.Warnings, ValidationError{
			Field:   "appearance",
			Key:     "scroll_column_presets",
			Message: fmt.Sprintf("%d is not a width from 1 to %d percent. tuios ignores it", p, ScrollColumnWidthCeiling),
		})
	}
}

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

	if sb.Tint == "" || slices.Contains(ScrollbarTints, sb.Tint) || theme.IsColor(sb.Tint) {
		return
	}
	result.Warnings = append(result.Warnings, ValidationError{
		Field:   "appearance",
		Key:     "scrollbar.tint",
		Message: ColourProblem(sb.Tint, ScrollbarTints...) + ". The scrollbar uses the quiet tint",
	})
}

var knownTitlePlaceholders = []string{"{title}", "{index}", "{cwd}"}

var knownHostTitlePlaceholders = append(slices.Clone(knownTitlePlaceholders), "{session}", "{workspace}")

var titlePlaceholderPattern = lazyre.New(`\{[^{}]*\}`)

func validateTitleFormat(field, key, format string, allowed []string, result *ValidationResult) {
	for _, placeholder := range titlePlaceholderPattern().FindAllString(format, -1) {
		if slices.Contains(allowed, placeholder) {
			continue
		}
		result.Warnings = append(result.Warnings, ValidationError{
			Field: field,
			Key:   key,
			Message: fmt.Sprintf("'%s' is not a known placeholder (allowed: %s); it will be shown literally",
				placeholder, strings.Join(allowed, ", ")),
		})
	}
}

type keySection struct {
	name string
	keys map[string][]string
}

func keySections(kb *KeybindingsConfig) []keySection {
	sections := make([]keySection, 0, len(keybindTables)+len(kb.Menus)+len(kb.Modes))
	for _, t := range keybindTables {
		sections = append(sections, keySection{t.name, *t.field(kb)})
	}
	for _, name := range kb.MenuNames() {
		sections = append(sections, keySection{MenuSectionPrefix + name, kb.Menus[name].Keys})
	}
	for _, name := range kb.ModeNames() {
		sections = append(sections, keySection{ModeSectionPrefix + name, kb.Modes[name].Keys})
	}
	return sections
}

// DroppedKey is one key DropUnreadableKeys took out of a config.
type DroppedKey struct {
	// File is the config file that sets the key, as DisplayPath writes it,
	// or "config.toml" when the files are not known.
	File string `json:"file"`
	// Section is the config table, or "keybindings" for the leader.
	Section string `json:"section"`
	// Action is the action the key was bound to, or "leader_key" or
	// "nest_exit_key".
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
	if d.IsLeader() || d.Action == "nest_exit_key" {
		name = d.Action + " = " + d.Key
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
	cfg.layers = lc
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
	if kb.NestExitKey != "" {
		if ok, msg := normalizer.ValidateKey(kb.NestExitKey); !ok {
			dropped = append(dropped, DroppedKey{
				File: fileOf("keybindings", "nest_exit_key"), Section: "keybindings", Action: "nest_exit_key",
				Key: kb.NestExitKey, Problem: msg, Fallback: []string{DefaultNestExitKey},
			})
			kb.NestExitKey = ""
		}
	}

	defaultTables := map[string]map[string][]string{}
	for _, section := range keySections(&defaults) {
		defaultTables[section.name] = section.keys
	}
	type emptied struct {
		section keySection
		action  string
		first   int
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
				if ok, msg := normalizer.ValidateSectionKey(section.name, key); !ok {
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

var windowModeSection = map[string]bool{
	"window_management": true, "workspaces": true, "layout": true,
	"mode_control": true, "system": true, "navigation": true,
	"restore_minimized": true,
}

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
	case "terminal_mode":
		section = cfg.Keybindings.TerminalMode
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

func validateMotion(cfg *UserConfig, result *ValidationResult) {
	warnEnum(result, "appearance", "workspace_transition", cfg.Appearance.WorkspaceTransition, WorkspaceTransitions, "default")
}
