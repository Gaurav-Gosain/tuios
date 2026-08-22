package app

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
	uv "github.com/charmbracelet/ultraviolet"
)

// getRealCursor returns a real terminal cursor for the focused window,
// or nil to hide the cursor. This enables native cursor shape support
// (block/bar/underline) from vi-mode and other applications.
//
// Everything here is read from the focused window's emulator on the frame it is
// used, so the shape the host is shown is the shape that window's guest asked
// for and nothing else. That is the whole mechanism: a mode change, a workspace
// switch and a reattach all repaint, a repaint calls this, and Bubble Tea emits
// DECSCUSR only when the answer differs from the last frame's. No path has to
// remember to re-emit anything, and an unfocused pane cannot reach the host
// cursor at all.
func (m *OS) getRealCursor() *tea.Cursor {
	// Only show real cursor in terminal mode with valid focused window
	if m.Mode != TerminalMode || m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows) {
		return nil
	}

	if m.ShowScrollbackBrowser {
		return nil
	}

	// A resize gesture draws no cursor: the pane it is over is showing the size
	// readout, not the guest's screen, so a cursor sitting in it points at
	// nothing. The gesture borrows window management (BeginPointerGesture), which
	// the mode check above already catches; this says it directly so the
	// property holds for any resize, however the mode got where it is.
	if m.Resizing {
		return nil
	}

	window := m.Windows[m.FocusedWindow]
	if window == nil || window.Terminal == nil {
		return nil
	}

	// Hide during copy mode, scrollback, or when VT hides cursor.
	// IsCursorHidden, CursorPosition and CursorStyle read emulator state that
	// the PTY and daemon output goroutines mutate under the window's I/O lock,
	// so all three reads take the read side of it.
	// An implicit copy-mode session that is sitting at the bottom (a
	// drag-selection over live output) is not a reason to hide the shell's
	// cursor; being scrolled back still is, and that is the second condition.
	if window.CopyModeVisible() || window.ScrollbackOffset > 0 {
		return nil
	}

	// Take the lock only if it is free. A pane in an output burst holds the
	// exclusive side almost continuously, and this read runs on the frame that
	// carries the user's keystroke echo, so blocking here makes a flooding
	// pane slow down typing everywhere. The cursor from the last frame that
	// did acquire is at most one frame stale and converges the moment the
	// burst ends, which is the same trade the compositor already makes for
	// pane content.
	var hidden, steady bool
	var pos uv.Position
	var style vt.CursorStyle
	if window.TryRLockIO() {
		if window.Terminal == nil {
			// Re-check under the lock: Close() nils Terminal while holding it.
			window.RUnlockIO()
			return nil
		}
		hidden = window.Terminal.IsCursorHidden()
		pos = window.Terminal.CursorPosition()
		style, steady = window.Terminal.CursorStyle()
		window.RUnlockIO()
		window.CachedCursor, window.CachedCursorHidden = pos, hidden
		window.CachedCursorStyle, window.CachedCursorSteady = style, steady
	} else {
		hidden, pos = window.CachedCursorHidden, window.CachedCursor
		style, steady = window.CachedCursorStyle, window.CachedCursorSteady
	}

	if hidden {
		return nil
	}
	contentWidth := window.ContentWidth()
	contentHeight := window.ContentHeight()

	// Bounds check - cursor must be within visible content area
	if pos.X < 0 || pos.X >= contentWidth || pos.Y < 0 || pos.Y >= contentHeight {
		return nil
	}

	// Transform to screen coordinates (+1 for border, +0 for tiled)
	borderOffset := 1
	if window.Tiled {
		borderOffset = 0
	}
	screenX := window.X + borderOffset + pos.X
	screenY := window.Y + borderOffset + pos.Y

	cursor := tea.NewCursor(screenX, screenY)
	cursor.Shape = mapCursorStyle(style)
	cursor.Blink = !steady
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
