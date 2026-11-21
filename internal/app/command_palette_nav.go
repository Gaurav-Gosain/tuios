package app

import (
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/listnav"
)

func (m *OS) allPaletteItems() []CommandPaletteItem {
	if m.PaletteItems == nil {
		m.rebuildPaletteItems()
	}
	return m.PaletteItems
}

func (m *OS) rebuildPaletteItems() {
	static := GetCommandPaletteItems()
	for i, it := range static {
		if ws, ok := strings.CutPrefix(it.Bind, "workspace_prefix_switch_"); ok {
			if n, err := strconv.Atoi(ws); err == nil {
				static[i].Name = m.WorkspaceWordTitle() + " " + strconv.Itoa(m.workspaceDisplayNumber(n))
			}
		}
		if it.Bind == "close_workspace" {
			static[i].Name = "Close " + m.workspaceWord()
		}
	}
	m.liveShortcuts(static)
	if !m.agentsSeen() {
		static = slices.DeleteFunc(static, func(it CommandPaletteItem) bool { return it.Category == paletteCategoryAgents })
	}
	if !m.agentsPageAvailable() {
		static = slices.DeleteFunc(static, func(it CommandPaletteItem) bool { return it.Name == paletteAgentsSettingsName })
	}
	if !m.DaemonRestartAvailable() {
		static = slices.DeleteFunc(static, func(it CommandPaletteItem) bool { return it.Name == paletteRestartDaemonName })
	}
	if len(m.MultifocusSet) == 0 {
		static = slices.DeleteFunc(static, func(it CommandPaletteItem) bool { return it.Name == paletteMultiCopyName })
	}
	if m.WindowModeOff() {
		static = slices.DeleteFunc(static, func(it CommandPaletteItem) bool { return config.IsWindowModeAction(it.Bind) })
	}
	items := make([]CommandPaletteItem, 0,
		len(static)+len(m.PaletteSessionItems)+len(m.PaletteKeybindItems)+len(m.PaletteSettingItems))
	items = append(items, static...)
	items = append(items, m.commandPaletteItems()...)
	items = append(items, m.pluginPaletteItems()...)
	items = append(items, m.PaletteSessionItems...)
	items = append(items, m.PaletteKeybindItems...)
	items = append(items, m.PaletteSettingItems...)
	m.PaletteItems = items
}

func (m *OS) filteredPaletteItems() []CommandPaletteItem {
	if m.prompt.req != nil {
		return m.promptRows()
	}
	if m.fileSearch {
		return matchPaletteItems(m.allPaletteItems(), m.paletteList.Query.String())
	}
	return FilterCommandPalette(m.allPaletteItems(), m.paletteList.Query.String())
}

// OpenCommandPalette opens the palette and rebuilds its session/window entries
// from the current session tree. This is the one place that does the tree build
// (and, in daemon mode, the daemon round trip inside it) so it happens once per
// open rather than once per frame.
func (m *OS) OpenCommandPalette() tea.Cmd {
	m.ShowCommandPalette = true
	m.paletteList.Reset()
	if m.fileSearch {
		m.PaletteItems = []CommandPaletteItem{}
		return nil
	}
	m.PaletteSessionItems = getSessionPaletteItems(m)
	m.PaletteKeybindItems = getKeybindPaletteItems(m)
	m.PaletteSettingItems = getSettingPaletteItems(m)
	m.rebuildPaletteItems()
	return nil
}

func (m *OS) paletteRescroll() {
	filtered := m.filteredPaletteItems()
	_, visible, _ := m.paletteLayout()
	lines := paletteLines(filtered, paletteGrouped(m.paletteList.Query.String()) && !m.fileSearch)
	m.paletteList.Scroll = paletteScroll(m.paletteList.Scroll, lines, m.paletteList.Cursor, visible)
}

func palettePicker() *listPicker {
	return &listPicker{
		kind:  "palette",
		state: func(m *OS) *listnav.State { return &m.paletteList },
		count: func(m *OS) int { return len(m.filteredPaletteItems()) },
		visible: func(m *OS) int {
			_, visible, _ := m.paletteLayout()
			return visible
		},
		page:     func(m *OS) int { return m.PalettePageRows() },
		rescroll: func(m *OS) { m.paletteRescroll() },
		keys: func(m *OS, key string) (tea.Cmd, bool) {
			if m.prompt.req != nil {
				return m.promptKey(key)
			}
			if key == PaletteRebindKey {
				m.RebindPaletteSelection()
				return nil, true
			}
			return nil, false
		},
		accept: func(m *OS) tea.Cmd { return m.ActivateCommandPalette() },
		close: func(m *OS) tea.Cmd {
			if m.prompt.req != nil {
				return m.promptCancel()
			}
			m.CloseCommandPalette()
			return nil
		},
		clearKey: true,
	}
}

// PalettePageRows is how many rows a page key moves the palette by.
func (m *OS) PalettePageRows() int {
	_, visible, _ := m.paletteLayout()
	return max(visible, 1)
}

// CloseCommandPalette hides the palette and resets its state.
func (m *OS) CloseCommandPalette() {
	if m.prompt.req != nil {
		m.endPrompt()
		return
	}
	m.ShowCommandPalette = false
	if m.fileSearch {
		if m.fileSearchCancel != nil {
			m.fileSearchCancel.Store(true)
		}
		m.fileSearch = false
		m.fileSearchGen++
	}
	m.paletteList.Reset()
	m.PaletteItems = nil
	m.PaletteKeybindItems = nil
	m.PaletteSettingItems = nil
}

func getSettingPaletteItems(m *OS) []CommandPaletteItem {
	var items []CommandPaletteItem
	for ci, cat := range m.settingsCategories() {
		for ii, item := range cat.Items {
			ci, ii := ci, ii
			items = append(items, CommandPaletteItem{
				Name:     item.Label,
				Shortcut: cat.Name,
				Category: "Settings",
				Setting:  true,
				Action: func(m *OS) (*OS, tea.Cmd) {
					m.OpenSettingsAtRow(ci, ii)
					return m, nil
				},
			})
		}
	}
	return items
}

// ActivateCommandPalette runs the currently selected command and closes the
// palette, returning its command. Shared by keyboard Enter and mouse click.
func (m *OS) ActivateCommandPalette() tea.Cmd {
	if m.prompt.req != nil {
		return m.promptAccept()
	}
	filtered := m.filteredPaletteItems()
	if m.paletteList.Cursor < 0 || m.paletteList.Cursor >= len(filtered) {
		if m.fileSearch {
			m.ShowNotification("No file matches "+m.paletteList.Query.String(), "info", m.Settings.NotificationDuration)
		} else {
			m.ShowNotification("Nothing to run: no command matches "+m.paletteList.Query.String(), "info", m.Settings.NotificationDuration)
		}
		return nil
	}
	item := filtered[m.paletteList.Cursor]
	m.CloseCommandPalette()
	over, before := m.ShowInbox, m.focusSpot()
	_, cmd := item.Run(m)
	if over && m.ShowInbox && m.focusSpot() != before {
		m.leaveInboxForPane()
	}
	return cmd
}

type focusSpot struct {
	workspace int
	window    string
}

func (m *OS) focusSpot() focusSpot {
	spot := focusSpot{workspace: m.CurrentWorkspace}
	if w := m.GetFocusedWindow(); w != nil {
		spot.window = w.ID
	}
	return spot
}

// Run runs the row: its own handler when it has one, and otherwise the
// keybind action it names, through the same runner a key press goes through,
// so the row and the key do the same thing.
func (it CommandPaletteItem) Run(m *OS) (*OS, tea.Cmd) {
	if it.Action != nil {
		return it.Action(m)
	}
	if it.Bind == "" {
		return m, nil
	}
	if err := m.runAction(it.Bind); err != nil {
		m.ShowNotification(err.Error(), "error", m.Settings.NotificationDuration)
		return m, nil
	}
	return m, m.takeScriptCmds()
}
