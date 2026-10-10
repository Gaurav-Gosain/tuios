package app

import (
	"image"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
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
	// A docked session is a separate daemon pane; it can own the keyboard
	// without changing the center session's mode or focused window.
	sidebarWindow := m.SidebarSessionWindow()
	if sidebarWindow == nil && (m.Mode != TerminalMode || m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows)) {
		return nil
	}

	if m.ShowScrollbackBrowser || m.review.open || m.hints != nil || m.paneLabels != nil {
		return nil
	}

	// An overlay covers the pane, so the pane's cursor has nothing to point at.
	// The panels that take typing draw their own caret as a character, which is
	// why this hides the terminal one rather than moving it: leaving it on put a
	// blinking hardware cursor in the pane behind the panel, next to the caret
	// the panel had already drawn.
	if m.AnyOverlayOpen() {
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

	window := sidebarWindow
	if window == nil {
		window = m.Windows[m.FocusedWindow]
	}
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

	// GuestCursor takes the lock only if it is free. A pane in an output
	// burst holds it almost continuously, and blocking on this frame, which
	// carries the user's keystroke echo, makes a flooding pane slow down
	// typing everywhere. The cursor from the last frame that did read it is
	// at most one frame stale, the same trade the compositor makes for pane
	// content.
	pos, hidden, ok := window.GuestCursor()
	if !ok {
		return nil
	}
	style, steady := window.CachedCursorStyle, window.CachedCursorSteady

	if hidden {
		return nil
	}
	contentWidth := window.ContentWidth()
	contentHeight := window.ContentHeight()

	// A normalized shell view hides only empty leading guest rows. Keep the
	// hardware cursor on the same displayed cell as the shell's text.
	visualY := pos.Y - window.DisplayRowOffset
	// Bounds check: the cursor must be within the visible content area.
	if pos.X < 0 || pos.X >= contentWidth || visualY < 0 || visualY >= contentHeight {
		return nil
	}

	// Transform to screen coordinates (+1 for border, +0 for tiled)
	borderOffset := 1
	if window.Tiled {
		borderOffset = 0
	}
	screenX := window.X + borderOffset + pos.X
	screenY := window.Y + borderOffset + visualY
	// In a view of a larger session the pane is drawn shifted, and a cursor
	// the view does not show is not drawn. See pane_view.go.
	if v := m.sessionView; v.on && sidebarWindow == nil {
		screenX, screenY = v.toScreen(screenX, screenY)
		if !image.Pt(screenX, screenY).In(v.clip) {
			return nil
		}
	}

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
