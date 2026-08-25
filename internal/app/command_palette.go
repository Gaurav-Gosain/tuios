package app

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/pkg/fuzzy"
)

// ConfigReloadedMsg carries a config parsed by the file watcher goroutine so it
// can be applied on the Bubble Tea goroutine. The watcher must not touch the
// appearance globals directly (the render loop reads them concurrently); it
// delivers this message via the program's Send instead, and Update applies it
// with config.ApplyAppearanceConfig.
type ConfigReloadedMsg struct {
	Config *config.UserConfig
}

// CommandPaletteItem represents a single command in the command palette.
type CommandPaletteItem struct {
	Name string // Display name: "Split horizontal"
	// Shortcut is the row's right-hand meta slot, in muted ink. For a command it
	// is the key that runs it ("prefix+v"); for a row whose consequence is
	// heavier than the palette's usual jump it is that consequence in words
	// ("switches session"), since a row with no key still owes the user a warning.
	Shortcut string
	Category string // "Window", "Layout", "Session", "Navigation"
	// Match holds the byte offsets in Name that the live query matched, filled
	// in by FilterCommandPalette so the renderer can underline them without
	// running the matcher a second time. Nil when nothing was typed, and for a
	// row admitted on its Category alone.
	Match []int
	// AgentState marks a session/window entry whose Name carries an agent-state
	// glyph (sessionPaletteLabel), so the renderer can color the glyph without
	// putting ANSI into Name, which the fuzzy filter matches raw.
	AgentState string
	Action     func(m *OS) (*OS, tea.Cmd)
}

// GetCommandPaletteItems returns all available commands for the command palette.
func GetCommandPaletteItems() []CommandPaletteItem {
	return []CommandPaletteItem{
		// The launcher is its own overlay, and this is the row that opens it.
		// It is the bridge that keeps "one box finds everything" true as an
		// entry point without the two lists having to be ranked against each
		// other (see launcher.go).
		{
			Name:     "Run a program",
			Shortcut: "alt+space",
			Category: PaletteCategoryRun,
			Action: func(m *OS) (*OS, tea.Cmd) {
				return m, m.OpenLauncher()
			},
		},

		// Screenshots. Three rows, because the three things a person means by
		// "take a screenshot" want different gestures and only the first needs
		// one.
		{
			Name:     "Screenshot this window",
			Shortcut: "prefix+C",
			Category: "Window",
			Action: func(m *OS) (*OS, tea.Cmd) {
				return m, m.ScreenshotFocusedWindow()
			},
		},
		{
			Name:     "Screenshot a region",
			Shortcut: "drag to select",
			Category: "Window",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.BeginCapture(false)
				return m, nil
			},
		},
		{
			Name:     "Screenshot the screen",
			Category: "Window",
			Action: func(m *OS) (*OS, tea.Cmd) {
				return m, m.ScreenshotScreen()
			},
		},

		// Window management
		{
			Name:     "New window",
			Shortcut: "prefix+c",
			Category: "Window",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.AddWindow("")
				return m, nil
			},
		},
		{
			Name:     "Close window",
			Shortcut: "prefix+x",
			Category: "Window",
			Action: func(m *OS) (*OS, tea.Cmd) {
				if len(m.Windows) > 0 && m.FocusedWindow >= 0 {
					m.DeleteWindow(m.FocusedWindow)
				}
				return m, nil
			},
		},
		{
			Name:     "Rename window",
			Shortcut: "prefix+r",
			Category: "Window",
			Action: func(m *OS) (*OS, tea.Cmd) {
				// The editor is a centred dialog, so hidden titles are no reason
				// for the palette to offer a row that does nothing.
				if focused := m.GetFocusedWindow(); focused != nil {
					m.Mode = WindowManagementMode
					m.BeginRenameWindow(focused)
				}
				return m, nil
			},
		},
		{
			Name:     "Toggle zoom",
			Shortcut: "prefix+z",
			Category: "Window",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.ToggleZoom()
				return m, nil
			},
		},
		{
			Name:     "Minimize window",
			Shortcut: "prefix+m m",
			Category: "Window",
			Action: func(m *OS) (*OS, tea.Cmd) {
				if len(m.Windows) > 0 && m.FocusedWindow >= 0 {
					focusedWindow := m.GetFocusedWindow()
					if focusedWindow != nil && !focusedWindow.Minimized {
						m.MinimizeWindow(m.FocusedWindow)
					}
				}
				return m, nil
			},
		},
		{
			Name:     "Restore all minimized",
			Shortcut: "prefix+m M",
			Category: "Window",
			Action: func(m *OS) (*OS, tea.Cmd) {
				for i := range m.Windows {
					if m.Windows[i].Minimized && m.Windows[i].Workspace == m.CurrentWorkspace {
						m.RestoreWindow(i)
					}
				}
				if m.AutoTiling {
					m.TileAllWindows()
				}
				return m, nil
			},
		},

		// Layout
		{
			Name:     "Toggle tiling",
			Shortcut: "prefix+space",
			Category: "Layout",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.AutoTiling = !m.AutoTiling
				if m.AutoTiling {
					m.TileAllWindows()
					m.ShowNotification("Tiling on", "success", config.NotificationDuration)
				} else {
					m.ShowNotification("Tiling off", "info", config.NotificationDuration)
				}
				m.FireLayoutChanged()
				return m, nil
			},
		},
		{
			Name:     "Split horizontal",
			Shortcut: "prefix+-",
			Category: "Layout",
			Action: func(m *OS) (*OS, tea.Cmd) {
				if m.AutoTiling {
					m.SplitFocusedHorizontal()
					m.ShowNotification("Split horizontal", "info", config.NotificationDuration)
				}
				return m, nil
			},
		},
		{
			Name:     "Split vertical",
			Shortcut: "prefix+|",
			Category: "Layout",
			Action: func(m *OS) (*OS, tea.Cmd) {
				if m.AutoTiling {
					m.SplitFocusedVertical()
					m.ShowNotification("Split vertical", "info", config.NotificationDuration)
				}
				return m, nil
			},
		},
		{
			Name:     "Smart split",
			Shortcut: "",
			Category: "Layout",
			Action: func(m *OS) (*OS, tea.Cmd) {
				if m.AutoTiling {
					m.SmartSplitFocused()
					m.ShowNotification("Smart split", "info", config.NotificationDuration)
				}
				return m, nil
			},
		},
		{
			Name:     "Toggle shared borders",
			Category: "Layout",
			Action: func(m *OS) (*OS, tea.Cmd) {
				// Flip the session's value, not the config global: after a
				// peer's setting has been adopted the two can differ, and the
				// toggle has to move the one the layout is using.
				m.SetSharedBordersSetting(!m.SharedBorders)
				save := m.persistSettings()
				if m.SharedBorders {
					m.ShowNotification("Shared borders on", "success", config.NotificationDuration)
				} else {
					m.ShowNotification("Shared borders off", "info", config.NotificationDuration)
				}
				return m, save
			},
		},
		{
			Name:     "Rotate split",
			Shortcut: "prefix+R",
			Category: "Layout",
			Action: func(m *OS) (*OS, tea.Cmd) {
				if m.AutoTiling {
					m.RotateFocusedSplit()
					m.ShowNotification("Split rotated", "info", config.NotificationDuration)
				}
				return m, nil
			},
		},
		{
			Name:     "Equalize splits",
			Shortcut: "prefix+=",
			Category: "Layout",
			Action: func(m *OS) (*OS, tea.Cmd) {
				if m.AutoTiling {
					m.EqualizeSplits()
					m.ShowNotification("Splits equalized", "info", config.NotificationDuration)
				}
				return m, nil
			},
		},
		{
			Name:     "Snap fullscreen",
			Shortcut: "prefix+z",
			Category: "Layout",
			Action: func(m *OS) (*OS, tea.Cmd) {
				if !m.AutoTiling && len(m.Windows) > 0 && m.FocusedWindow >= 0 {
					m.Snap(m.FocusedWindow, SnapFullScreen)
				}
				return m, nil
			},
		},

		// Layout templates
		{
			Name:     "Save layout",
			Shortcut: "",
			Category: "Layout",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.ShowLayoutPicker = true
				m.LayoutPickerMode = "save"
				m.LayoutSaveBuffer = ""
				return m, nil
			},
		},
		{
			Name:     "Load layout",
			Shortcut: "prefix+L",
			Category: "Layout",
			Action: func(m *OS) (*OS, tea.Cmd) {
				templates, _ := LoadLayoutTemplates()
				m.ShowLayoutPicker = true
				m.LayoutPickerMode = "load"
				m.LayoutPickerItems = templates
				m.LayoutPickerQuery = ""
				m.LayoutPickerSelected = 0
				m.LayoutPickerScroll = 0
				return m, nil
			},
		},

		// Navigation
		{
			Name:     "Next window",
			Shortcut: "prefix+n",
			Category: "Navigation",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.CycleToNextVisibleWindow()
				return m, nil
			},
		},
		{
			Name:     "Previous window",
			Shortcut: "prefix+p",
			Category: "Navigation",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.CycleToPreviousVisibleWindow()
				return m, nil
			},
		},
		{
			Name:     "Workspace 1",
			Shortcut: "prefix+w 1",
			Category: "Navigation",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.SwitchToWorkspace(1)
				return m, nil
			},
		},
		{
			Name:     "Workspace 2",
			Shortcut: "prefix+w 2",
			Category: "Navigation",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.SwitchToWorkspace(2)
				return m, nil
			},
		},
		{
			Name:     "Workspace 3",
			Shortcut: "prefix+w 3",
			Category: "Navigation",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.SwitchToWorkspace(3)
				return m, nil
			},
		},
		{
			Name:     "Workspace 4",
			Shortcut: "prefix+w 4",
			Category: "Navigation",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.SwitchToWorkspace(4)
				return m, nil
			},
		},
		{
			Name:     "Workspace 5",
			Shortcut: "prefix+w 5",
			Category: "Navigation",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.SwitchToWorkspace(5)
				return m, nil
			},
		},
		{
			Name:     "Workspace 6",
			Shortcut: "prefix+w 6",
			Category: "Navigation",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.SwitchToWorkspace(6)
				return m, nil
			},
		},
		{
			Name:     "Workspace 7",
			Shortcut: "prefix+w 7",
			Category: "Navigation",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.SwitchToWorkspace(7)
				return m, nil
			},
		},
		{
			Name:     "Workspace 8",
			Shortcut: "prefix+w 8",
			Category: "Navigation",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.SwitchToWorkspace(8)
				return m, nil
			},
		},
		{
			Name:     "Workspace 9",
			Shortcut: "prefix+w 9",
			Category: "Navigation",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.SwitchToWorkspace(9)
				return m, nil
			},
		},

		// Layout
		{
			Name:     "Next layout",
			Category: "Layout",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.NextLayout()
				return m, nil
			},
		},
		{
			Name:     "Previous layout",
			Category: "Layout",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.PrevLayout()
				return m, nil
			},
		},
		{
			Name:     "Toggle multifocus",
			Category: "Window",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.ToggleMultifocus(m.FocusedWindow)
				return m, nil
			},
		},
		{
			Name:     "Clear multifocus",
			Category: "Window",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.ClearMultifocus()
				return m, nil
			},
		},
		// Layout mode  - individual commands
		{
			Name:     "Layout: BSP tiling",
			Category: "Layout",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.EnableBSPLayout()
				return m, nil
			},
		},
		{
			Name:     "Layout: master-stack",
			Category: "Layout",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.EnableMasterStackLayout()
				return m, nil
			},
		},
		{
			Name:     "Layout: scrolling (niri-style)",
			Category: "Layout",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.EnableScrollingLayout()
				return m, nil
			},
		},
		{
			Name:     "Layout: disable tiling",
			Category: "Layout",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.DisableAllTiling()
				return m, nil
			},
		},
		// Scrolling-specific actions
		{
			Name:     "Scroll: cycle column width",
			Category: "Layout",
			Action: func(m *OS) (*OS, tea.Cmd) {
				if m.UseScrollingLayout {
					m.ScrollingCycleWidth()
				}
				return m, nil
			},
		},
		{
			Name:     "Scroll: move window into the column below",
			Category: "Layout",
			Action: func(m *OS) (*OS, tea.Cmd) {
				if m.UseScrollingLayout {
					m.ScrollingConsumeWindow()
				}
				return m, nil
			},
		},
		{
			Name:     "Scroll: move window out to its own column",
			Category: "Layout",
			Action: func(m *OS) (*OS, tea.Cmd) {
				if m.UseScrollingLayout {
					m.ScrollingExpelWindow()
				}
				return m, nil
			},
		},
		// Scrollback
		{
			Name:     "Edit scrollback in $EDITOR",
			Category: "Window",
			Action: func(m *OS) (*OS, tea.Cmd) {
				return m, m.EditScrollbackInEditor()
			},
		},
		// Floating
		{
			Name:     "Toggle floating",
			Category: "Window",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.ToggleFloating()
				return m, nil
			},
		},
		// Navigation
		{
			// No Shortcut: the aggregate view has no default binding and is
			// reached from this palette (or a user binding) only.
			Name:     "All windows",
			Category: "Navigation",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.ShowAggregateView = true
				m.AggregateViewQuery = ""
				m.AggregateViewSelected = 0
				m.AggregateViewScroll = 0
				return m, nil
			},
		},
		// Session & Config
		{
			Name:     "Settings",
			Shortcut: "prefix+,",
			Category: "Session",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.OpenSettings()
				return m, nil
			},
		},
		{
			Name:     "Theme picker",
			Category: "Session",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.OpenThemePicker()
				return m, nil
			},
		},
		{
			Name:     "Reload config",
			Category: "Session",
			Action: func(m *OS) (*OS, tea.Cmd) {
				configPath, err := config.GetConfigPath()
				if err != nil {
					m.ShowNotification("Config path error: "+err.Error(), "error", 0)
					return m, nil
				}
				newCfg, err := config.ReloadConfig(configPath)
				if err != nil {
					m.ShowNotification("Config error: "+err.Error(), "error", 0)
					return m, nil
				}
				// Runs on the Bubble Tea goroutine, so applying the appearance
				// globals here is single-threaded and takes effect immediately.
				config.ApplyAppearanceConfig(newCfg)
				// Land the globals the same way the file watcher does: a reload
				// that moved the pane gap or the sidebar has to retile, not just
				// repaint.
				m.applyAppearanceLive(true)
				m.AnnounceLayoutReserve()
				m.ShowNotification("Config reloaded", "success", 0)
				return m, nil
			},
		},
		{
			Name:     "Toggle sidebar",
			Shortcut: "prefix+b",
			Category: "Session",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.ToggleSidebar()
				state := "off"
				if config.SidebarEnabled {
					state = "on"
				}
				m.ShowNotification("Sidebar "+state, "success", config.NotificationDuration)
				return m, nil
			},
		},
		{
			Name:     "Toggle focus follows mouse",
			Category: "Session",
			Action: func(m *OS) (*OS, tea.Cmd) {
				save := m.ToggleFocusFollowsMouse()
				state := "off"
				if config.FocusFollowsMouse {
					state = "on"
				}
				m.ShowNotification("Focus follows mouse "+state, "success", config.NotificationDuration)
				return m, save
			},
		},
		{
			Name:     "Switch session",
			Shortcut: "prefix+S",
			Category: "Session",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.OpenSessionSwitcher()
				return m, nil
			},
		},
		{
			Name:     "Show help",
			Shortcut: "prefix+?",
			Category: "Session",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.ShowHelp = !m.ShowHelp
				if m.ShowHelp {
					m.HelpScrollOffset = 0
				}
				return m, nil
			},
		},
		{
			Name:     "Show logs",
			Shortcut: "prefix+D l",
			Category: "Session",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.ShowLogs = !m.ShowLogs
				return m, nil
			},
		},
		{
			Name:     "Toggle scrollback browser",
			Shortcut: "prefix+s",
			Category: "Session",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.ShowScrollbackBrowser = !m.ShowScrollbackBrowser
				return m, nil
			},
		},
		{
			Name:     "Toggle show keys",
			Shortcut: "prefix+D k",
			Category: "Session",
			Action: func(m *OS) (*OS, tea.Cmd) {
				save := m.ToggleShowKeys()
				state := "off"
				if m.ShowKeys {
					state = "on"
				}
				m.ShowNotification("Show keys "+state, "success", config.NotificationDuration)
				return m, save
			},
		},
		{
			Name:     "Toggle animations",
			Shortcut: "prefix+D a",
			Category: "Session",
			Action: func(m *OS) (*OS, tea.Cmd) {
				config.AnimationsEnabled = !config.AnimationsEnabled
				if config.AnimationsEnabled {
					m.ShowNotification("Animations on", "success", config.NotificationDuration)
				} else {
					m.ShowNotification("Animations off", "info", config.NotificationDuration)
				}
				return m, nil
			},
		},
		{
			Name:     "Window management mode",
			Shortcut: "prefix+esc",
			Category: "Session",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.Mode = WindowManagementMode
				m.ShowNotification("Window management mode", "info", config.NotificationDuration)
				if focusedWindow := m.GetFocusedWindow(); focusedWindow != nil {
					focusedWindow.InvalidateCache()
				}
				return m, nil
			},
		},
		{
			Name:     "Tape: review the project tape",
			Shortcut: "prefix+T t",
			Category: "Session",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.OpenTapeReview()
				return m, nil
			},
		},
		{
			Name:     "Open the tape manager",
			Shortcut: "prefix+T m",
			Category: "Session",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.ToggleTapeManager()
				return m, nil
			},
		},
		{
			Name:     "Enter copy mode",
			Shortcut: "prefix+[",
			Category: "Session",
			Action: func(m *OS) (*OS, tea.Cmd) {
				if focusedWindow := m.GetFocusedWindow(); focusedWindow != nil {
					focusedWindow.EnterCopyMode()
					m.ShowNotification("Copy mode (hjkl, q to exit)", "info", config.NotificationDuration*2)
				}
				return m, nil
			},
		},
	}
}

// paletteStateTokens are the states a leading "@" token narrows the palette to.
// Resolved by prefix in this order, so "@a" is attention, "@w" working, "@n"
// needs input, "@d" done, "@i" idle and "@e" errored: one keystroke past the
// "@", which is what makes "who needs me" worth typing at all.
//
// attention is first and is the reason the mechanism exists. It is the rail's
// own definition of a state that wants a human (sidebarAttention), so the
// palette and the rail's gutter mark can never come to mean different things.
var paletteStateTokens = []struct {
	name   string
	states []string
}{
	{"attention", []string{"needs_input", "errored"}},
	{"working", []string{"working"}},
	{"needs_input", []string{"needs_input"}},
	{"done", []string{"done"}},
	{"idle", []string{"idle"}},
	{"errored", []string{"errored"}},
}

// splitPaletteQuery pulls a leading "@state" token off a query and reports the
// states it admits, whether one was present at all, and the text left to match
// on. A bare "@" names no state and admits every entry that carries one, which
// is the halfway house the user is in while typing the next character.
//
// An "@" naming nothing (a typo, or a state this build does not have) admits
// nothing: a filter the user typed and that quietly did not apply is worse than
// an empty list, which at least says so.
func splitPaletteQuery(query string) (states []string, filtered bool, rest string) {
	tok, after, _ := strings.Cut(strings.TrimSpace(query), " ")
	if !strings.HasPrefix(tok, "@") {
		return nil, false, query
	}
	rest = strings.TrimSpace(after)
	name := strings.ToLower(tok[1:])
	if name == "" {
		return nil, true, rest
	}
	for _, t := range paletteStateTokens {
		if strings.HasPrefix(t.name, name) {
			return t.states, true, rest
		}
	}
	return []string{}, true, rest
}

// paletteStateMatches reports whether an entry survives a state filter. Only the
// session and window entries carry a state, so filtering by one is also what
// drops every static command: "@attention" is a question about panes.
func paletteStateMatches(item CommandPaletteItem, states []string) bool {
	if item.AgentState == "" {
		return false
	}
	if states == nil {
		return true // a bare "@": anything running an agent
	}
	for _, s := range states {
		if item.AgentState == s {
			return true
		}
	}
	return false
}

// FilterCommandPalette filters command palette items by a query string, best
// match first, after an optional leading "@state" token narrows the list to the
// panes in that state (see splitPaletteQuery).
//
// Matching is the shared scored matcher, so one box ranks static commands and
// panes against each other. Name is matched on its own and Category is a weaker
// fallback, so a row admitted only by its section name can never outrank one
// that matched what it actually says.
func FilterCommandPalette(items []CommandPaletteItem, query string) []CommandPaletteItem {
	if states, filtered, rest := splitPaletteQuery(query); filtered {
		kept := make([]CommandPaletteItem, 0, len(items))
		for _, item := range items {
			if paletteStateMatches(item, states) {
				kept = append(kept, item)
			}
		}
		// The scored matcher runs over what is left, so "@a server" still ranks a
		// prefix hit above a mid-word one.
		return FilterCommandPalette(kept, rest)
	}
	if query == "" {
		return items
	}
	var m fuzzy.Matcher
	hits := m.FilterIndex(query, len(items), func(i int) string {
		return printableTitle(items[i].Name)
	})

	named := make([]bool, len(items))
	out := make([]CommandPaletteItem, 0, len(items))
	for _, h := range hits {
		named[h.Index] = true
		item := items[h.Index]
		item.Match = h.Positions
		out = append(out, item)
	}

	// Category hits land after every name hit rather than being scored beside
	// them. That is the old scoring's intent without its magic numbers: typing
	// "layout" should list the Layout section, below anything actually called
	// layout.
	for i, item := range items {
		if named[i] || item.Category == "" || !fuzzy.Match(query, item.Category) {
			continue
		}
		item.Match = nil
		out = append(out, item)
	}
	return out
}
