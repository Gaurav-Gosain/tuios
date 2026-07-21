package app

import (
	"strconv"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// settingControl is the kind of editor a setting row uses.
type settingControl int

const (
	controlEnum settingControl = iota // ‹ value › cycler
	controlBool                       // [ on ] / [ off ] toggle
	controlInt                        // ‹ n › numeric stepper
)

// settingItem is one row on the settings page. adjust changes the value by dir
// (-1 or +1 for enum/int, either flips a bool) and applies it live; the input
// handler persists afterward.
type settingItem struct {
	Label   string
	Desc    string
	Control settingControl
	Options []string
	value   func(m *OS) string
	boolVal func(m *OS) bool
	adjust  func(m *OS, dir int)
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

// persistSettings writes the current config to disk. Called after any settings
// change so it survives a restart.
func (m *OS) persistSettings() {
	if m.UserConfig == nil {
		return
	}
	if err := config.SaveUserConfig(m.UserConfig); err != nil {
		m.ShowNotification("Could not save settings: "+err.Error(), "error", 0)
	}
}

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

const themeNone = "none"

var (
	borderStyleOptions = []string{"rounded", "normal", "thick", "double", "block", "outer-half-block", "inner-half-block", "ascii", "hidden"}
	positionOptions    = []string{"bottom", "top", "hidden"}
	whichKeyPosOptions = []string{"bottom-right", "bottom-left", "top-right", "top-left", "center"}
	fpsOptions         = []string{"30", "60", "90", "120", "144", "unlimited"}
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
			m.persistThemeSelection(v)
		})
	themeItem.activate = func(m *OS) { m.OpenThemePicker() }

	appearance := settingsCategory{
		Name: "Appearance",
		Items: []settingItem{
			themeItem,
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
			boolItem("Scrollbar", "Show the scrollbar thumb on the border",
				func() bool { return !config.HideScrollbar },
				func(m *OS, v bool) {
					config.HideScrollbar = !v
					m.setAppearance(func(a *config.AppearanceConfig) { a.HideScrollbar = !v })
					m.applyAppearanceLive(false)
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
			intItem("Zoom width", "Max columns in zoom mode (0 = fullscreen)", 0, 400, 10,
				func() int { return config.ZoomMaxWidth },
				func(m *OS, v int) {
					config.ZoomMaxWidth = v
					m.setAppearance(func(a *config.AppearanceConfig) { a.ZoomMaxWidth = v })
				}),
		},
	}

	return []settingsCategory{appearance, dock, behavior, startup, advanced}
}

// OpenSettings shows the settings overlay, initializing the theme registry so
// the theme list is populated.
func (m *OS) OpenSettings() {
	theme.EnsureRegistry()
	m.ShowSettings = true
	m.SettingsCategory = 0
	m.SettingsSelected = 0
	m.SettingsScroll = 0
}

// CloseSettings hides the settings overlay.
func (m *OS) CloseSettings() {
	m.ShowSettings = false
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
func (m *OS) SettingsAdjust(dir int) {
	items := m.settingsCurrentItems()
	if len(items) == 0 {
		return
	}
	items[m.SettingsSelected].adjust(m, dir)
	m.persistSettings()
}

// SettingsActivate runs a setting's activate hook if it has one (e.g. opening
// the theme picker), otherwise toggles/advances the value. Bound to Enter.
func (m *OS) SettingsActivate() {
	items := m.settingsCurrentItems()
	if len(items) == 0 {
		return
	}
	if fn := items[m.SettingsSelected].activate; fn != nil {
		fn(m)
		return
	}
	m.SettingsAdjust(1)
}
