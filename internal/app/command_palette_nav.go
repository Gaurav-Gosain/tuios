package app

import tea "charm.land/bubbletea/v2"

// allPaletteItems returns the static commands plus the dynamic session/window
// entries built when the palette was opened (see OpenCommandPalette).
func (m *OS) allPaletteItems() []CommandPaletteItem {
	items := GetCommandPaletteItems()
	return append(items, m.PaletteSessionItems...)
}

// filteredPaletteItems returns the command palette entries matching the current
// query.
func (m *OS) filteredPaletteItems() []CommandPaletteItem {
	return FilterCommandPalette(m.allPaletteItems(), m.CommandPaletteQuery)
}

// OpenCommandPalette opens the palette and rebuilds its session/window
// entries from the current session tree. This is the one place that does the
// tree build (and, in daemon mode, the daemon round trip inside it) so it
// happens once per open rather than once per frame.
func (m *OS) OpenCommandPalette() {
	m.ShowCommandPalette = true
	m.CommandPaletteQuery = ""
	m.CommandPaletteSelected = 0
	m.CommandPaletteScroll = 0
	m.PaletteSessionItems = getSessionPaletteItems(m)
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
