package app

import (
	"image/color"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
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

// setStartup runs fn against the held config's startup section when a config is
// present, so a change to a [startup] setting can be persisted. These settings
// take effect on the next launch, so there is nothing to apply live.
func (m *OS) setStartup(fn func(s *config.StartupConfig)) {
	if m.UserConfig != nil {
		fn(&m.UserConfig.Startup)
	}
}

// setTape runs fn against the held config's [tape] section. Project-tape
// settings are read straight off UserConfig (not appearance globals), so a
// change takes effect on the next detection with no extra apply step.
func (m *OS) setTape(fn func(t *config.TapeConfig)) {
	if m.UserConfig != nil {
		fn(&m.UserConfig.Tape)
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

// tapeAutorunConfigValue returns the configured [tape] autorun mode (not the
// TUIOS_TAPE_AUTORUN env override), for the settings row.
func (m *OS) tapeAutorunConfigValue() string {
	if m.UserConfig != nil && m.UserConfig.Tape.Autorun != "" {
		return m.UserConfig.Tape.Autorun
	}
	return config.TapeAutorunAsk
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

// intItem builds a numeric stepper bound to an int global.
func intItem(label, desc string, lo, hi, step int, get func() int, set func(m *OS, v int)) settingItem {
	return settingItem{
		Label:   label,
		Desc:    desc,
		Control: controlInt,
		value:   func(_ *OS) string { return strconv.Itoa(get()) },
		adjust: func(m *OS, dir int) {
			set(m, clampInt(get()+dir*step, lo, hi))
		},
	}
}

// stringItem builds a free-text field. get reads the current value (nil-safe
// against a missing config), set commits a trimmed value. Editing happens inline
// via the settings input handler; set is called on commit and the change is
// persisted afterward.
func stringItem(label, desc, placeholder, unset string, get func(m *OS) string, set func(m *OS, v string)) settingItem {
	// The example belongs to the description. Rendered in the value's place it
	// was indistinguishable from a value the user had actually set.
	if placeholder != "" {
		desc += ", e.g. " + placeholder
	}
	if unset == "" {
		unset = "(default)"
	}
	return settingItem{
		Label:       label,
		Desc:        desc,
		Control:     controlString,
		Placeholder: placeholder,
		Unset:       unset,
		value:       get,
		setStr:      set,
	}
}

// appearanceString reads a string field off the held appearance config, or ""
// when no config is present (e.g. in unit tests that build a bare OS).
func (m *OS) appearanceString(get func(a *config.AppearanceConfig) string) string {
	if m.UserConfig == nil {
		return ""
	}
	return get(&m.UserConfig.Appearance)
}

// daemonLogLevelOptions lists the daemon debug verbosity levels, lowest first.
var daemonLogLevelOptions = []string{"off", "errors", "basic", "messages", "verbose", "trace"}

// settingsCategories builds the full settings model, binding each row to its
// config global, persisted field, and live-apply behavior.
func (m *OS) settingsCategories() []settingsCategory {
	themeOptions := append([]string{themeNone}, theme.AvailableThemes()...)

	themeItem := enumItem("Theme", "Color theme (press Enter for the picker with previews)", themeOptions,
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
	themeItem.activate = func(m *OS) { m.OpenThemePicker() }

	// The glyph set is the theme's opposite number and sits beside it: the
	// theme says what colour the chrome is and the set says what shape it is.
	// A cycler rather than a picker, because the list is four built-ins plus
	// whatever the user has written rather than several hundred.
	glyphOptions := theme.AvailableGlyphSets()

	appearance := settingsCategory{
		Name: "Appearance",
		Items: []settingItem{
			themeItem,
			enumItem("Glyph set", "Characters the border, controls, rules and rail marks are drawn with",
				glyphOptions,
				func() string { return config.GlyphSet },
				func(m *OS, v string) {
					config.GlyphSet = v
					theme.SetActiveGlyphs(v)
					m.setAppearance(func(a *config.AppearanceConfig) { a.Glyphs = v })
					m.applyAppearanceLive(true)
				}),
			enumItem("Border style", "Window border characters", borderStyleOptions,
				func() string { return config.BorderStyle },
				func(m *OS, v string) {
					config.BorderStyle = v
					m.setAppearance(func(a *config.AppearanceConfig) { a.BorderStyle = v })
					m.applyAppearanceLive(true)
				}),
			enumItem("Window title", "Where window titles are drawn", positionOptions,
				func() string { return config.WindowTitlePosition },
				func(m *OS, v string) {
					config.WindowTitlePosition = v
					m.setAppearance(func(a *config.AppearanceConfig) { a.WindowTitlePosition = v })
					m.applyAppearanceLive(true)
				}),
			boolItem("Shared borders", "Merge borders between tiled panes",
				func() bool { return config.SharedBorders },
				func(m *OS, v bool) {
					config.SharedBorders = v
					m.setAppearance(func(a *config.AppearanceConfig) { a.SharedBorders = boolPtr(v) })
					m.applyAppearanceLive(true)
				}),
			boolItem("Window buttons", "Show minimize/maximize/close buttons",
				func() bool { return !config.HideWindowButtons },
				func(m *OS, v bool) {
					config.HideWindowButtons = !v
					m.setAppearance(func(a *config.AppearanceConfig) { a.HideWindowButtons = !v })
					m.applyAppearanceLive(false)
				}),
			enumItem("Window button style", "pill: glyphs on a filled pill. dots: macOS traffic lights, labelled on hover",
				windowButtonOptions,
				func() string { return config.WindowButtonStyle },
				func(m *OS, v string) {
					config.WindowButtonStyle = v
					m.setAppearance(func(a *config.AppearanceConfig) { a.WindowButtonStyle = v })
					m.applyAppearanceLive(false)
				}),
			enumItem("Window button position", "Which end of the title bar the controls sit on. macOS puts them left",
				windowButtonPosOptions,
				func() string { return config.WindowButtonPosition },
				func(m *OS, v string) {
					config.WindowButtonPosition = v
					m.setAppearance(func(a *config.AppearanceConfig) { a.WindowButtonPosition = v })
					m.applyAppearanceLive(false)
				}),
			boolItem("Scrollbar", "Show where a scrolled-back pane is in its history",
				func() bool { return !config.HideScrollbar },
				func(m *OS, v bool) {
					config.HideScrollbar = !v
					m.setAppearance(func(a *config.AppearanceConfig) { a.HideScrollbar = !v })
					m.applyAppearanceLive(false)
				}),
			enumItem("Scrollbar style", "thin: a hairline thumb. track: a full-height track behind it",
				scrollbarStyleOptions,
				func() string { return config.ScrollbarStyle },
				func(m *OS, v string) {
					config.ScrollbarStyle = v
					m.setAppearance(func(a *config.AppearanceConfig) { a.Scrollbar.Style = v })
					m.applyAppearanceLive(false)
				}),
			// The three colour rows, adjacent and identical in shape. Each opens
			// the picker; none of them is a text field or a cycler any more.
			colorSettingItem("appearance.scrollbar.tint"),
			colorSettingItem("appearance.border_focused_color"),
			colorSettingItem("appearance.border_unfocused_color"),
			stringItem("Window title format", "Template: {title}, {index}, {cwd}", "{index}: {title}", "(raw title)",
				func(m *OS) string { return config.WindowTitleFormat },
				func(m *OS, v string) {
					config.WindowTitleFormat = v
					m.setAppearance(func(a *config.AppearanceConfig) { a.WindowTitleFormat = v })
					m.applyAppearanceLive(false)
				}),
			intItem("Pane gap", "Cells of empty ground between two neighbouring tiled panes",
				0, config.PaneGapMax, 1,
				func() int { return config.PaneGap },
				func(m *OS, v int) {
					config.PaneGap = v
					m.setAppearance(func(a *config.AppearanceConfig) { a.Gap = v })
					m.applyAppearanceLive(true)
				}),
			intItem("Dim unfocused", "Quiet the content of panes you are not in, as a percent; 0 is off",
				0, config.DimUnfocusedMax, 5,
				func() int { return config.DimUnfocused },
				func(m *OS, v int) {
					config.DimUnfocused = v
					m.setAppearance(func(a *config.AppearanceConfig) { a.DimUnfocused = v })
					m.applyAppearanceLive(false)
				}),
			intItem("Panel padding", "Columns each side of an overlay panel's content",
				1, overlay.MaxPanelPadding, 1,
				func() int { return overlay.PanelPadding() },
				func(m *OS, v int) {
					overlay.SetPanelPadding(v)
					m.setAppearance(func(a *config.AppearanceConfig) { a.PanelPadding = v })
					m.applyAppearanceLive(false)
				}),
			enumItem("Zen mode", "Hide borders of unfocused windows: disabled, always, or mouse (reveal while moving)",
				config.ZenModeModes,
				func() string { return config.ZenMode },
				func(m *OS, v string) {
					config.ZenMode = v
					m.setAppearance(func(a *config.AppearanceConfig) { a.ZenMode = v })
					m.applyAppearanceLive(true)
				}),
		},
	}

	dock := settingsCategory{
		Name: "Dock",
		Items: []settingItem{
			enumItem("Dock position", "Where the dock bar sits", positionOptions,
				func() string { return config.DockbarPosition },
				func(m *OS, v string) {
					config.DockbarPosition = v
					m.setAppearance(func(a *config.AppearanceConfig) { a.DockbarPosition = v })
					m.applyAppearanceLive(true)
				}),
			boolItem("Clock", "Show the clock overlay",
				func() bool { return config.ShowClock },
				func(m *OS, v bool) {
					config.ShowClock = v
					m.setAppearance(func(a *config.AppearanceConfig) { a.ShowClock = v })
					m.applyAppearanceLive(false)
				}),
			stringItem("Clock format", "Go time layout the clock is drawn with", "Mon 3:04PM", config.DefaultClockFormat,
				func(_ *OS) string { return config.ClockFormat },
				func(m *OS, v string) {
					config.ClockFormat = v
					m.setAppearance(func(a *config.AppearanceConfig) { a.ClockFormat = v })
					m.applyAppearanceLive(false)
				}),
			boolItem("CPU meter", "Show CPU usage in the dock",
				func() bool { return config.ShowCPU },
				func(m *OS, v bool) {
					config.ShowCPU = v
					m.setAppearance(func(a *config.AppearanceConfig) { a.ShowCPU = v })
					m.applyAppearanceLive(false)
				}),
			boolItem("RAM meter", "Show RAM usage in the dock",
				func() bool { return config.ShowRAM },
				func(m *OS, v bool) {
					config.ShowRAM = v
					m.setAppearance(func(a *config.AppearanceConfig) { a.ShowRAM = v })
					m.applyAppearanceLive(false)
				}),
			boolItem("Workspace tabs", "Show the clickable workspace strip in the dock",
				func() bool { return config.DockWorkspaceTabs },
				func(m *OS, v bool) {
					config.DockWorkspaceTabs = v
					m.setAppearance(func(a *config.AppearanceConfig) { a.DockWorkspaceTabs = boolPtr(v) })
					m.applyAppearanceLive(false)
				}),
			stringItem("Workspace tab format", "Template for each workspace tab: {index}, {name}", "{index}: {name}", "(name only)",
				func(m *OS) string { return config.DockWorkspaceTabFormat },
				func(m *OS, v string) {
					config.DockWorkspaceTabFormat = v
					m.setAppearance(func(a *config.AppearanceConfig) { a.DockWorkspaceTabFormat = v })
					m.applyAppearanceLive(false)
				}),
			boolItem("Workspace name on hover", "Pop a workspace's full name when its pill cut it short",
				func() bool { return config.DockWorkspaceTooltip },
				func(m *OS, v bool) {
					config.DockWorkspaceTooltip = v
					m.setAppearance(func(a *config.AppearanceConfig) { a.DockWorkspaceTooltip = boolPtr(v) })
					m.applyAppearanceLive(false)
				}),
			boolItem("Pill caps", "Powerline caps on the dock's pills instead of flat cells",
				func() bool { return config.DockPillCaps },
				func(m *OS, v bool) {
					config.DockPillCaps = v
					m.setAppearance(func(a *config.AppearanceConfig) { a.DockPillCaps = boolPtr(v) })
					m.applyAppearanceLive(false)
				}),
		},
	}

	// The sidebar rows were appended to Appearance while there were six of them,
	// to keep the tab strip on one row. There are ten now, which buried the rest
	// of Appearance under a scroll; a tab of their own costs the strip a second
	// row, and panelBody already budgets the body against TabRowCount, so that
	// row comes out of the scrolling list rather than out of the viewport.
	sidebar := settingsCategory{
		Name: "Sidebar",
		Items: []settingItem{
			boolItem("Sidebar", "Show the vertical session sidebar",
				func() bool { return config.SidebarEnabled },
				func(m *OS, v bool) {
					config.SidebarEnabled = v
					m.setAppearance(func(a *config.AppearanceConfig) { a.Sidebar.Enabled = boolPtr(v) })
					m.applyAppearanceLive(true)
				}),
			enumItem("Position", "Which edge the sidebar reserves", sidebarPositionOptions,
				func() string { return config.SidebarPosition },
				func(m *OS, v string) {
					config.SidebarPosition = v
					m.setAppearance(func(a *config.AppearanceConfig) { a.Sidebar.Position = v })
					m.applyAppearanceLive(true)
				}),
			intItem("Width", "Preferred sidebar width in columns", 10, 60, 2,
				func() int { return config.SidebarWidth },
				func(m *OS, v int) {
					config.SidebarWidth = v
					m.setAppearance(func(a *config.AppearanceConfig) { a.Sidebar.Width = v })
					m.applyAppearanceLive(true)
				}),
			boolItem("Show windows", "List window rows under the current session",
				func() bool { return config.SidebarShowWindows },
				func(m *OS, v bool) {
					config.SidebarShowWindows = v
					m.setAppearance(func(a *config.AppearanceConfig) { a.Sidebar.ShowWindows = boolPtr(v) })
					m.applyAppearanceLive(false)
				}),
			boolItem("Show glyphs", "Draw agent-state glyphs on sidebar rows",
				func() bool { return config.SidebarShowGlyphs },
				func(m *OS, v bool) {
					config.SidebarShowGlyphs = v
					m.setAppearance(func(a *config.AppearanceConfig) { a.Sidebar.ShowGlyphs = boolPtr(v) })
					m.applyAppearanceLive(false)
				}),
			boolItem("Show counts", "Draw window counts on sidebar session rows",
				func() bool { return config.SidebarShowCounts },
				func(m *OS, v bool) {
					config.SidebarShowCounts = v
					m.setAppearance(func(a *config.AppearanceConfig) { a.Sidebar.ShowCounts = boolPtr(v) })
					m.applyAppearanceLive(false)
				}),
			boolItem("Agents section", "List the panes running an agent, pinned to the rail's bottom",
				func() bool { return config.SidebarShowAgents },
				func(m *OS, v bool) {
					config.SidebarShowAgents = v
					m.setAppearance(func(a *config.AppearanceConfig) { a.Sidebar.ShowAgents = boolPtr(v) })
					m.applyAppearanceLive(false)
				}),
			boolItem("Marquee", "Scroll a hovered row's title when it does not fit",
				func() bool { return config.SidebarMarquee },
				func(m *OS, v bool) {
					config.SidebarMarquee = v
					m.setAppearance(func(a *config.AppearanceConfig) { a.Sidebar.Marquee = boolPtr(v) })
					m.applyAppearanceLive(false)
				}),
			boolItem("Tooltips", "Name icon-only controls on hover",
				func() bool { return config.Tooltips },
				func(m *OS, v bool) {
					config.Tooltips = v
					m.setAppearance(func(a *config.AppearanceConfig) { a.Sidebar.Tooltips = boolPtr(v) })
					m.applyAppearanceLive(false)
				}),
			boolItem("Session colors", "Give each session its own colour on the rail and the switcher",
				func() bool { return config.SessionColors },
				func(m *OS, v bool) {
					config.SessionColors = v
					m.setAppearance(func(a *config.AppearanceConfig) { a.SessionColors = boolPtr(v) })
					m.applyAppearanceLive(false)
				}),
		},
	}

	behavior := settingsCategory{
		Name: "Behavior",
		Items: []settingItem{
			boolItem("Animations", "Animate window transitions",
				func() bool { return config.AnimationsEnabled },
				func(m *OS, v bool) {
					config.AnimationsEnabled = v
					m.setAppearance(func(a *config.AppearanceConfig) { a.AnimationsEnabled = boolPtr(v) })
					m.applyAppearanceLive(false)
				}),
			boolItem("Confirm quit", "Always confirm before quitting",
				func() bool { return config.AlwaysConfirmQuit },
				func(m *OS, v bool) {
					config.AlwaysConfirmQuit = v
					m.setAppearance(func(a *config.AppearanceConfig) { a.ConfirmQuit = boolPtr(v) })
				}),
			boolItem("Which-key", "Show the leader-key hint popup",
				func() bool { return config.WhichKeyEnabled },
				func(m *OS, v bool) {
					config.WhichKeyEnabled = v
					m.setAppearance(func(a *config.AppearanceConfig) { a.WhichKeyEnabled = boolPtr(v) })
				}),
			enumItem("Which-key position", "Corner for the leader-key popup", whichKeyPosOptions,
				func() string { return config.WhichKeyPosition },
				func(m *OS, v string) {
					config.WhichKeyPosition = v
					m.setAppearance(func(a *config.AppearanceConfig) { a.WhichKeyPosition = v })
				}),
			boolItem("Focus follows mouse", "Focus the pane under the cursor without clicking",
				func() bool { return config.FocusFollowsMouse },
				func(m *OS, v bool) {
					config.FocusFollowsMouse = v
					m.setAppearance(func(a *config.AppearanceConfig) { a.FocusFollowsMouse = boolPtr(v) })
				}),
			enumItem("Click to type", "Clicking a pane: single starts typing, double needs two clicks, off only focuses",
				clickToTypeOptions,
				func() string { return config.ClickToType },
				func(m *OS, v string) {
					config.ClickToType = v
					m.setAppearance(func(a *config.AppearanceConfig) { a.ClickToType = v })
				}),
			boolItem("Reverse scroll", "Reverse scroll in the scrolling layout",
				func() bool { return config.NiriReverseScroll },
				func(m *OS, v bool) {
					config.NiriReverseScroll = v
					m.setAppearance(func(a *config.AppearanceConfig) { a.NiriReverseScroll = v })
				}),
			enumItem("Max FPS", "Render frame-rate cap (unlimited uncaps it)", fpsOptions,
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
					config.NormalFPS = fps
					m.setAppearance(func(a *config.AppearanceConfig) { a.MaxFPS = fps })
				}),
			stringItem("Preferred shell", "Shell for new windows (applies to new windows)", "/bin/bash", "(auto-detect)",
				func(m *OS) string {
					return m.appearanceString(func(a *config.AppearanceConfig) string { return a.PreferredShell })
				},
				func(m *OS, v string) {
					m.setAppearance(func(a *config.AppearanceConfig) { a.PreferredShell = v })
				}),
		},
	}

	startup := settingsCategory{
		Name: "Startup",
		Items: []settingItem{
			boolItem("Open default window", "Open a terminal when a session starts empty (next launch)",
				func() bool { return m.UserConfig != nil && m.UserConfig.Startup.OpenDefaultWindow },
				func(m *OS, v bool) {
					m.setStartup(func(s *config.StartupConfig) { s.OpenDefaultWindow = v })
				}),
			boolItem("Start tiled", "Start a new session tiled, not floating (next launch)",
				func() bool { return m.UserConfig != nil && m.UserConfig.Startup.Tiled },
				func(m *OS, v bool) {
					m.setStartup(func(s *config.StartupConfig) { s.Tiled = v })
				}),
			boolItem("Start in terminal mode", "Land in the shell, ready to type (next launch)",
				func() bool { return m.UserConfig != nil && m.UserConfig.Startup.StartInTerminalMode },
				func(m *OS, v bool) {
					m.setStartup(func(s *config.StartupConfig) { s.StartInTerminalMode = v })
				}),
			boolItem("Daemon by default", "Plain 'tuios' attaches to a daemon session; --standalone or TUIOS_NO_DAEMON=1 overrides",
				func() bool { return m.UserConfig != nil && m.UserConfig.Startup.Daemon },
				func(m *OS, v bool) {
					m.setStartup(func(s *config.StartupConfig) { s.Daemon = v })
				}),
		},
	}

	advanced := settingsCategory{
		Name: "Advanced",
		Items: []settingItem{
			intItem("Scrollback lines", "Lines kept per window (applies to new windows)", 100, 100000, 1000,
				func() int { return config.ScrollbackLines },
				func(m *OS, v int) {
					config.ScrollbackLines = v
					m.setAppearance(func(a *config.AppearanceConfig) { a.ScrollbackLines = v })
				}),
			intItem("Scroll lines", "Lines scrolled per mouse wheel notch", 1, 50, 1,
				func() int { return config.ScrollLines },
				func(m *OS, v int) {
					config.ScrollLines = v
					m.setAppearance(func(a *config.AppearanceConfig) { a.ScrollLines = v })
				}),
			boolItem("Copy on select", "Put a mouse selection on the clipboard as soon as it is released",
				func() bool { return config.CopyOnSelect },
				func(m *OS, v bool) {
					config.CopyOnSelect = v
					m.setAppearance(func(a *config.AppearanceConfig) { a.CopyOnSelect = &v })
				}),
			stringItem("Word characters", "Punctuation double-click keeps inside a word (letters and digits always count)", "@-./_~?&=%+#", "(default)",
				func(m *OS) string { return config.WordCharacters },
				func(m *OS, v string) {
					config.WordCharacters = v
					m.setAppearance(func(a *config.AppearanceConfig) { a.WordCharacters = &v })
				}),
			intItem("Zoom width", "Max columns in zoom mode (0 = fullscreen)", 0, 400, 10,
				func() int { return config.ZoomMaxWidth },
				func(m *OS, v int) {
					config.ZoomMaxWidth = v
					m.setAppearance(func(a *config.AppearanceConfig) { a.ZoomMaxWidth = v })
				}),
			boolItem("Show keys overlay", "Show pressed keys as a keycast in the bottom-right corner",
				func() bool { return m.ShowKeys },
				func(m *OS, v bool) {
					m.ShowKeys = v
					m.setDebug(func(d *config.DebugConfig) { d.ShowKeyEvents = v })
				}),
		},
	}

	daemon := settingsCategory{
		Name: "Daemon",
		Items: []settingItem{
			{
				Label:   "Log level",
				Desc:    "Daemon debug log verbosity (applies to the daemon on restart)",
				Control: controlEnum,
				Options: daemonLogLevelOptions,
				value:   func(m *OS) string { return m.daemonLogLevel() },
				adjust: func(m *OS, dir int) {
					next := cycleEnum(daemonLogLevelOptions, m.daemonLogLevel(), dir)
					if m.UserConfig != nil {
						m.UserConfig.Daemon.LogLevel = next
					}
				},
			},
		},
	}

	tape := settingsCategory{
		Name: "Tape",
		Items: []settingItem{
			{
				Label:   "Autorun",
				Desc:    "Project-tape detection: off, ask (passive), or auto (run trusted)",
				Control: controlEnum,
				Options: config.TapeAutorunModes,
				value:   func(m *OS) string { return m.tapeAutorunConfigValue() },
				adjust: func(m *OS, dir int) {
					next := cycleEnum(config.TapeAutorunModes, m.tapeAutorunConfigValue(), dir)
					m.setTape(func(t *config.TapeConfig) { t.Autorun = next })
				},
			},
			{
				Label:   "Auto-open review",
				Desc:    "Open the review dialog automatically on entering a tape directory",
				Control: controlBool,
				boolVal: func(m *OS) bool { return m.UserConfig != nil && m.UserConfig.Tape.AutoReview },
				adjust: func(m *OS, _ int) {
					cur := m.UserConfig != nil && m.UserConfig.Tape.AutoReview
					m.setTape(func(t *config.TapeConfig) { t.AutoReview = !cur })
				},
			},
		},
	}

	return []settingsCategory{appearance, sidebar, dock, behavior, startup, advanced, daemon, tape}
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
