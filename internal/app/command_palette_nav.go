package app

import (
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/listnav"
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
	static := GetCommandPaletteItems()
	m.liveShortcuts(static)
	// The agent entries wait until an agent has been seen, like the prefix
	// menu's Inbox lines. Their prefix keys work either way.
	if !m.agentsSeen() {
		static = slices.DeleteFunc(static, func(it CommandPaletteItem) bool { return it.Category == paletteCategoryAgents })
	}
	// The review waits for a daemon that can review a pane's changes.
	if !m.reviewSupported() {
		static = slices.DeleteFunc(static, func(it CommandPaletteItem) bool { return it.Name == paletteReviewName })
	}
	// The Agents tab is not there with the agent features off, or on a client
	// that cannot change this machine's integrations.
	if !m.agentsPageAvailable() {
		static = slices.DeleteFunc(static, func(it CommandPaletteItem) bool { return it.Name == paletteAgentsSettingsName })
	}
	// Multi copy mode needs a multifocus set, so it is offered only with one.
	if !m.DaemonRestartAvailable() {
		static = slices.DeleteFunc(static, func(it CommandPaletteItem) bool { return it.Name == paletteRestartDaemonName })
	}
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
// window in view.
func (m *OS) PaletteMove(delta int) { _ = palettePicker().move(m, delta) }

// paletteRescroll keeps the cursor's row in view. The window counts list rows,
// headings included, so it is kept against the rows the renderer lays out
// rather than against the commands alone.
func (m *OS) paletteRescroll() {
	filtered := m.filteredPaletteItems()
	_, visible, _ := m.paletteLayout()
	lines := paletteLines(filtered, paletteGrouped(m.paletteList.Query.String()) && !m.fileSearch)
	m.paletteList.Scroll = paletteScroll(m.paletteList.Scroll, lines, m.paletteList.Cursor, visible)
}

// palettePicker is the command palette's list behaviour. See list_picker.go.
// A page is the rows on screen. The rebind key edits the binding of the
// action under the cursor.
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
			if key == PaletteRebindKey {
				m.RebindPaletteSelection()
				return nil, true
			}
			return nil, false
		},
		accept: func(m *OS) tea.Cmd { return m.ActivateCommandPalette() },
		close: func(m *OS) tea.Cmd {
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
	m.ShowCommandPalette = false
	if m.fileSearch {
		if m.fileSearchCancel != nil {
			m.fileSearchCancel.Store(true)
		}
		m.fileSearch = false
		m.fileSearchGen++
	}
	m.paletteList.Reset()
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
	if m.paletteList.Cursor < 0 || m.paletteList.Cursor >= len(filtered) {
		// Nothing to run. Closing here would dismiss the panel and throw away
		// the query that narrowed it to nothing, which answers the key with
		// silence and cannot be told from the key not being bound. The query is
		// what the user is part way through typing, so it stays and so does the
		// panel.
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
	// A row run over the agents view that went to another pane or
	// workspace closes the view, so the person sees where they went. The
	// view opens again on the same agent, as after ctrl+].
	if over && m.ShowInbox && m.focusSpot() != before {
		m.leaveInboxForPane()
	}
	return cmd
}

// focusSpot is where the focus is: the workspace and the focused window.
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
	if err := m.RunAction(it.Bind); err != nil {
		m.ShowNotification(err.Error(), "error", m.Settings.NotificationDuration)
		return m, nil
	}
	return m, m.takeScriptCmds()
}
