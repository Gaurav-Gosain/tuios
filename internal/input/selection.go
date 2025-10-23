// Package input implements text selection logic for TUIOS.
package input

import (
	"fmt"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	uv "github.com/charmbracelet/ultraviolet"
)

// extractSelectedText extracts selected text from terminal based on selection coordinates
// This handles both current screen content and scrollback buffer
func extractSelectedText(window *terminal.Window, o *app.OS) string {
	if window.Terminal == nil {
		return ""
	}

	screen := window.Terminal
	if screen == nil {
		return ""
	}

	// Get selection bounds
	startX, startY := window.SelectionStart.X, window.SelectionStart.Y
	endX, endY := window.SelectionEnd.X, window.SelectionEnd.Y

	// Normalize selection (ensure start is before end)
	if startY > endY || (startY == endY && startX > endX) {
		startX, endX = endX, startX
		startY, endY = endY, startY
	}

	var selectedText strings.Builder

	// Get screen dimensions
	screenHeight := screen.Height()
	screenWidth := screen.Width()

	// Clamp to screen bounds
	if startY >= screenHeight || endY < 0 {
		return ""
	}
	if startY < 0 {
		startY = 0
	}
	if endY >= screenHeight {
		endY = screenHeight - 1
	}

	// Check if we're viewing scrollback
	scrollbackLen := window.ScrollbackLen()
	inScrollbackMode := window.ScrollbackOffset > 0

	// Helper function to get cell at position (handles scrollback)
	getCellAt := func(x, y int) *uv.Cell {
		if inScrollbackMode {
			if y < window.ScrollbackOffset {
				// This line is from scrollback buffer
				scrollbackIndex := scrollbackLen - window.ScrollbackOffset + y
				if scrollbackIndex >= 0 && scrollbackIndex < scrollbackLen {
					scrollbackLine := window.ScrollbackLine(scrollbackIndex)
					if scrollbackLine != nil && x < len(scrollbackLine) {
						return &scrollbackLine[x]
					}
				}
				return nil
			} else {
				// This line is from current screen (below scrollback)
				screenY := y - window.ScrollbackOffset
				if screenY >= 0 && screenY < screenHeight {
					return screen.CellAt(x, screenY)
				}
				return nil
			}
		} else {
			// No scrollback, read from current screen
			return screen.CellAt(x, y)
		}
	}

	// Single line selection
	if startY == endY {
		// Clamp selection bounds to line length
		if startX >= screenWidth {
			return ""
		}
		if endX >= screenWidth {
			endX = screenWidth - 1
		}

		for x := startX; x <= endX && x < screenWidth; x++ {
			cell := getCellAt(x, startY)
			if cell != nil && cell.Content != "" {
				selectedText.WriteString(cell.Content)
			} else {
				selectedText.WriteRune(' ')
			}
		}
		return strings.TrimSpace(selectedText.String())
	}

	// Multi-line selection
	for y := startY; y <= endY; y++ {
		if y == startY {
			// First line - from startX to end
			for x := startX; x < screenWidth; x++ {
				cell := getCellAt(x, y)
				if cell != nil && cell.Content != "" {
					selectedText.WriteString(cell.Content)
				} else {
					selectedText.WriteRune(' ')
				}
			}
		} else if y == endY {
			// Last line - from start to endX
			for x := 0; x <= endX && x < screenWidth; x++ {
				cell := getCellAt(x, y)
				if cell != nil && cell.Content != "" {
					selectedText.WriteString(cell.Content)
				} else {
					selectedText.WriteRune(' ')
				}
			}
		} else {
			// Middle lines - full line
			for x := 0; x < screenWidth; x++ {
				cell := getCellAt(x, y)
				if cell != nil && cell.Content != "" {
					selectedText.WriteString(cell.Content)
				} else {
					selectedText.WriteRune(' ')
				}
			}
		}

		// Add newline between lines (except for last line)
		if y < endY {
			selectedText.WriteRune('\n')
		}
	}

	return strings.TrimSpace(selectedText.String())
}

// handleClipboardPaste processes clipboard content and sends it to the focused terminal
func handleClipboardPaste(o *app.OS) {
	if o.FocusedWindow < 0 || o.FocusedWindow >= len(o.Windows) {
		return
	}

	focusedWindow := o.GetFocusedWindow()
	if focusedWindow == nil {
		return
	}

	if o.ClipboardContent == "" {
		o.ShowNotification("Clipboard is empty", "warning", config.NotificationDuration)
		return
	}

	// Use the terminal's Paste method which automatically handles bracketed paste mode
	// If the application running in the terminal has enabled bracketed paste mode,
	// the text will be wrapped with \x1b[200~ ... \x1b[201~ escape sequences
	if focusedWindow.Terminal != nil {
		focusedWindow.Terminal.Paste(o.ClipboardContent)
		o.ShowNotification(fmt.Sprintf("Pasted %d characters", len(o.ClipboardContent)), "success", config.NotificationDuration)
	}
}
