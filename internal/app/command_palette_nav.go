package app

import tea "charm.land/bubbletea/v2"

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
	items := make([]CommandPaletteItem, 0, len(static)+len(m.PaletteSessionItems))
	items = append(items, static...)
	items = append(items, m.PaletteSessionItems...)
	m.PaletteItems = items
}

// filteredPaletteItems returns the command palette entries matching the current
// query.
func (m *OS) filteredPaletteItems() []CommandPaletteItem {
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
	m.PaletteSessionItems = getSessionPaletteItems(m)
	m.rebuildPaletteItems()
	return nil
}

// PaletteMove moves the command-palette selection by delta and keeps the scroll
// window in view. Shared by keyboard arrows and the mouse wheel.
func (m *OS) PaletteMove(delta int) {
	n := len(m.filteredPaletteItems())
	if n == 0 {
		m.CommandPaletteSelected = 0
		return
	}
	m.CommandPaletteSelected = clampInt(m.CommandPaletteSelected+delta, 0, n-1)
	_, visible, _ := m.paletteLayout()
	if m.CommandPaletteSelected < m.CommandPaletteScroll {
		m.CommandPaletteScroll = m.CommandPaletteSelected
	}
	if m.CommandPaletteSelected >= m.CommandPaletteScroll+visible {
		m.CommandPaletteScroll = m.CommandPaletteSelected - visible + 1
	}
}

// CloseCommandPalette hides the palette and resets its state.
func (m *OS) CloseCommandPalette() {
	m.ShowCommandPalette = false
	m.CommandPaletteQuery = ""
	m.CommandPaletteSelected = 0
	m.CommandPaletteScroll = 0
	// Nothing reads the merged list while the palette is shut, and it is rebuilt
	// on the next open, so dropping it costs nothing but that rebuild.
	m.PaletteItems = nil
}

// ActivateCommandPalette runs the currently selected command and closes the
// palette, returning its command. Shared by keyboard Enter and mouse click.
func (m *OS) ActivateCommandPalette() tea.Cmd {
	filtered := m.filteredPaletteItems()
	if m.CommandPaletteSelected < 0 || m.CommandPaletteSelected >= len(filtered) {
		m.CloseCommandPalette()
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
