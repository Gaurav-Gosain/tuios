package app

import (
	"image/color"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// settingControl is the kind of editor a setting row uses.
type settingControl int

const (
	controlEnum   settingControl = iota // ‹ value › cycler
	controlBool                         // [ on ] / [ off ] toggle
	controlInt                          // ‹ n › numeric stepper
	controlString                       // free-text field edited inline
	// controlColor is a swatch and its value, opening the colour picker. It has
	// no stepper: there is no next colour to step to, and typing a hex into a
	// text field was never the way to choose one.
	controlColor
)

// settingItem is one row on the settings page. adjust changes the value by dir
// (-1 or +1 for enum/int, either flips a bool) and applies it live; the input
// handler persists afterward.
type settingItem struct {
	// Path is the registry option this row reaches, empty for a row that is
	// not one (the daemon log level's own spelling, a section header). The
	// coverage test reads it to tell an option with no way to reach it from one
	// that is deliberately absent.
	Path    string
	Label   string
	Desc    string
	Control settingControl
	Options []string
	// Placeholder is the example value, shown on the description line. It is
	// deliberately not drawn in the value's place: an example there reads as the
	// value in force.
	Placeholder string
	// Unset is what the field shows when nothing is set: what happens instead,
	// in the row's own terms.
	Unset   string
	value   func(m *OS) string
	boolVal func(m *OS) bool
	adjust  func(m *OS, dir int)
	// setStr commits an edited controlString value.
	setStr func(m *OS, v string)
	// swatch is the colour a controlColor row shows, given the ground it will be
	// painted on. It is the colour in force rather than the value stored, so an
	// unset row still shows what it is inheriting.
	swatch func(ground color.Color) color.Color
	// activate, when set, runs on Enter/click instead of adjusting the value
	// (e.g. the Theme row opens the theme picker).
	activate func(m *OS)
}

// settingsCategory groups related settings under a tab.
type settingsCategory struct {
	Name  string
	Items []settingItem
}

// cycleEnum returns the option dir steps away from current, wrapping around.
func cycleEnum(options []string, current string, dir int) string {
	if len(options) == 0 {
		return current
	}
	idx := 0
	for i, o := range options {
		if o == current {
			idx = i
			break
		}
	}
	idx = (idx + dir + len(options)) % len(options)
	return options[idx]
}

// clampInt bounds v to [lo, hi].
func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// applyAppearanceLive repaints all windows so a chrome change is visible
// immediately; when retile is set it also reflows the tiling layout for
// changes that affect window geometry (dock position, borders, title bars).
func (m *OS) applyAppearanceLive(retile bool) {
	m.MarkAllDirty()
	if retile && m.AutoTiling {
		m.TileAllWindows()
	}
}

// ApplyAppearanceLive is applyAppearanceLive for callers outside the package.
// The out-of-process fuzz target (internal/fuzz/apptarget) flips the same
// appearance globals the settings page does and has to land them the same way;
// open-coding the two calls there would be a copy that drifts the moment this
// one grows a third.
func (m *OS) ApplyAppearanceLive(retile bool) { m.applyAppearanceLive(retile) }

// applyTheme switches the active terminal theme at runtime and repaints. The
// sentinel "none" disables theming and restores standard terminal colors.
func (m *OS) applyTheme(name string) {
	if name == themeNone {
		_ = theme.Initialize("")
	} else {
		_ = theme.Initialize(name)
	}
	// Push the new palette into every emulator: SGR indexed colors resolve
	// through the emulator's color table at render time, so without this the
	// chrome recolors but terminal content keeps the old palette until fresh
	// guest output arrives. MarkAllDirty then forces a repaint with dropped
	// caches.
	m.UpdateAllWindowThemes()
	m.MarkAllDirty()
}

// applyBorderColors pushes the configured border color overrides into the theme
// package and repaints so the new colors show immediately. Empty values clear
// the override and restore the theme-derived colors.
func (m *OS) applyBorderColors() {
	focused, unfocused := "", ""
	if m.UserConfig != nil {
		focused = m.UserConfig.Appearance.BorderFocusedColor
		unfocused = m.UserConfig.Appearance.BorderUnfocusedColor
	}
	theme.SetBorderOverrides(focused, unfocused)
	m.MarkAllDirty()
}

// persistSettings writes the current config to disk. Called after any settings
// change so it survives a restart.
//
// A read-only session skips the write and says so once. The change itself has
// already been applied, so the session behaves as asked for as long as it
// lasts; what it does not do is decide the config file's contents on behalf of
// whoever else is attached. See OSOptions.ConfigReadOnly.
// The write itself is a command, not something this does. Marshalling the
// config and putting it on disk was happening inline, so every arrow-key press
// on a settings row spent a file write on the goroutine that must not block, and
// a held key spent one per repeat. Reading the config into bytes stays here
// (memory, and the config is the model's own); the file lands off the Update
// goroutine, the way the applist history does.
func (m *OS) persistSettings() tea.Cmd {
	if m.UserConfig == nil {
		return nil
	}
	if m.ConfigReadOnly {
		if !m.configReadOnlyTold {
			m.configReadOnlyTold = true
			m.ShowNotification("Settings apply to this session only; the config file is not written", "warning", 0)
		}
		return nil
	}
	write, err := config.RenderUserConfig(m.UserConfig)
	if err != nil {
		m.ShowNotification("Could not save settings: "+err.Error(), "error", 0)
		return nil
	}
	return func() tea.Msg {
		if err := write(); err != nil {
			return settingsSaveFailedMsg{err: err}
		}
		return nil
	}
}

// settingsSaveFailedMsg carries a failed config write back to the Update
// goroutine, which is the only place a notification can be raised from.
type settingsSaveFailedMsg struct{ err error }

// setAppearance runs fn against the held config's appearance section when a
// config is present, so live changes can be persisted.
func (m *OS) setAppearance(fn func(a *config.AppearanceConfig)) {
	if m.UserConfig != nil {
		fn(&m.UserConfig.Appearance)
	}
}

// setDebug runs fn against the held config's [debug] section when a config is
// present, so a change to a diagnostic toggle can be persisted.
func (m *OS) setDebug(fn func(d *config.DebugConfig)) {
	if m.UserConfig != nil {
		fn(&m.UserConfig.Debug)
	}
}

// ToggleShowKeys flips the showkeys overlay, mirrors the new state into the
// persisted [debug] show_key_events config, and saves it. Shared by the settings
// toggle, the command-palette entry, and the keybinding so all of them stay in
// sync and survive a restart.
func (m *OS) ToggleShowKeys() tea.Cmd {
	m.ShowKeys = !m.ShowKeys
	m.setDebug(func(d *config.DebugConfig) { d.ShowKeyEvents = m.ShowKeys })
	return m.persistSettings()
}

// ToggleFocusFollowsMouse flips focus-follows-mouse, mirrors the new state into
// the persisted appearance config, and saves it. Shared by the settings row and
// the command-palette entry so both stay in sync and survive a restart.
func (m *OS) ToggleFocusFollowsMouse() tea.Cmd {
	config.FocusFollowsMouse = !config.FocusFollowsMouse
	m.setAppearance(func(a *config.AppearanceConfig) { a.FocusFollowsMouse = boolPtr(config.FocusFollowsMouse) })
	return m.persistSettings()
}

const themeNone = "none"

var (
	borderStyleOptions     = config.BorderStyles
	positionOptions        = config.DockbarPositions
	whichKeyPosOptions     = config.WhichKeyPositions
	fpsOptions             = []string{"30", "60", "90", "120", "144", "unlimited"}
	sidebarPositionOptions = config.SidebarPositions
	scrollbarStyleOptions  = config.ScrollbarStyles
	windowButtonOptions    = config.WindowButtonStyles
	windowButtonPosOptions = config.WindowButtonPositions
	clickToTypeOptions     = config.ClickToTypeModes
)

// boolPtr returns a pointer to b, for the *bool config fields.
func boolPtr(b bool) *bool { return &b }

// enumItem builds an enum setting bound to a string config global via getters
// and a setter that updates the global, mirrors to the persisted config, and
// applies the change live.
func enumItem(label, desc string, options []string, get func() string, set func(m *OS, v string)) settingItem {
	return settingItem{
		Label:   label,
		Desc:    desc,
		Control: controlEnum,
		Options: options,
		value:   func(_ *OS) string { return get() },
		adjust: func(m *OS, dir int) {
			set(m, cycleEnum(options, get(), dir))
		},
	}
}

// boolItem builds a boolean toggle. show maps the stored value to what the row
// displays (e.g. "hide" flags are shown inverted as "on = visible").
func boolItem(label, desc string, get func() bool, set func(m *OS, v bool)) settingItem {
	return settingItem{
		Label:   label,
		Desc:    desc,
		Control: controlBool,
		boolVal: func(_ *OS) bool { return get() },
		adjust:  func(m *OS, _ int) { set(m, !get()) },
	}
}

// settingsCategories is the settings page: which options appear, under which
// tab, in which order.
//
// Almost every row is a registry path, and the row itself is derived from what
// the registry already says about it (see settings_registry.go). What is spelled
// out here is the ordering and the grouping, which is the part the registry has
// no opinion about. A row written by hand is one whose behaviour the registry
// cannot describe, and it names the path it stands in for so the coverage test
// can see it.
func (m *OS) settingsCategories() []settingsCategory {
	appearance := settingsCategory{
		Name: "Appearance",
		Items: m.resolveRows([]settingsRow{
			custom("appearance.theme", m.themeItem()),
			custom("appearance.glyphs", m.glyphItem()),
			opt("appearance.border_style"),
			opt("appearance.window_title_position"),
			opt("appearance.window_title_format"),
			opt("appearance.shared_borders"),
			opt("appearance.hide_window_buttons"),
			opt("appearance.window_button_style"),
			opt("appearance.window_button_position"),
			opt("appearance.hide_scrollbar"),
			opt("appearance.scrollbar.style"),
			opt("appearance.scrollbar.tint"),
			opt("appearance.scrollbar.thumb"),
			opt("appearance.scrollbar.track"),
			opt("appearance.border_focused_color"),
			opt("appearance.border_unfocused_color"),
			opt("appearance.gap"),
			opt("appearance.dim_unfocused"),
			opt("appearance.panel_padding"),
			opt("appearance.zen_mode"),
			opt("appearance.session_colors"),
		}),
	}

	// The sidebar rows were appended to Appearance while there were six of them,
	// to keep the tab strip on one row. There are nine now, which buried the rest
	// of Appearance under a scroll; a tab of their own costs the strip a second
	// row, and panelBody already budgets the body against TabRowCount, so that
	// row comes out of the scrolling list rather than out of the viewport.
	sidebar := settingsCategory{
		Name: "Sidebar",
		Items: m.resolveRows([]settingsRow{
			opt("appearance.sidebar.enabled"),
			opt("appearance.sidebar.position"),
			opt("appearance.sidebar.width"),
			opt("appearance.sidebar.show_windows"),
			opt("appearance.sidebar.show_glyphs"),
			opt("appearance.sidebar.show_counts"),
			opt("appearance.sidebar.show_agents"),
			opt("appearance.sidebar.marquee"),
			opt("appearance.sidebar.tooltips"),
		}),
	}

	dock := settingsCategory{
		Name: "Dock",
		Items: m.resolveRows([]settingsRow{
			opt("appearance.dockbar_position"),
			opt("appearance.show_clock"),
			opt("appearance.clock_format"),
			opt("dock.clock.format"),
			opt("appearance.show_cpu"),
			opt("appearance.show_ram"),
			opt("appearance.dock_workspace_tabs"),
			opt("appearance.dock_workspace_tab_format"),
			opt("appearance.dock_workspace_tooltip"),
			opt("appearance.dock_pill_caps"),
		}),
	}

	behavior := settingsCategory{
		Name: "Behavior",
		Items: m.resolveRows([]settingsRow{
			opt("appearance.animations_enabled"),
			opt("appearance.confirm_quit"),
			opt("appearance.whichkey_enabled"),
			opt("appearance.whichkey_position"),
			opt("appearance.focus_follows_mouse"),
			opt("appearance.click_to_type"),
			opt("appearance.alt_drag"),
			opt("appearance.niri_reverse_scroll"),
			custom("appearance.max_fps", m.maxFPSItem()),
			opt("appearance.preferred_shell"),
		}),
	}

	// [notifications] is nineteen options and was none of the page. An alert
	// that fires when it should not is the setting people go looking for first,
	// and until now the only place to change it was the file.
	notifications := settingsCategory{
		Name: "Notify",
		Items: m.resolveRows([]settingsRow{
			opt("notifications.duration"),
			opt("notifications.warning_duration"),
			opt("notifications.error_duration"),
			opt("notifications.error_sticky"),
			opt("notifications.agent.enabled"),
			opt("notifications.agent.notify"),
			opt("notifications.agent.dock"),
			opt("notifications.agent.sound"),
			opt("notifications.agent.sound_mode"),
			opt("notifications.agent.sound_cooldown_seconds"),
			opt("notifications.agent.settle_seconds"),
			opt("notifications.agent.suppress_focused"),
			opt("notifications.agent.quiet_hours"),
			opt("notifications.agent.command"),
			opt("notifications.agent.states.working"),
			opt("notifications.agent.states.idle"),
			opt("notifications.agent.states.done"),
			opt("notifications.agent.states.needs_input"),
			opt("notifications.agent.states.errored"),
		}),
	}

	startup := settingsCategory{
		Name: "Startup",
		Items: m.resolveRows([]settingsRow{
			opt("startup.open_default_window"),
			opt("startup.tiled"),
			opt("startup.start_in_terminal_mode"),
			opt("startup.daemon"),
		}),
	}

	advanced := settingsCategory{
		Name: "Advanced",
		Items: m.resolveRows([]settingsRow{
			opt("appearance.scrollback_lines"),
			opt("appearance.scroll_lines"),
			opt("appearance.copy_on_select"),
			opt("appearance.word_characters"),
			opt("appearance.zoom_max_width"),
			custom("debug.show_key_events", m.showKeysItem()),
		}),
	}

	daemon := settingsCategory{
		Name: "Daemon",
		Items: m.resolveRows([]settingsRow{
			opt("daemon.log_level"),
			opt("daemon.agent_autodetect"),
			opt("daemon.agent_detect_seconds"),
		}),
	}

	tape := settingsCategory{
		Name: "Tape",
		Items: m.resolveRows([]settingsRow{
			opt("tape.autorun"),
			opt("tape.auto_review"),
		}),
	}

	return []settingsCategory{
		appearance, sidebar, dock, behavior,
		notifications, startup, advanced, daemon, tape,
	}
}

// themeItem is the theme row. Hand-written because the value is a name from an
// open set rather than one of a closed list, so Enter opens the picker with its
// previews rather than stepping to a next theme nobody can name in advance.
func (m *OS) themeItem() settingItem {
	options := append([]string{themeNone}, theme.AvailableThemes()...)
	item := enumItem("Theme", "Color theme (press Enter for the picker with previews)", options,
		func() string {
			if id := theme.CurrentThemeID(); id != "" {
				return id
			}
			return themeNone
		},
		func(m *OS, v string) {
			m.applyTheme(v)
			m.setThemeSelection(v)
		})
	item.activate = func(m *OS) { m.OpenThemePicker() }
	return item
}

// glyphItem is the glyph set row, the theme's opposite number: the theme says
// what colour the chrome is and the set says what shape it is. Hand-written for
// the reason the theme row is, because the value is a name from an open set
// rather than one of a closed list.
func (m *OS) glyphItem() settingItem {
	options := theme.AvailableGlyphSets()
	return enumItem("Glyph set", "Characters the border, controls, rules and rail marks are drawn with",
		options,
		func() string { return theme.ActiveGlyphSetID() },
		func(m *OS, v string) { m.setOption("appearance.glyphs", v) })
}

// maxFPSItem is the frame-rate cap. Hand-written because the row says
// "unlimited" for a number: the config holds an int, and a stepper walking to
// the cap one frame at a time is not how anyone sets this.
func (m *OS) maxFPSItem() settingItem {
	return enumItem("Max FPS", "Render frame-rate cap (unlimited uncaps it)", fpsOptions,
		func() string {
			if config.NormalFPS >= config.MaxFPSCap {
				return "unlimited"
			}
			return strconv.Itoa(config.NormalFPS)
		},
		func(m *OS, v string) {
			fps := config.MaxFPSCap
			if v != "unlimited" {
				if n, err := strconv.Atoi(v); err == nil {
					fps = n
				}
			}
			m.setOption("appearance.max_fps", strconv.Itoa(fps))
		})
}

// showKeysItem is the keycast toggle. Hand-written because the overlay's live
// state is a model field the renderer reads directly, so the row has to move
// both it and the config.
func (m *OS) showKeysItem() settingItem {
	return boolItem("Show keys overlay", "Show pressed keys as a keycast in the bottom-right corner",
		func() bool { return m.ShowKeys },
		func(m *OS, v bool) {
			m.ShowKeys = v
			m.setOption("debug.show_key_events", strconv.FormatBool(v))
		})
}

// daemonLogLevel returns the configured daemon log level, defaulting to "off"
// when unset or no config is held.
func (m *OS) daemonLogLevel() string {
	if m.UserConfig != nil && m.UserConfig.Daemon.LogLevel != "" {
		return m.UserConfig.Daemon.LogLevel
	}
	return "off"
}

// OpenSettings shows the settings overlay, initializing the theme registry so
// the theme list is populated.
func (m *OS) OpenSettings() {
	theme.EnsureRegistry()
	m.ShowSettings = true
	m.SettingsCategory = 0
	m.SettingsSelected = 0
	m.SettingsScroll = 0
	m.SettingsEditing = false
	m.SettingsEditBuffer = ""
}

// OpenSettingsAt opens the settings overlay on the named category, for the
// entry points that already know which part of the app the user is pointing at.
// The name is resolved against the live category list rather than an index: the
// list is built per call, so a hardcoded index would rot the moment a tab moves.
func (m *OS) OpenSettingsAt(category string) {
	m.OpenSettings()
	for i, c := range m.settingsCategories() {
		if c.Name == category {
			m.SettingsCategory = i
			return
		}
	}
}

// CloseSettings hides the settings overlay.
func (m *OS) CloseSettings() {
	m.ShowSettings = false
	m.SettingsEditing = false
	m.SettingsEditBuffer = ""
}

// settingsCurrentItems returns the items in the active category, clamping the
// category and selection indices.
func (m *OS) settingsCurrentItems() []settingItem {
	cats := m.settingsCategories()
	if len(cats) == 0 {
		return nil
	}
	m.SettingsCategory = clampInt(m.SettingsCategory, 0, len(cats)-1)
	items := cats[m.SettingsCategory].Items
	if len(items) > 0 {
		m.SettingsSelected = clampInt(m.SettingsSelected, 0, len(items)-1)
	} else {
		m.SettingsSelected = 0
	}
	return items
}

// SettingsMoveUp/Down move the row selection within the active category.
func (m *OS) SettingsMoveUp() {
	if m.SettingsSelected > 0 {
		m.SettingsSelected--
	}
}

// SettingsMoveDown moves the row selection down within the active category.
func (m *OS) SettingsMoveDown() {
	items := m.settingsCurrentItems()
	if m.SettingsSelected < len(items)-1 {
		m.SettingsSelected++
	}
}

// SettingsNextCategory switches to the next settings tab.
func (m *OS) SettingsNextCategory() {
	cats := m.settingsCategories()
	if m.SettingsCategory < len(cats)-1 {
		m.SettingsCategory++
		m.SettingsSelected = 0
		m.SettingsScroll = 0
	}
}

// SettingsPrevCategory switches to the previous settings tab.
func (m *OS) SettingsPrevCategory() {
	if m.SettingsCategory > 0 {
		m.SettingsCategory--
		m.SettingsSelected = 0
		m.SettingsScroll = 0
	}
}

// SettingsAdjust changes the focused setting by dir (-1 or +1) and persists it.
// Text (controlString) settings are edited inline rather than stepped, so the
// arrow keys are a no-op on them.
func (m *OS) SettingsAdjust(dir int) tea.Cmd {
	items := m.settingsCurrentItems()
	if len(items) == 0 {
		return nil
	}
	item := items[m.SettingsSelected]
	if item.Control == controlString || item.adjust == nil {
		return nil
	}
	item.adjust(m, dir)
	return m.persistSettings()
}

// SettingsActivate runs a setting's activate hook if it has one (e.g. opening
// the theme picker), begins inline editing for a text setting, otherwise
// toggles/advances the value. Bound to Enter.
func (m *OS) SettingsActivate() tea.Cmd {
	items := m.settingsCurrentItems()
	if len(items) == 0 {
		return nil
	}
	item := items[m.SettingsSelected]
	if fn := item.activate; fn != nil {
		fn(m)
		return nil
	}
	if item.Control == controlString {
		m.SettingsBeginEdit()
		return nil
	}
	return m.SettingsAdjust(1)
}

// SettingsEditActive reports whether a text setting is currently being edited.
func (m *OS) SettingsEditActive() bool { return m.SettingsEditing }

// SettingsBeginEdit starts inline editing of the focused text setting, seeding
// the buffer with its current value.
func (m *OS) SettingsBeginEdit() {
	items := m.settingsCurrentItems()
	if len(items) == 0 {
		return
	}
	item := items[m.SettingsSelected]
	if item.Control != controlString {
		return
	}
	m.SettingsEditing = true
	m.SettingsEditBuffer = item.value(m)
}

// SettingsEditAppend adds typed text to the edit buffer.
func (m *OS) SettingsEditAppend(s string) {
	if m.SettingsEditing {
		m.SettingsEditBuffer += s
	}
}

// SettingsEditBackspace removes the last rune from the edit buffer.
func (m *OS) SettingsEditBackspace() {
	if !m.SettingsEditing || m.SettingsEditBuffer == "" {
		return
	}
	r := []rune(m.SettingsEditBuffer)
	m.SettingsEditBuffer = string(r[:len(r)-1])
}

// SettingsEditClear empties the edit buffer.
func (m *OS) SettingsEditClear() {
	if m.SettingsEditing {
		m.SettingsEditBuffer = ""
	}
}

// SettingsEditCancel abandons the edit without applying it.
func (m *OS) SettingsEditCancel() {
	m.SettingsEditing = false
	m.SettingsEditBuffer = ""
}

// SettingsEditCommit applies the edited (trimmed) value to the focused text
// setting and persists it.
func (m *OS) SettingsEditCommit() tea.Cmd {
	if !m.SettingsEditing {
		return nil
	}
	value := strings.TrimSpace(m.SettingsEditBuffer)
	items := m.settingsCurrentItems()
	if len(items) > 0 {
		if set := items[m.SettingsSelected].setStr; set != nil {
			set(m, value)
		}
	}
	m.SettingsEditing = false
	m.SettingsEditBuffer = ""
	return m.persistSettings()
}
