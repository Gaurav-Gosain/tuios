package app

import (
	"image"

	tea "charm.land/bubbletea/v2"
	"tuios.dev/vt"
)

func (m *OS) getRealCursor() *tea.Cursor {
	cur, hidden := m.paneCursor()
	if hidden {
		return nil
	}
	return cur
}

func (m *OS) paneCursor() (cur *tea.Cursor, hidden bool) {
	sidebarWindow := m.SidebarSessionWindow()
	if sidebarWindow == nil && (m.Mode != TerminalMode || m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows)) {
		return nil, false
	}

	if m.ShowScrollbackBrowser || m.review.open || m.hints != nil || m.paneLabels != nil {
		return nil, false
	}
	if m.wsSlide.running() {
		return nil, false
	}

	if m.AnyOverlayOpen() || m.pathPicker != nil {
		return nil, false
	}

	if m.Resizing {
		return nil, false
	}

	window := sidebarWindow
	if window == nil {
		window = m.Windows[m.FocusedWindow]
	}
	if window == nil || window.Terminal == nil {
		return nil, false
	}
	if m.DrawsTML(window) || m.tmlHostTooOld(window) {
		return nil, false
	}

	if window.CopyModeVisible() || window.ScrollbackOffset > 0 {
		return nil, false
	}

	pos, hidden, ok := window.GuestCursor()
	if !ok {
		return nil, false
	}
	style, steady, col := window.CachedCursorStyle, window.CachedCursorSteady, window.CachedCursorColor

	contentWidth := window.ContentWidth()
	contentHeight := window.ContentHeight()

	if pos.X < 0 || pos.X >= contentWidth || pos.Y < 0 || pos.Y >= contentHeight {
		return nil, false
	}

	borderOffset := 1
	if window.Tiled {
		borderOffset = 0
	}
	screenX := window.X + borderOffset + pos.X
	screenY := window.Y + borderOffset + pos.Y
	if v := m.sessionView; v.on && sidebarWindow == nil {
		screenX, screenY = v.toScreen(screenX, screenY)
		if !image.Pt(screenX, screenY).In(v.clip) {
			return nil, false
		}
	}

	cursor := tea.NewCursor(screenX, screenY)
	cursor.Shape = mapCursorStyle(style)
	cursor.Blink = !steady && !m.Settings.Calm
	cursor.Color = col
	return cursor, hidden
}

func mapCursorStyle(style vt.CursorStyle) tea.CursorShape {
	switch style {
	case vt.CursorUnderline:
		return tea.CursorUnderline
	case vt.CursorBar:
		return tea.CursorBar
	default:
		return tea.CursorBlock
	}
}
