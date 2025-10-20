// Package input implements text selection logic for TUIOS.
package input

import (
	"fmt"
	"os"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// extractSelectedText extracts selected text from terminal based on selection coordinates
func extractSelectedText(window *terminal.Window, o *app.OS) string {
	if window.Terminal == nil {
		return ""
	}

	screen := window.Terminal.Screen()
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
			cell := screen.Cell(x, startY)
			if cell != nil && cell.Rune != 0 {
				selectedText.WriteRune(cell.Rune)
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
				cell := screen.Cell(x, y)
				if cell != nil && cell.Rune != 0 {
					selectedText.WriteRune(cell.Rune)
				} else {
					selectedText.WriteRune(' ')
				}
			}
		} else if y == endY {
			// Last line - from start to endX
			for x := 0; x <= endX && x < screenWidth; x++ {
				cell := screen.Cell(x, y)
				if cell != nil && cell.Rune != 0 {
					selectedText.WriteRune(cell.Rune)
				} else {
					selectedText.WriteRune(' ')
				}
			}
		} else {
			// Middle lines - full line
			for x := 0; x < screenWidth; x++ {
				cell := screen.Cell(x, y)
				if cell != nil && cell.Rune != 0 {
					selectedText.WriteRune(cell.Rune)
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

	// Use bracketed paste mode if supported (most modern terminals)
	var inputData []byte

	// Check if terminal likely supports bracketed paste
	termEnv := os.Getenv("TERM")
	supportsBracketedPaste := strings.Contains(termEnv, "xterm") ||
		strings.Contains(termEnv, "screen") ||
		strings.Contains(termEnv, "tmux") ||
		termEnv == "alacritty" || termEnv == "kitty"

	if supportsBracketedPaste {
		// Use bracketed paste mode to preserve formatting and prevent command execution
		inputData = []byte("\x1b[200~" + o.ClipboardContent + "\x1b[201~")
	} else {
		// Direct paste for terminals that don't support bracketed paste
		inputData = []byte(o.ClipboardContent)
	}

	err := focusedWindow.SendInput(inputData)
	if err != nil {
		o.ShowNotification(fmt.Sprintf("Failed to paste: %v", err), "error", config.NotificationDuration)
	} else {
		o.ShowNotification(fmt.Sprintf("Pasted %d characters", len(o.ClipboardContent)), "success", config.NotificationDuration)
	}
}
