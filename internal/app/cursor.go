package app

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// getRealCursor returns a real terminal cursor for the focused window,
// or nil to hide the cursor. This enables native cursor shape support
// (block/bar/underline) from vi-mode and other applications.
func (m *OS) getRealCursor() *tea.Cursor {
	// Only show real cursor in terminal mode with valid focused window
	if m.Mode != TerminalMode || m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows) {
		return nil
	}

	window := m.Windows[m.FocusedWindow]
	if window == nil || window.Terminal == nil {
		return nil
	}

	// Hide during copy mode, scrollback, or when VT hides cursor
	if (window.CopyMode != nil && window.CopyMode.Active) ||
		window.ScrollbackOffset > 0 ||
		window.Terminal.IsCursorHidden() {
		return nil
	}

	pos := window.Terminal.CursorPosition()
	contentWidth := window.Width - 2 // subtract borders
	contentHeight := window.Height - 2

	// Bounds check - cursor must be within visible content area
	if pos.X < 0 || pos.X >= contentWidth || pos.Y < 0 || pos.Y >= contentHeight {
		return nil
	}

	// Transform to screen coordinates (+1 for border)
	screenX := window.X + 1 + pos.X
	screenY := window.Y + 1 + pos.Y

	cursor := tea.NewCursor(screenX, screenY)
	cursor.Shape = mapCursorStyle(window.CursorStyle)
	cursor.Blink = window.CursorBlink
	return cursor
}

// mapCursorStyle converts vt.CursorStyle to tea.CursorShape.
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
