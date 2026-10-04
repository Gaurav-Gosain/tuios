package app

import (
	"slices"

	tea "charm.land/bubbletea/v2"
)

// allPaletteItems returns the merged palette list: the static commands, and the
// session/window entries built when the palette was opened.
func (m *OS) allPaletteItems() []CommandPaletteItem {
	if m.PaletteItems == nil {
		// A caller that reaches the list without opening the palette still gets
		// a correct answer rather than an empty one.
		m.rebuildPaletteItems()
	}
	return m.PaletteItems
}

// rebuildPaletteItems merges the two sources. Called when the palette opens,
// never per frame.
func (m *OS) rebuildPaletteItems() {
	static := GetCommandPaletteItems(&m.Settings)
	// The agent entries wait until an agent has been seen, like the prefix
	// menu's Inbox lines. Their prefix keys work either way.
	if !m.agentsSeen() {
		static = slices.DeleteFunc(static, func(it CommandPaletteItem) bool { return it.Category == paletteCategoryAgents })
	}
	// The review waits for a daemon that can review a pane's changes.
	if !m.reviewSupported() {
		static = slices.DeleteFunc(static, func(it CommandPaletteItem) bool { return it.Name == paletteReviewName })
	}
	// Multi copy mode needs a multifocus set, so it is offered only with one.
	if len(m.MultifocusSet) == 0 {
		static = slices.DeleteFunc(static, func(it CommandPaletteItem) bool { return it.Name == paletteMultiCopyName })
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

// filteredPaletteItems returns the command palette entries matching the current
// query.
func (m *OS) filteredPaletteItems() []CommandPaletteItem {
	if m.fileSearch {
		return matchPaletteItems(m.allPaletteItems(), m.CommandPaletteQuery)
	}
	return FilterCommandPalette(m.allPaletteItems(), m.CommandPaletteQuery)
}

// OpenCommandPalette opens the palette and rebuilds its session/window entries
// from the current session tree. This is the one place that does the tree build
// (and, in daemon mode, the daemon round trip inside it) so it happens once per
// open rather than once per frame.
func (m *OS) OpenCommandPalette() tea.Cmd {
	m.ShowCommandPalette = true
	m.CommandPaletteQuery = ""
	m.CommandPaletteSelected = 0
	m.CommandPaletteScroll = 0
	if m.fileSearch {
		m.PaletteItems = []CommandPaletteItem{}
		return nil
	}
	m.PaletteSessionItems = getSessionPaletteItems(m)
	// Built here for the same reason as the session entries: once per open, not
	// once per frame. This one is a pass over the config rather than a daemon
	// round trip, but the filtered list is rebuilt on every keystroke and the
	// action rows are the larger half of it.
	m.PaletteKeybindItems = getKeybindPaletteItems(m)
	m.PaletteSettingItems = getSettingPaletteItems(m)
	m.rebuildPaletteItems()
	return nil
}

// PaletteMove moves the command-palette selection by delta and keeps the scroll
// window in view. Shared by keyboard arrows and the mouse wheel.
func (m *OS) PaletteMove(delta int) {
	filtered := m.filteredPaletteItems()
	n := len(filtered)
	if n == 0 {
		m.CommandPaletteSelected = 0
		return
	}
	m.CommandPaletteSelected = m.listStep(m.CommandPaletteSelected, delta, n)
	_, visible, _ := m.paletteLayout()
	// The scroll counts list rows, headers included, so it is kept against the
	// rows the renderer lays out rather than against the commands alone.
	lines := paletteLines(filtered, paletteGrouped(m.CommandPaletteQuery) && !m.fileSearch)
	m.CommandPaletteScroll = paletteScroll(m.CommandPaletteScroll, lines, m.CommandPaletteSelected, visible)
}

// PalettePageRows is how many rows a page key moves the palette by.
func (m *OS) PalettePageRows() int {
	_, visible, _ := m.paletteLayout()
	return max(visible, 1)
}

// CloseCommandPalette hides the palette and resets its state.
func (m *OS) CloseCommandPalette() {
	m.ShowCommandPalette = false
	if m.fileSearch {
		if m.fileSearchCancel != nil {
			m.fileSearchCancel.Store(true)
		}
		m.fileSearch = false
		m.fileSearchGen++
	}
	m.CommandPaletteQuery = ""
	m.CommandPaletteSelected = 0
	m.CommandPaletteScroll = 0
	// Nothing reads the merged list while the palette is shut, and it is rebuilt
	// on the next open, so dropping it costs nothing but that rebuild.
	m.PaletteItems = nil
	m.PaletteKeybindItems = nil
	m.PaletteSettingItems = nil
}

// getSettingPaletteItems is one palette row per settings row, named the way
// the page names it, with its tab in the meta slot. Running one opens the
// settings page on that row.
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
	filtered := m.filteredPaletteItems()
	if m.CommandPaletteSelected < 0 || m.CommandPaletteSelected >= len(filtered) {
		// Nothing to run. Closing here would dismiss the panel and throw away
		// the query that narrowed it to nothing, which answers the key with
		// silence and cannot be told from the key not being bound. The query is
		// what the user is part way through typing, so it stays and so does the
		// panel.
		if m.fileSearch {
			m.ShowNotification("No file matches "+m.CommandPaletteQuery, "info", m.Settings.NotificationDuration)
		} else {
			m.ShowNotification("Nothing to run: no command matches "+m.CommandPaletteQuery, "info", m.Settings.NotificationDuration)
		}
		return nil
	}
	action := filtered[m.CommandPaletteSelected].Action
	m.CloseCommandPalette()
	if action != nil {
		_, cmd := action(m)
		return cmd
	}
	return nil
}
