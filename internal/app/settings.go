package app

import (
	"image/color"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

type settingControl int

const (
	controlEnum settingControl = iota
	controlBool
	controlInt
	controlString
	controlColor
	controlStatus
)

type settingItem struct {
	Path           string
	Label          string
	Aside          string
	Desc           string
	Control        settingControl
	Options        []string
	Placeholder    string
	Unset          string
	value          func(m *OS) string
	boolVal        func(m *OS) bool
	adjust         func(m *OS, dir int)
	setStr         func(m *OS, v string)
	swatch         func(ground color.Color, s *config.Settings) color.Color
	ink            func(pal overlay.Palette) color.Color
	activate       func(m *OS) tea.Cmd
	meter          func(m *OS) float64
	setNum         func(m *OS, v int)
	numMin, numMax int
	derived        bool
	differs        func(m *OS) bool
	reset          func(m *OS)
	resetShown     string
}

type settingsCategory struct {
	Name  string
	Items []settingItem
}

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

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func (m *OS) applyAppearanceLive(retile bool) {
	m.applyFrameRate()
	m.detectDisplayRate(false)
	m.adoptConfigPaneGeometry()
	m.refreshKittyPlaceholderMode()
	m.refreshImageSymbols()
	m.MarkAllDirty()
	if retile && m.AutoTiling {
		m.TileAllWindows()
	}
	m.SyncStateToDaemon()
}

func (m *OS) adoptConfigPaneGeometry() {
	if m.Settings.SharedBorders != m.lastConfigSharedBorders {
		m.lastConfigSharedBorders = m.Settings.SharedBorders
		m.SharedBorders = m.Settings.SharedBorders
	}
	if m.Settings.PaneGap != m.lastConfigPaneGap {
		m.lastConfigPaneGap = m.Settings.PaneGap
		m.PaneGap = m.Settings.PaneGap
	}
	if m.Settings.ScrollColumnWidth != m.lastConfigScrollWidth {
		m.lastConfigScrollWidth = m.Settings.ScrollColumnWidth
		m.ScrollColumnWidth = m.Settings.ScrollColumnWidth
	}
	m.adoptConfigMasterLayout()
}

// ApplyAppearanceLive is applyAppearanceLive for callers outside the package.
// The out-of-process fuzz target (internal/fuzz/apptarget) flips the same
// appearance globals the settings page does and has to land them the same way;
// open-coding the two calls there would be a copy that drifts the moment this
// one grows a third.
func (m *OS) ApplyAppearanceLive(retile bool) { m.applyAppearanceLive(retile) }

func (m *OS) applyTheme(name string) {
	if name == themeNone {
		_ = theme.Initialize(theme.NoneID)
	} else {
		_ = theme.Initialize(name)
	}
	m.UpdateAllWindowThemes()
	m.MarkAllDirty()
}

func (m *OS) applyBorderColors() {
	var overrides theme.BorderOverrides
	if m.UserConfig != nil {
		overrides = m.UserConfig.Appearance.BorderOverrides()
	}
	theme.SetBorderOverrides(overrides)
	m.MarkAllDirty()
}

func (m *OS) persistSettings() tea.Cmd {
	if m.UserConfig == nil {
		return nil
	}
	if m.ConfigReadOnly {
		cmd := m.applyAgentsSwitch()
		if !m.configReadOnlyTold && !m.LearnMode {
			m.configReadOnlyTold = true
			m.ShowNotification("Settings apply to this session only. The config file is not changed.", "warning", 0)
		}
		return cmd
	}
	write, err := config.RenderUserConfig(m.UserConfig)
	if err != nil {
		m.ShowNotification("Could not save settings: "+err.Error(), "error", 0)
		return nil
	}
	apply := m.applyHostsCmd()
	agents := m.applyAgentsSwitch()
	write = m.queueSettingsSave(write)
	save := func() tea.Msg {
		note, err := write()
		if err != nil {
			return settingsSaveFailedMsg{err: err}
		}
		if !note.Empty() {
			said := func() tea.Msg { return settingsSaveRedirectedMsg{note: note} }
			if apply != nil {
				return tea.Batch(said, apply)()
			}
			return said()
		}
		if apply != nil {
			return apply()
		}
		return nil
	}
	if agents == nil {
		return save
	}
	return tea.Batch(agents, save)
}

type settingsSaveFailedMsg struct{ err error }

type settingsSaveRedirectedMsg struct{ note config.WriteNote }

func (m *OS) setAppearance(fn func(a *config.AppearanceConfig)) {
	if m.UserConfig != nil {
		fn(&m.UserConfig.Appearance)
	}
}

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
	m.Settings.FocusFollowsMouse = !m.Settings.FocusFollowsMouse
	m.setAppearance(func(a *config.AppearanceConfig) { a.FocusFollowsMouse = new(m.Settings.FocusFollowsMouse) })
	return m.persistSettings()
}

const themeNone = "none"

var fpsOptions = []string{fpsAutoLabel, "30", "60", "90", "120", "144", "165", "240"}

const fpsAutoLabel = "Auto"

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

func boolItem(label, desc string, get func() bool, set func(m *OS, v bool)) settingItem {
	return settingItem{
		Label:   label,
		Desc:    desc,
		Control: controlBool,
		boolVal: func(_ *OS) bool { return get() },
		adjust:  func(m *OS, _ int) { set(m, !get()) },
	}
}

func (m *OS) settingsCategories() []settingsCategory {
	appearance := settingsCategory{
		Name: "Appearance",
		Items: m.resolveRows([]settingsRow{
			opt("agents.enabled"),
			custom("appearance.theme", m.themeItem()),
			custom("", m.themeEditItem()),
			opt("appearance.theme_light"),
			custom("appearance.glyphs", m.glyphItem()),
			custom("", m.shareLookItem()),
			opt("appearance.border_style"),
			opt("appearance.frame_panels"),
			opt("appearance.pane_overlays"),
			opt("appearance.overlay_border_color"),
			opt("appearance.window_title_position"),
			opt("appearance.window_title_style"),
			opt("appearance.window_title_align"),
			opt("appearance.window_title_format"),
			opt("appearance.host_title"),
			custom("appearance.shared_borders", m.sharedBordersItem()),
			opt("appearance.hide_window_buttons"),
			opt("appearance.window_button_style"),
			opt("appearance.window_button_position"),
			opt("appearance.hide_scrollbar"),
			opt("appearance.scrollbar.style"),
			opt("appearance.scrollbar.tint"),
			opt("appearance.scrollbar.thumb"),
			opt("appearance.scrollbar.track"),
			opt("appearance.border_focused_color"),
			opt("appearance.border_focused_color_window"),
			opt("appearance.border_focused_color_terminal"),
			opt("appearance.border_unfocused_color"),
			opt("appearance.border_focused_gradient"),
			opt("appearance.border_gradient_angle"),
			custom("appearance.gap", m.paneGapItem()),
			opt("appearance.tiling_scheme"),
			opt("appearance.drop_preview"),
			opt("appearance.drop_preview_delay"),
			custom("appearance.master_ratio", m.masterRatioItem()),
			opt("appearance.master_position"),
			opt("appearance.master_count"),
			opt("appearance.master_grid"),
			custom("appearance.scroll_column_width", m.scrollColumnWidthItem()),
			opt("appearance.scroll_column_max"),
			opt("appearance.dim_unfocused"),
			opt("appearance.multi_copy_dim"),
			opt("appearance.copy_search_max_matches"),
			opt("appearance.dim_foreground"),
			opt("appearance.dim_multifocus"),
			opt("appearance.modal_dim"),
			opt("appearance.popup_dim"),
			opt("appearance.panel_padding"),
			opt("appearance.zen_mode"),
			opt("appearance.zen_reveal_ms"),
			opt("appearance.links"),
			opt("appearance.link_click"),
			opt("appearance.link_hover"),
			opt("appearance.link_opener"),
			opt("appearance.session_colors"),
			opt("appearance.session_border"),
		}),
	}

	backgrounds := settingsCategory{
		Name: "Backgrounds",
		Items: m.resolveRows([]settingsRow{
			opt("appearance.background"),
			opt("appearance.pane_background"),
			opt("appearance.desktop_background"),
			opt("appearance.window_chrome_background"),
			opt("appearance.dock_background"),
			opt("appearance.sidebar.background"),
		}),
	}

	sidebar := settingsCategory{
		Name: "Sidebar",
		Items: m.resolveRows([]settingsRow{
			opt("appearance.sidebar.enabled"),
			opt("appearance.sidebar.per_session"),
			opt("appearance.sidebar.position"),
			opt("appearance.sidebar.width"),
			custom("appearance.sidebar.sections", m.sectionLayoutItem()),
			opt("appearance.sidebar.header_case"),
			opt("appearance.sidebar.row_style"),
			opt("appearance.sidebar.header_style"),
			opt("appearance.sidebar.cursor_background"),
			opt("appearance.sidebar.density"),
			opt("appearance.sidebar.show_glyphs"),
			opt("appearance.sidebar.show_counts"),
			opt("appearance.sidebar.show_numbers"),
			opt("appearance.sidebar.file_icons"),
			opt("appearance.sidebar.file_icon_colors"),
			opt("appearance.sidebar.folder_click"),
			opt("appearance.sidebar.file_drag"),
			opt("appearance.sidebar.files_style"),
			opt("appearance.sidebar.editor"),
			opt("appearance.sidebar.file_actions"),
			opt("appearance.sidebar.file_delete"),
			opt("files.open_copy_ask_mb"),
			opt("files.open_copy_max_mb"),
			opt("files.open_copy_keep_days"),
			opt("files.drop_keep_hours"),
			opt("files.drop_keep_mb"),
			opt("appearance.sidebar.group_main"),
			opt("appearance.sidebar.marquee"),
			opt("appearance.sidebar.marquee_pause_ms"),
			opt("appearance.sidebar.marquee_step_ms"),
			opt("appearance.sidebar.peek"),
			opt("appearance.sidebar.tooltips"),
			opt("appearance.sidebar.git_refresh_ms"),
			opt("appearance.git_dirty"),
			opt("appearance.global_session"),
		}),
	}
	if m.agentsOn() {
		sidebar.Items = slices.Insert(sidebar.Items, 4, m.resolveRows([]settingsRow{custom("", m.agentRowItem())})...)
	}
	if m.agentsSeen() {
		sidebar.Items = append(sidebar.Items, m.resolveRows([]settingsRow{
			opt("appearance.sidebar.agent_rest_fold"),
			opt("appearance.sidebar.subagent_rows"),
			opt("appearance.sidebar.subagent_collapse"),
			opt("appearance.sidebar.subagent_fields"),
			opt("appearance.sidebar.subagent_max_rows"),
			opt("appearance.sidebar.subagent_hide_finished"),
		})...)
	}
	left, right := m.sidebarSessionConfig(sidebarLeft), m.sidebarSessionConfig(sidebarRight)
	if left != nil && right != nil && left.Session != "" && right.Session != "" {
		sidebar.Items = nil
	} else if cfg := m.sidebarSessionConfig(m.legacySidebarEdge()); cfg != nil && cfg.Session != "" {
		sidebar.Items = slices.DeleteFunc(sidebar.Items, func(item settingItem) bool {
			return item.Path == "appearance.sidebar.sections"
		})
	}
	sidebar.Items = slices.Insert(sidebar.Items, 0, m.sidebarSessionSettingItems()...)

	selection := settingsCategory{
		Name: "Selection",
		Items: m.resolveRows([]settingsRow{
			opt("appearance.selection.bg"),
			opt("appearance.selection.fg"),
			opt("appearance.selection.bold"),
			opt("appearance.selection.search_bg"),
			opt("appearance.selection.search_fg"),
			opt("appearance.selection.match_bg"),
			opt("appearance.selection.match_fg"),
			opt("appearance.selection.cursor_bg"),
			opt("appearance.selection.cursor_fg"),
			opt("appearance.selection.flash"),
			opt("appearance.selection.flash_ms"),
			opt("appearance.selection.flash_style"),
			opt("appearance.selection.flash_color"),
			opt("appearance.selection.multi_format"),
			opt("appearance.selection.copy_entry"),
			opt("appearance.selection.osc52_write"),
			opt("paste_buffers.limit"),
			opt("paste_buffers.max_kb"),
			opt("hints.builtins"),
			opt("hints.alphabet"),
			opt("hints.open"),
			opt("hints.dim"),
			opt("hints.all_panes"),
			opt("panes.label_keys"),
			opt("panes.navigator_layout"),
			opt("panes.navigator_search"),
			opt("panes.drag_and_drop"),
			opt("panes.drag_and_drop_desktop"),
			opt("path_picker.show_hidden"),
		}),
	}

	dock := settingsCategory{
		Name: "Dock",
		Items: m.resolveRows([]settingsRow{
			opt("appearance.dockbar_position"),
			custom("", m.dockComponentsItem()),
			opt("appearance.show_clock"),
			opt("appearance.clock_format"),
			opt("dock.clock.format"),
			opt("appearance.show_cpu"),
			opt("appearance.show_ram"),
			opt("dock.meter_interval_ms"),
			opt("appearance.dock_workspace_tabs"),
			opt("appearance.dock_workspace_tab_format"),
			opt("appearance.dock_workspace_tooltip"),
			opt("appearance.tooltip_delay_ms"),
			opt("appearance.dock_workspace_label_max"),
			opt("appearance.dock_pill_caps"),
			opt("appearance.dock_prefix_indicator"),
			opt("appearance.dock_mode_icon_window"),
			opt("appearance.dock_mode_icon_terminal"),
			opt("appearance.dock_mode_icon_tiling"),
			opt("appearance.dock_mode_pill"),
			opt("appearance.dock_mode_split_letter"),
			opt("appearance.dock_compact"),
		}),
	}

	behavior := settingsCategory{
		Name: "Behavior",
		Items: m.resolveRows([]settingsRow{
			custom("", m.profileItem()),
			custom("appearance.calm", m.calmItem()),
			opt("appearance.motion"),
			opt("appearance.animation_speed"),
			opt("appearance.theme_transition"),
			opt("appearance.theme_transition_duration"),
			opt("appearance.confirm_close"),
			opt("appearance.confirm_quit"),
			opt("appearance.whichkey_enabled"),
			opt("appearance.whichkey_position"),
			opt("appearance.whichkey_delay_ms"),
			opt("appearance.wrap_lists"),
			opt("appearance.focus_follows_mouse"),
			opt("appearance.focus_follows_mouse_pause"),
			opt("appearance.paste_preview"),
			opt("appearance.paste_preview_ms"),
			opt("keybindings.window_mode"),
			opt("appearance.click_to_type"),
			opt("appearance.auto_enter_terminal_on_focus"),
			opt("appearance.nvim_navigation"),
			opt("appearance.focus_wrap"),
			opt("appearance.alt_drag"),
			opt("appearance.right_click_opens_menu"),
			opt("mouse.double_click_ms"),
			opt("mouse.drag_threshold"),
			opt("appearance.new_window_inherit_cwd"),
			opt("appearance.new_window_follow_ssh"),
			opt("appearance.niri_reverse_scroll"),
			opt("appearance.niri_scroll_cells"),
			opt("appearance.niri_scroll_snap"),
			opt("appearance.niri_click_reveals"),
			opt("appearance.niri_hover_reveals"),
			opt("appearance.prefix_repeat_time"),
			opt("appearance.chord_timeout_ms"),
			custom("appearance.max_fps", m.maxFPSItem()),
			opt("appearance.cell_frames"),
			opt("appearance.idle_fps"),
			opt("appearance.preferred_shell"),
			opt("shell.integration"),
			opt("shell.login"),
			opt("agents.fold_working"),
			opt("agents.answer_advance"),
			opt("agents.view_list"),
			opt("agents.mermaid"),
			opt("agents.name_hint"),
			opt("agents.reply_markers"),
			opt("agents.acp_version"),
			opt("agents.list_width"),
			opt("agents.card_preview"),
			opt("agents.card_preview_width"),
			opt("agents.reply_rows"),
			opt("agents.ask_wait_seconds"),
			opt("agents.undo_window_seconds"),
			opt("review.focus"),
			opt("review.scope"),
			opt("pip.width"),
			opt("pip.height"),
			opt("pip.corner"),
		}),
	}

	alertRows := []settingsRow{
		opt("notifications.duration"),
		opt("notifications.warning_duration"),
		opt("notifications.error_duration"),
		opt("notifications.error_sticky"),
		opt("notifications.rate_limit_ms"),
		opt("notifications.bell_rate_limit_ms"),
		opt("notifications.bell"),
		opt("notifications.style"),
		opt("notifications.level"),
		opt("notifications.log_hidden"),
		opt("notifications.machine_offer"),
	}
	alertRows = append(alertRows, m.machineOfferNeverItems()...)
	if m.agentsOn() {
		alertRows = append(alertRows, custom("", m.agentAlertsGroupItem()))
	}
	if m.agentsOn() && m.agentAlertsOpen() {
		alertRows = append(alertRows, agentAlertRows...)
		alertRows = append(alertRows, m.mailAlertRows()...)
	}
	notifications := settingsCategory{
		Name:  "Alerts",
		Items: m.resolveRows(alertRows),
	}

	var motionRows []settingsRow
	for _, path := range []string{
		"workspace", "workspace_ms", "layout", "layout_ms", "zoom", "zoom_ms", "overlays", "overlays_ms",
		"sidebar", "scroll", "scroll_ms", "copy", "confetti",
	} {
		motionRows = append(motionRows, opt("motion."+path))
	}
	motion := settingsCategory{Name: "Motion", Items: m.resolveRows(motionRows)}

	startup := settingsCategory{
		Name: "Startup",
		Items: m.resolveRows([]settingsRow{
			opt("startup.open_default_window"),
			opt("startup.tiled"),
			opt("startup.layout"),
			opt("startup.start_in_terminal_mode"),
			opt("startup.daemon"),
			opt("startup.session"),
			opt("startup.window"),
		}),
	}

	advanced := settingsCategory{
		Name: "Advanced",
		Items: m.resolveRows([]settingsRow{
			opt("appearance.scrollback_lines"),
			opt("appearance.scrollback_keep_on_clear"),
			opt("appearance.scroll_lines"),
			opt("appearance.copy_on_select"),
			opt("appearance.word_characters"),
			opt("appearance.zoom_size"),
			opt("appearance.zoom_borderless"),
			opt("appearance.zoom_follows_focus"),
			opt("appearance.window_button_zoom"),
			opt("appearance.zoom_max_width"),
			opt("appearance.kitty_placeholders"),
			opt("appearance.image_symbols"),
			custom("debug.show_key_events", m.showKeysItem()),
			opt("debug.show_key_count"),
		}),
	}

	spotlight := settingsCategory{
		Name: "Spotlight",
		Items: m.resolveRows([]settingsRow{
			custom("spotlight.enabled", m.spotlightItem()),
			opt("spotlight.follow"),
			opt("spotlight.radius"),
			opt("spotlight.dim"),
			opt("spotlight.edge"),
			opt("spotlight.shake"),
		}),
	}

	daemon := settingsCategory{
		Name: "Daemon",
		Items: m.resolveRows([]settingsRow{
			opt("daemon.log_level"),
			opt("daemon.window_size"),
			opt("daemon.keep_awake"),
			opt("daemon.client_follows_upgrade"),
			opt("daemon.persist_scrollback"),
			opt("daemon.persist_scrollback_lines"),
			opt("daemon.persist_scrollback_kb"),
			opt("daemon.cleanup_seconds"),
			opt("daemon.pr_poll_seconds"),
			opt("daemon.transfer_keep_seconds"),
			opt("daemon.save_seconds"),
			opt("daemon.exec_timeout_seconds"),
			opt("daemon.reconnect_seconds"),
			opt("daemon.host_heartbeat_seconds"),
			opt("daemon.host_heartbeat_timeout_seconds"),
			opt("daemon.host_call_timeout_seconds"),
			opt("scratch.width"),
			opt("scratch.height"),
			opt("launcher.gui_command"),
			opt("launcher.keep_open"),
			opt("workspaces.return_when_empty"),
			opt("workspaces.new_window_when_empty"),
			opt("worktree.path"),
			opt("worktree.setup"),
			opt("worktree.setup_async"),
		}),
	}

	if m.agentsOn() {
		daemon.Items = append(daemon.Items[:2:2], append(m.resolveRows([]settingsRow{
			opt("daemon.agent_autodetect"),
			opt("daemon.agent_detect_seconds"),
			opt("daemon.agent_stall_seconds"),
			opt("daemon.subagent_fade_seconds"),
			opt("daemon.dispatch_ready_seconds"),
			opt("daemon.resume_agents"),
			opt("tasks.auto_stage"),
			opt("tasks.machine_cap"),
			opt("tasks.attachment_max_mib"),
			opt("tasks.attachments_per_card"),
			opt("tasks.attachment_session_mib"),
		}), daemon.Items[2:]...)...)
	}
	if m.DaemonRestartAvailable() {
		daemon.Items = append([]settingItem{m.restartDaemonItem()}, daemon.Items...)
	}

	tape := settingsCategory{
		Name: "Tape",
		Items: m.resolveRows([]settingsRow{
			opt("tape.autorun"),
			opt("tape.auto_review"),
			opt("tape.show_status"),
			opt("tape.banner_ms"),
			opt("tape.done_linger_ms"),
		}),
	}

	screenshot := settingsCategory{
		Name: "Screenshot",
		Items: m.resolveRows([]settingsRow{
			opt("screenshot.format"),
			opt("screenshot.frame"),
			opt("screenshot.background"),
			opt("screenshot.controls"),
			opt("screenshot.title_format"),
			opt("screenshot.padding"),
			opt("screenshot.radius"),
			opt("screenshot.shadow"),
			opt("screenshot.scale"),
			opt("screenshot.font_family"),
			opt("screenshot.cursor"),
			opt("screenshot.copy"),
			opt("screenshot.preview"),
		}),
	}

	screensaver := settingsCategory{
		Name: "Saver",
		Items: m.resolveRows([]settingsRow{
			opt("screensaver.enabled"),
			opt("screensaver.idle_minutes"),
			custom("screensaver.effect", m.screensaverEffectItem()),
			opt("screensaver.while_busy"),
		}),
	}

	cats := []settingsCategory{
		appearance, backgrounds, sidebar, selection, dock, behavior, motion,
		notifications, startup, screenshot, screensaver, spotlight, advanced, daemon,
		m.hostsCategory(),
	}
	m.agentsPage.tab = -1
	if m.agentsPageAvailable() {
		m.agentsPage.tab = len(cats)
		cats = append(cats, m.agentsCategory())
	}
	return append(cats, tape)
}

func (m *OS) themeItem() settingItem {
	options := append([]string{themeNone}, theme.AvailableThemes()...)
	desc := "Color theme. Press enter to open the picker."
	if m.ConfigReadOnly {
		desc = "Color theme. Press enter to open the picker. Shared with every client on this server."
	}
	item := enumItem("Theme", desc, options,
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
	item.activate = func(m *OS) tea.Cmd { m.OpenThemePicker(); return nil }
	item.differs = differsFromDefault("appearance.theme", func(m *OS) string {
		if m.UserConfig != nil {
			return m.UserConfig.Appearance.Theme
		}
		return theme.CurrentThemeID()
	})
	return item
}

func (m *OS) glyphItem() settingItem {
	options := theme.AvailableGlyphSets()
	item := enumItem("Glyph set", "Shapes for the border, controls, rules and rail marks. Press enter to open the picker.",
		options,
		func() string { return theme.ActiveGlyphSetID() },
		func(m *OS, v string) { m.setOption("appearance.glyphs", v) })
	item.activate = func(m *OS) tea.Cmd { m.OpenGlyphPicker(); return nil }
	item.differs = differsFromDefault("appearance.glyphs", func(*OS) string { return theme.ActiveGlyphSetID() })
	return item
}

func (m *OS) screensaverEffectItem() settingItem {
	item := enumItem("Effect",
		"The animation the screen saver runs. Press enter to open the picker.",
		config.ScreensaverEffects,
		func() string { return m.screensaverConfig().EffectName() },
		func(m *OS, v string) { m.setOption("screensaver.effect", v) })
	item.activate = func(m *OS) tea.Cmd { return m.OpenEffectPicker() }
	item.differs = differsFromDefault("screensaver.effect", func(m *OS) string { return m.screensaverConfig().EffectName() })
	return item
}

func (m *OS) themeEditItem() settingItem {
	return settingItem{
		Label:   "Edit theme",
		Desc:    "Change the colours of the theme in use, and see them on the whole screen. Press enter to open the editor.",
		Control: controlEnum,
		value: func(m *OS) string {
			if id := theme.CurrentThemeID(); id != "" {
				return id
			}
			return themeNone
		},
		activate: func(m *OS) tea.Cmd {
			m.CloseSettings()
			m.OpenThemeEditor(theme.CurrentThemeID(), false, "")
			return nil
		},
	}
}

func (m *OS) shareLookItem() settingItem {
	return settingItem{
		Label:   "Share my look",
		Desc:    "Put your look and a screenshot in the setups discussion on GitHub. Press enter to start.",
		Control: controlEnum,
		value:   func(*OS) string { return "GitHub" },
		activate: func(m *OS) tea.Cmd {
			m.CloseSettings()
			return m.ShareLook("", "", "")
		},
	}
}

func (m *OS) dockComponentsItem() settingItem {
	return settingItem{
		Label:   "Components",
		Desc:    "The parts of the dock, in the order they draw. Press enter to edit.",
		Control: controlEnum,
		value: func(m *OS) string {
			if m.UserConfig == nil {
				return "default"
			}
			n := len(m.UserConfig.Dock.DockList("left")) +
				len(m.UserConfig.Dock.DockList("center")) +
				len(m.UserConfig.Dock.DockList("right"))
			return strconv.Itoa(n) + " placed"
		},
		activate: func(m *OS) tea.Cmd { m.OpenDockEditor(); return nil },
	}
}

func (m *OS) sectionLayoutItem() settingItem {
	return settingItem{
		Path:    "appearance.sidebar.sections",
		Label:   "Sections",
		Desc:    "The rail's sections, in the order it stacks them. Press enter to edit.",
		Control: controlEnum,
		value: func(m *OS) string {
			n := sectionCount(m.sectionEntries(), m.agentsOn())
			if n == 1 {
				return "1 section"
			}
			return strconv.Itoa(n) + " sections"
		},
		activate: func(m *OS) tea.Cmd { m.OpenSectionEditor(); return nil },
		differs: differsFromDefault("appearance.sidebar.sections", func(m *OS) string {
			return m.optionValue("appearance.sidebar.sections")
		}),
	}
}

func (m *OS) agentRowItem() settingItem {
	return settingItem{
		Label:   "Agent row",
		Desc:    "The tokens an agent row shows, and the value rules that colour them. Set them in config.toml under [appearance.sidebar.agent_row].",
		Control: controlEnum,
		value: func(m *OS) string {
			spec := m.sidebarAgentRowSpec()
			if !spec.Custom() {
				return "default"
			}
			tokens := plural.Count(len(spec.Tokens), "token")
			if n := spec.RuleCount(); n > 0 {
				return tokens + ", " + plural.Count(n, "rule")
			}
			return tokens
		},
		activate: func(m *OS) tea.Cmd {
			path, err := config.GetConfigPath()
			if err != nil {
				path = "config.toml"
			}
			m.ShowNotification("Edit [appearance.sidebar.agent_row] in "+path, "info", m.Settings.NotificationDuration*2)
			return nil
		},
	}
}

func (m *OS) maxFPSItem() settingItem {
	current := func(m *OS) string {
		if strings.EqualFold(m.optionValue("appearance.max_fps"), config.FPSAuto) {
			return fpsAutoLabel
		}
		return strconv.Itoa(m.Settings.NormalFPS)
	}
	item := enumItem("Max FPS", "Highest frame rate tuios draws at. Your terminal and monitor can show fewer frames. A value above that is not smoother.", fpsOptions,
		func() string { return current(m) },
		func(m *OS, v string) {
			if v == fpsAutoLabel {
				v = config.FPSAuto
			}
			m.setOption("appearance.max_fps", v)
		})
	item.value = func(m *OS) string {
		v := current(m)
		if v == fpsAutoLabel {
			return fpsAutoLabel + " (" + strconv.Itoa(m.Settings.NormalFPS) + ")"
		}
		return v
	}
	item.differs = differsFromDefault("appearance.max_fps", func(m *OS) string { return m.optionValue("appearance.max_fps") })
	return item
}

func (m *OS) showKeysItem() settingItem {
	item := boolItem("Show keys", "Show each key you press in the bottom right corner.",
		func() bool { return m.ShowKeys },
		func(m *OS, v bool) {
			m.ShowKeys = v
			m.setOption("debug.show_key_events", strconv.FormatBool(v))
		})
	item.differs = differsFromDefault("debug.show_key_events", func(m *OS) string { return strconv.FormatBool(m.ShowKeys) })
	return item
}

var agentAlertRows = []settingsRow{
	opt("notifications.agent.enabled"),
	opt("notifications.agent.notify"),
	opt("notifications.agent.dock"),
	opt("notifications.agent.sound"),
	opt("notifications.agent.sound_mode"),
	opt("notifications.agent.sound_cooldown_seconds"),
	opt("notifications.agent.settle_seconds"),
	opt("notifications.agent.suppress_focused"),
	opt("notifications.agent.quiet_hours"),
	opt("notifications.agent.states.working"),
	opt("notifications.agent.states.idle"),
	opt("notifications.agent.states.done"),
	opt("notifications.agent.states.needs_input"),
	opt("notifications.agent.states.errored"),
}

func (m *OS) mailAlertRows() []settingsRow {
	return []settingsRow{
		custom("notifications.mail.enabled", m.mailAlertItem("notifications.mail.enabled", "Mail alerts",
			"Alert when an agent writes to you or sends a notice.",
			func(p config.MailAlertPolicy) bool { return p.Enabled })),
		custom("notifications.mail.notify", m.mailAlertItem("notifications.mail.notify", "Mail alert on the desktop",
			"Send a desktop notification for mail.",
			func(p config.MailAlertPolicy) bool { return p.Notify })),
		custom("notifications.mail.dock", m.mailAlertItem("notifications.mail.dock", "Mail alert in the dock",
			"Show mail in the dock. Click it to open the thread.",
			func(p config.MailAlertPolicy) bool { return p.Dock })),
		custom("notifications.mail.sound", m.mailAlertItem("notifications.mail.sound", "Mail alert sound",
			"Make a mail alert audible. The agent sound settings say how.",
			func(p config.MailAlertPolicy) bool { return p.Sound })),
		opt("notifications.mail.between_agents"),
	}
}

func (m *OS) mailAlertItem(path, label, desc string, get func(config.MailAlertPolicy) bool) settingItem {
	item := boolItem(label, desc+" Unset follows the agent row.",
		func() bool { return get(m.mailAlertPolicy()) },
		func(m *OS, v bool) { m.setOption(path, strconv.FormatBool(v)) })
	item.differs = func(m *OS) bool { return m.optionValue(path) != "" }
	item.reset = func(m *OS) { m.setOption(path, "") }
	item.resetShown = "the agent row"
	return item
}

func (m *OS) agentAlertsOpen() bool {
	if m.settingsAgentsOpen != nil {
		return *m.settingsAgentsOpen
	}
	return m.agentsSeen()
}

func (m *OS) agentAlertsGroupItem() settingItem {
	return settingItem{
		Label:   "Agents",
		Desc:    "Alerts about agents: which states alert, and how. Folded until an agent has run here; change this row to show or hide them.",
		Control: controlEnum,
		value: func(m *OS) string {
			if m.agentAlertsOpen() {
				return "shown"
			}
			return strconv.Itoa(len(agentAlertRows)+len(m.mailAlertRows())) + " hidden"
		},
		adjust: func(m *OS, _ int) {
			open := !m.agentAlertsOpen()
			m.settingsAgentsOpen = &open
		},
	}
}

func (m *OS) spotlightItem() settingItem {
	item := boolItem("Spotlight", "Light the area around the cursor and dim the rest.",
		func() bool { return m.SpotlightOn() },
		func(m *OS, v bool) {
			m.SetSpotlight(v)
			m.setOption("spotlight.enabled", strconv.FormatBool(v))
		})
	item.differs = differsFromDefault("spotlight.enabled", func(m *OS) string { return strconv.FormatBool(m.SpotlightOn()) })
	return item
}

func (m *OS) calmItem() settingItem {
	item := boolItem("Calm mode", "Stop motion and blinking. Show only messages that need you.",
		func() bool { return m.CalmOn() },
		func(m *OS, v bool) { m.SetCalm(v) })
	item.differs = differsFromDefault("appearance.calm", func(m *OS) string { return strconv.FormatBool(m.CalmOn()) })
	return item
}

// OpenSettings shows the settings overlay, initializing the theme registry so
// the theme list is populated.
//
// It opens where the person left it: the same tab, the same row, and the same
// search if one was open. The page is one a person comes back to while trying
// a change out, and reopening on the first tab sent them looking for the row
// they had just been on. The renderer clamps the remembered place if the rows
// have changed since.
func (m *OS) OpenSettings() {
	theme.EnsureRegistry()
	m.ShowSettings = true
	m.SettingsEditing = false
	m.settingsEdit.Set("")
	if m.settingsSearch.open {
		m.settingsSearch.hits = m.searchSettings(m.settingsCategories(), m.settingsSearch.query.String())
	}
}

// OpenSettingsAt opens the settings overlay on the named category, for the
// entry points that already know which part of the app the user is pointing at.
// The name is resolved against the live category list rather than an index: the
// list is built per call, so a hardcoded index would rot the moment a tab moves.
//
// An entry point that names a tab wants that tab, so a remembered search or
// row gives way to it.
func (m *OS) OpenSettingsAt(category string) {
	m.OpenSettings()
	for i, c := range m.settingsCategories() {
		if c.Name == category {
			m.settingsSearch = settingsSearchState{}
			m.SettingsCategory = i
			m.settingsList.Cursor = 0
			m.settingsList.Scroll = 0
			return
		}
	}
}

// OpenSettingsAtRow opens the settings page on one row of one tab: where a
// command palette result for a setting goes.
func (m *OS) OpenSettingsAtRow(category, row int) {
	m.OpenSettings()
	m.settingsSearch = settingsSearchState{}
	m.SettingsCategory = category
	m.settingsList.Cursor = row
	m.settingsList.Scroll = 0
}

// SettingsSetCategory switches to tab i, when there is one. The number keys
// come here.
func (m *OS) SettingsSetCategory(i int) {
	if i < 0 || i >= len(m.settingsCategories()) || i == m.SettingsCategory {
		return
	}
	m.SettingsCategory = i
	m.settingsList.Cursor = 0
	m.settingsList.Scroll = 0
}

// CloseSettings hides the settings overlay.
func (m *OS) CloseSettings() {
	m.ShowSettings = false
	m.SettingsEditing = false
	m.settingsEdit.Set("")
}

func (m *OS) settingsCurrentItems() []settingItem {
	cats := m.settingsCategories()
	if len(cats) == 0 {
		return nil
	}
	m.SettingsCategory = clampInt(m.SettingsCategory, 0, len(cats)-1)
	items := cats[m.SettingsCategory].Items
	if m.settingsSearch.open {
		items, _ = m.settingsSearchRows(cats)
	}
	if len(items) > 0 {
		m.settingsList.Cursor = clampInt(m.settingsList.Cursor, 0, len(items)-1)
	} else {
		m.settingsList.Cursor = 0
	}
	return items
}

// SettingsMove moves the row selection by delta under the list rule: one step
// off either end wraps, a page or a jump stops at the end. The renderer keeps
// the selection in view.
func (m *OS) SettingsMove(delta int) {
	items := m.settingsCurrentItems()
	m.settingsList.Cursor = m.listStep(m.settingsList.Cursor, delta, len(items))
}

// SettingsCursor is the settings row under the cursor.
func (m *OS) SettingsCursor() int { return m.settingsList.Cursor }

// SettingsRowCount is how many rows the list shows now.
func (m *OS) SettingsRowCount() int { return len(m.settingsCurrentItems()) }

// SettingsPageRows is how many rows a page key moves the settings list by:
// the rows the panel shows.
func (m *OS) SettingsPageRows() int {
	_, _, rows, _, _ := m.settingsLayout(m.settingsCategories())
	return max(rows, 1)
}

// SettingsNextCategory switches to the next settings tab, wrapping from the
// last to the first under the list rule.
func (m *OS) SettingsNextCategory() { m.settingsStepCategory(1) }

// SettingsPrevCategory switches to the previous settings tab, wrapping from
// the first to the last under the list rule.
func (m *OS) SettingsPrevCategory() { m.settingsStepCategory(-1) }

func (m *OS) settingsStepCategory(delta int) {
	cats := m.settingsCategories()
	next := m.listStep(m.SettingsCategory, delta, len(cats))
	if next == m.SettingsCategory {
		return
	}
	m.SettingsCategory = next
	m.settingsList.Cursor = 0
	m.settingsList.Scroll = 0
}

// SettingsAdjust changes the focused setting by dir (-1 or +1) and persists it.
// Text (controlString) settings are edited inline rather than stepped, so the
// arrow keys are a no-op on them.
func (m *OS) SettingsAdjust(dir int) tea.Cmd {
	items := m.settingsCurrentItems()
	if len(items) == 0 {
		return nil
	}
	item := items[m.settingsList.Cursor]
	if item.Control == controlString || item.adjust == nil {
		return nil
	}
	m.settingsRecord(item, func() { item.adjust(m, dir) })
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
	item := items[m.settingsList.Cursor]
	if fn := item.activate; fn != nil {
		return fn(m)
	}
	if item.Control == controlString || m.settingsRowTakesANumber(item) {
		m.SettingsBeginEdit()
		return nil
	}
	return m.SettingsAdjust(1)
}

func (m *OS) settingsRowTakesANumber(item settingItem) bool {
	return item.Control == controlInt && item.setNum != nil && item.numMax > item.numMin
}

// SettingsEditingNumber reports whether the open editor is the number one, so
// the renderer knows to draw a slider and the input handler knows that the
// arrow keys move a value rather than nothing.
func (m *OS) SettingsEditingNumber() bool {
	if !m.SettingsEditing {
		return false
	}
	item, ok := m.settingsSelectedItem()
	return ok && m.settingsRowTakesANumber(item)
}

func (m *OS) settingsSelectedItem() (settingItem, bool) {
	items := m.settingsCurrentItems()
	if m.settingsList.Cursor < 0 || m.settingsList.Cursor >= len(items) {
		return settingItem{}, false
	}
	return items[m.settingsList.Cursor], true
}

// SettingsEditNumberRange is the ends the open number editor validates and
// slides between, and whether there is one open at all.
func (m *OS) SettingsEditNumberRange() (lo, hi int, ok bool) {
	item, found := m.settingsSelectedItem()
	if !found || !m.SettingsEditingNumber() {
		return 0, 0, false
	}
	return item.numMin, item.numMax, true
}

// SettingsEditNumberValue is the number in the editor's buffer, clamped to the
// row's range, and whether the buffer holds a number at all.
//
// An empty buffer is not a number and not an error either: it is what is left
// after clearing the field to type a new one, and the slider keeps showing the
// value the setting still has while that happens.
func (m *OS) SettingsEditNumberValue() (int, bool) {
	lo, hi, ok := m.SettingsEditNumberRange()
	if !ok {
		return 0, false
	}
	v, err := strconv.Atoi(strings.TrimSpace(m.settingsEdit.String()))
	if err != nil {
		return 0, false
	}
	return clampInt(v, lo, hi), true
}

// SettingsEditNumberSlide moves the number in the editor by one step and
// rewrites the buffer, which is the slider half of the editor.
//
// It works on the buffer rather than on the setting, so a slide and a typed
// digit are edits to the same thing and Esc abandons both together.
func (m *OS) SettingsEditNumberSlide(dir int) {
	item, found := m.settingsSelectedItem()
	if !found || !m.SettingsEditingNumber() {
		return
	}
	cur, ok := m.SettingsEditNumberValue()
	if !ok {
		cur = settingsNumberOf(item.value(m))
	}
	step := 1
	if span := item.numMax - item.numMin; span > 200 {
		step = span / 100
	}
	m.settingsEdit.Set(strconv.Itoa(clampInt(cur+dir*step, item.numMin, item.numMax)))
}

func settingsNumberOf(text string) int {
	end := 0
	for end < len(text) && (text[end] == '-' || (text[end] >= '0' && text[end] <= '9')) {
		end++
	}
	v, err := strconv.Atoi(text[:end])
	if err != nil {
		return 0
	}
	return v
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
	item := items[m.settingsList.Cursor]
	if item.Control != controlString && !m.settingsRowTakesANumber(item) {
		return
	}
	m.SettingsEditing = true
	m.settingsEdit.Set(item.value(m))
	if item.Control == controlInt {
		m.settingsEdit.Set(strconv.Itoa(settingsNumberOf(m.settingsEdit.String())))
	}
}

// SettingsEditAppend types text at the edit field's caret, as a paste does.
// A number field takes digits and a minus only.
func (m *OS) SettingsEditAppend(s string) {
	if m.SettingsEditing {
		lineInsert(&m.settingsEdit, s, m.settingsEditClean)
	}
}

// SettingsEditKey applies one key press to the edit field (see lineKey). A
// number field takes digits and a minus only: a number editor that accepts
// letters is one that refuses the value on enter for a reason the person
// cannot see while typing it.
func (m *OS) SettingsEditKey(key, text string) {
	if m.SettingsEditing {
		lineKey(&m.settingsEdit, key, text, m.settingsEditClean)
	}
}

// SettingsEditClear empties the edit field.
func (m *OS) SettingsEditClear() {
	if m.SettingsEditing {
		m.settingsEdit.Set("")
	}
}

func (m *OS) settingsEditClean(text string) string {
	text = cleanControl(text)
	if m.SettingsEditingNumber() {
		for _, r := range text {
			if (r < '0' || r > '9') && r != '-' {
				return ""
			}
		}
	}
	return text
}

// SettingsEditCancel abandons the edit without applying it.
func (m *OS) SettingsEditCancel() {
	m.SettingsEditing = false
	m.settingsEdit.Set("")
}

// SettingsEditCommit applies the edited (trimmed) value to the focused text
// setting and persists it.
func (m *OS) SettingsEditCommit() tea.Cmd {
	if !m.SettingsEditing {
		return nil
	}
	value := strings.TrimSpace(m.settingsEdit.String())
	items := m.settingsCurrentItems()
	if len(items) > 0 {
		item := items[m.settingsList.Cursor]
		m.settingsRecord(item, func() {
			switch {
			case m.settingsRowTakesANumber(item):
				if v, err := strconv.Atoi(value); err == nil {
					item.setNum(m, clampInt(v, item.numMin, item.numMax))
				}
			case item.setStr != nil:
				item.setStr(m, value)
			}
		})
	}
	m.SettingsEditing = false
	m.settingsEdit.Set("")
	return m.persistSettings()
}
