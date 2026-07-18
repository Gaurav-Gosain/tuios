package app

import (
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// EditScrollbackInEditor captures the focused pane's scrollback to a temp file
// and returns a tea.Cmd that suspends bubbletea and opens $EDITOR.
func (m *OS) EditScrollbackInEditor() tea.Cmd {
	content, err := m.capturePane("", "scrollback") // plain text, no ANSI
	if err != nil {
		m.ShowNotification("Capture failed: "+err.Error(), "error", 0)
		return nil
	}

	tmpFile, err := os.CreateTemp("", "tuios-scrollback-*.txt")
	if err != nil {
		m.ShowNotification("Failed to create temp file: "+err.Error(), "error", 0)
		return nil
	}
	if _, err := tmpFile.WriteString(content); err != nil {
		_ = tmpFile.Close()
		m.ShowNotification("Failed to write temp file: "+err.Error(), "error", 0)
		return nil
	}
	_ = tmpFile.Close()

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		editor = "vi"
	}

	// Use tea.ExecProcess to properly suspend bubbletea while editor runs
	c := exec.Command(editor, tmpFile.Name()) //nolint:gosec
	return tea.ExecProcess(c, func(err error) tea.Msg {
		if err != nil {
			return nil
		}
		return nil
	})
}

// MoveSelectionCursor moves the selection cursor in the specified direction.
// Parameters:
//   - window: The window to operate on
//   - dx, dy: Direction to move cursor (-1, 0, 1)
//   - extending: true if extending selection (Shift+Arrow), false if just moving cursor
func (m *OS) MoveSelectionCursor(window *terminal.Window, dx, dy int, extending bool) {
	if window.Terminal == nil {
		return
	}

	screen := window.Terminal
	if screen == nil {
		return
	}

	// Get terminal dimensions (account for borders)
	maxX := window.ContentWidth()
	maxY := window.ContentHeight()

	// Initialize selection cursor if not set (only for non-extending moves)
	if !extending && !window.IsSelecting {
		// Position at terminal cursor when starting cursor movement
		cursor := screen.CursorPosition()
		window.SelectionCursor.X = cursor.X
		window.SelectionCursor.Y = cursor.Y
	}

	// Move cursor
	newX := window.SelectionCursor.X + dx
	newY := window.SelectionCursor.Y + dy

	// Handle scrollback when cursor moves beyond visible area in selection mode
	if newY < 0 {
		// Trying to move up past the top - scroll up in scrollback
		// Note: We DON'T enter scrollbackMode, we just adjust the offset
		// This allows selection to work with scrollback seamlessly
		if window.Terminal != nil {
			scrollbackLen := window.ScrollbackLen()
			if scrollbackLen > 0 && window.ScrollbackOffset < scrollbackLen {
				// Scroll up by increasing offset
				window.ScrollbackOffset++
				if window.ScrollbackOffset > scrollbackLen {
					window.ScrollbackOffset = scrollbackLen
				}
				window.InvalidateCache()
			}
		}
		newY = 0 // Keep cursor at top
	} else if newY >= maxY {
		// Trying to move down past the bottom - scroll down in scrollback
		if window.ScrollbackOffset > 0 {
			window.ScrollbackOffset--
			if window.ScrollbackOffset < 0 {
				window.ScrollbackOffset = 0
			}
			window.InvalidateCache()
		}
		newY = maxY - 1 // Keep cursor at bottom
	}

	// X boundary checking
	if newX < 0 {
		newX = 0
	}
	if newX >= maxX {
		newX = maxX - 1
	}

	// Update cursor position
	window.SelectionCursor.X = newX
	window.SelectionCursor.Y = newY

	if extending {
		// Extending selection - update selection end
		if !window.IsSelecting {
			// Start selection
			window.IsSelecting = true
			window.SelectionStart = window.SelectionCursor
		}
		window.SelectionEnd = window.SelectionCursor

		// Extract selected text
		selectedText := m.extractSelectedText(window)
		window.SelectedText = selectedText

	} else {
		// Just moving cursor - start new selection
		if window.IsSelecting || window.SelectedText != "" {
			// Clear existing selection
			window.IsSelecting = false
			window.SelectedText = ""
		}

		// Start new selection at cursor position
		window.SelectionStart = window.SelectionCursor
		window.SelectionEnd = window.SelectionCursor
		window.IsSelecting = true
	}

	window.InvalidateCache()
}

// extractSelectedText extracts text from the terminal within the selected region.
func (m *OS) extractSelectedText(window *terminal.Window) string {
	if window.Terminal == nil {
		return ""
	}

	// Ensure selection coordinates are valid
	startX := window.SelectionStart.X
	startY := window.SelectionStart.Y
	endX := window.SelectionEnd.X
	endY := window.SelectionEnd.Y

	// Normalize selection (ensure start comes before end)
	if startY > endY || (startY == endY && startX > endX) {
		startX, endX = endX, startX
		startY, endY = endY, startY
	}

	var selectedLines []string

	// Extract text line by line
	for y := startY; y <= endY; y++ {
		var lineBuilder strings.Builder

		// Determine start and end columns for this line
		lineStartX := 0
		lineEndX := window.ContentWidth() // Account for borders

		if y == startY {
			lineStartX = startX
		}
		if y == endY {
			lineEndX = endX
		}

		// Extract characters from the terminal for this line
		for x := lineStartX; x <= lineEndX && x < window.ContentWidth(); x++ {
			// Get the cell from the terminal at this position
			cell := window.Terminal.CellAt(x, y)
			if cell != nil && cell.Content != "" {
				lineBuilder.WriteString(string(cell.Content))
			} else {
				lineBuilder.WriteByte(' ')
			}
		}

		selectedLines = append(selectedLines, lineBuilder.String())
	}

	return strings.Join(selectedLines, "\n")
}
