package app

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// CommandPaletteItem represents a single command in the command palette.
type CommandPaletteItem struct {
	Name     string // Display name: "Split Horizontal"
	Shortcut string // Key hint: "prefix+v"
	Category string // "Window", "Layout", "Session", "Navigation"
	Action   func(m *OS) (*OS, tea.Cmd)
}

// GetCommandPaletteItems returns all available commands for the command palette.
func GetCommandPaletteItems() []CommandPaletteItem {
	return []CommandPaletteItem{
		// Window management
		{
			Name:     "New Window",
			Shortcut: "prefix+c",
			Category: "Window",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.AddWindow("")
				return m, nil
			},
		},
		{
			Name:     "Close Window",
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
			Name:     "Rename Window",
			Shortcut: "prefix+,",
			Category: "Window",
			Action: func(m *OS) (*OS, tea.Cmd) {
				if config.WindowTitlePosition != "hidden" && len(m.Windows) > 0 && m.FocusedWindow >= 0 {
					focusedWindow := m.GetFocusedWindow()
					if focusedWindow != nil {
						m.Mode = WindowManagementMode
						m.RenamingWindow = true
						m.RenameBuffer = focusedWindow.CustomName
					}
				}
				return m, nil
			},
		},
		{
			Name:     "Toggle Zoom",
			Shortcut: "prefix+z",
			Category: "Window",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.ToggleZoom()
				return m, nil
			},
		},
		{
			Name:     "Minimize Window",
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
			Name:     "Restore All Minimized",
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
			Name:     "Toggle Tiling",
			Shortcut: "prefix+space",
			Category: "Layout",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.AutoTiling = !m.AutoTiling
				if m.AutoTiling {
					m.TileAllWindows()
					m.ShowNotification("Tiling Mode Enabled", "success", config.NotificationDuration)
				} else {
					m.ShowNotification("Tiling Mode Disabled", "info", config.NotificationDuration)
				}
				return m, nil
			},
		},
		{
			Name:     "Split Horizontal",
			Shortcut: "prefix+-",
			Category: "Layout",
			Action: func(m *OS) (*OS, tea.Cmd) {
				if m.AutoTiling {
					m.SplitFocusedHorizontal()
					m.ShowNotification("Split Horizontal", "info", config.NotificationDuration)
				}
				return m, nil
			},
		},
		{
			Name:     "Split Vertical",
			Shortcut: "prefix+|",
			Category: "Layout",
			Action: func(m *OS) (*OS, tea.Cmd) {
				if m.AutoTiling {
					m.SplitFocusedVertical()
					m.ShowNotification("Split Vertical", "info", config.NotificationDuration)
				}
				return m, nil
			},
		},
		{
			Name:     "Smart Split",
			Shortcut: "",
			Category: "Layout",
			Action: func(m *OS) (*OS, tea.Cmd) {
				if m.AutoTiling {
					m.SmartSplitFocused()
					m.ShowNotification("Smart Split", "info", config.NotificationDuration)
				}
				return m, nil
			},
		},
		{
			Name:     "Rotate Split",
			Shortcut: "prefix+R",
			Category: "Layout",
			Action: func(m *OS) (*OS, tea.Cmd) {
				if m.AutoTiling {
					m.RotateFocusedSplit()
					m.ShowNotification("Split Rotated", "info", config.NotificationDuration)
				}
				return m, nil
			},
		},
		{
			Name:     "Equalize Splits",
			Shortcut: "prefix+=",
			Category: "Layout",
			Action: func(m *OS) (*OS, tea.Cmd) {
				if m.AutoTiling {
					m.EqualizeSplits()
					m.ShowNotification("Splits Equalized", "info", config.NotificationDuration)
				}
				return m, nil
			},
		},
		{
			Name:     "Snap Fullscreen",
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
			Name:     "Save Layout",
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
			Name:     "Load Layout",
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
			Name:     "Next Window",
			Shortcut: "prefix+n",
			Category: "Navigation",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.CycleToNextVisibleWindow()
				return m, nil
			},
		},
		{
			Name:     "Previous Window",
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

		// Floating
		{
			Name:     "Toggle Floating",
			Category: "Window",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.ToggleFloating()
				return m, nil
			},
		},
		// Navigation
		{
			Name:     "Aggregate View (All Windows)",
			Shortcut: "Alt+G",
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
			Name:     "Reload Config",
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
				_ = newCfg // TODO: apply new config (keybinds, appearance)
				m.ShowNotification("Config reloaded", "success", 0)
				return m, nil
			},
		},
		{
			Name:     "Switch Session",
			Shortcut: "prefix+S",
			Category: "Session",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.ShowSessionSwitcher = true
				m.SessionSwitcherQuery = ""
				m.SessionSwitcherSelected = 0
				m.SessionSwitcherScroll = 0
				m.SessionSwitcherError = ""
				m.SessionSwitcherItems = m.RefreshSessionList()
				return m, nil
			},
		},
		{
			Name:     "Show Help",
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
			Name:     "Show Logs",
			Shortcut: "prefix+D l",
			Category: "Session",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.ShowLogs = !m.ShowLogs
				return m, nil
			},
		},
		{
			Name:     "Toggle Scrollback Browser",
			Shortcut: "prefix+s",
			Category: "Session",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.ShowScrollbackBrowser = !m.ShowScrollbackBrowser
				return m, nil
			},
		},
		{
			Name:     "Toggle Show Keys",
			Shortcut: "prefix+D k",
			Category: "Session",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.ShowKeys = !m.ShowKeys
				return m, nil
			},
		},
		{
			Name:     "Toggle Animations",
			Shortcut: "prefix+D a",
			Category: "Session",
			Action: func(m *OS) (*OS, tea.Cmd) {
				config.AnimationsEnabled = !config.AnimationsEnabled
				if config.AnimationsEnabled {
					m.ShowNotification("Animations Enabled", "success", config.NotificationDuration)
				} else {
					m.ShowNotification("Animations Disabled", "info", config.NotificationDuration)
				}
				return m, nil
			},
		},
		{
			Name:     "Window Management Mode",
			Shortcut: "prefix+esc",
			Category: "Session",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.Mode = WindowManagementMode
				m.ShowNotification("Window Management Mode", "info", config.NotificationDuration)
				if focusedWindow := m.GetFocusedWindow(); focusedWindow != nil {
					focusedWindow.InvalidateCache()
				}
				return m, nil
			},
		},
		{
			Name:     "Enter Copy Mode",
			Shortcut: "prefix+[",
			Category: "Session",
			Action: func(m *OS) (*OS, tea.Cmd) {
				if focusedWindow := m.GetFocusedWindow(); focusedWindow != nil {
					focusedWindow.EnterCopyMode()
					m.ShowNotification("COPY MODE (hjkl/q)", "info", config.NotificationDuration*2)
				}
				return m, nil
			},
		},
	}
}

// FilterCommandPalette filters command palette items by a query string.
// It performs case-insensitive substring matching on both Name and Category.
func FilterCommandPalette(items []CommandPaletteItem, query string) []CommandPaletteItem {
	if query == "" {
		return items
	}
	q := strings.ToLower(query)

	type scored struct {
		item  CommandPaletteItem
		score int
	}
	var results []scored

	for _, item := range items {
		nameLower := strings.ToLower(item.Name)
		catLower := strings.ToLower(item.Category)

		score := 0
		if strings.Contains(nameLower, q) {
			score = 100
			// Boost for prefix match on name
			if strings.HasPrefix(nameLower, q) {
				score = 200
			}
			// Boost for exact word match
			for word := range strings.FieldsSeq(nameLower) {
				if strings.HasPrefix(word, q) {
					score += 50
					break
				}
			}
		} else if strings.Contains(catLower, q) {
			score = 10 // Category-only match ranks lower
		}

		if score > 0 {
			results = append(results, scored{item, score})
		}
	}

	// Sort by score descending (stable to preserve original order within same score)
	for i := 1; i < len(results); i++ {
		for j := i; j > 0 && results[j].score > results[j-1].score; j-- {
			results[j], results[j-1] = results[j-1], results[j]
		}
	}

	filtered := make([]CommandPaletteItem, len(results))
	for i, r := range results {
		filtered[i] = r.item
	}
	return filtered
}
