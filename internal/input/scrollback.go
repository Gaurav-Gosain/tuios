package input

import (
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	tea "github.com/charmbracelet/bubbletea/v2"
)

// handleScrollbackModeKey handles keyboard input in scrollback mode
func handleScrollbackModeKey(msg tea.KeyPressMsg, o *app.OS, window *terminal.Window) (*app.OS, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		// Exit scrollback mode
		window.ExitScrollbackMode()
		o.ShowNotification("Scrollback Mode Exited", "info", config.NotificationDuration)
		return o, nil

	case "up", "k":
		// Scroll up one line
		window.ScrollUp(1)
		return o, nil

	case "down", "j":
		// Scroll down one line
		window.ScrollDown(1)
		if !window.ScrollbackMode {
			// Exited scrollback mode by scrolling to bottom
			o.ShowNotification("Scrollback Mode Exited", "info", config.NotificationDuration)
		}
		return o, nil

	case "pgup", "ctrl+b", "ctrl+u":
		// Scroll up by half a screen
		pageSize := max(window.Height/2, 1)
		window.ScrollUp(pageSize)
		return o, nil

	case "pgdown", "ctrl+f", "ctrl+d":
		// Scroll down by half a screen
		pageSize := max(window.Height/2, 1)
		window.ScrollDown(pageSize)
		if !window.ScrollbackMode {
			// Exited scrollback mode by scrolling to bottom
			o.ShowNotification("Scrollback Mode Exited", "info", config.NotificationDuration)
		}
		return o, nil

	case "home", "g":
		// Go to oldest scrollback line
		if window.Terminal != nil {
			window.ScrollbackOffset = window.ScrollbackLen()
			window.InvalidateCache()
		}
		return o, nil

	case "end", "G":
		// Go to newest scrollback line (bottom)
		window.ExitScrollbackMode()
		o.ShowNotification("Scrollback Mode Exited", "info", config.NotificationDuration)
		return o, nil

	default:
		// Ignore other keys in scrollback mode
		return o, nil
	}
}

// HandleScrollbackMouseWheel handles mouse wheel events for scrollback
func HandleScrollbackMouseWheel(window *terminal.Window, wheelUp bool) {
	if window == nil {
		return
	}

	// Mouse wheel in terminal mode should enter scrollback mode if not already
	if !window.ScrollbackMode {
		if wheelUp {
			window.EnterScrollbackMode()
		} else {
			// Scrolling down when not in scrollback mode does nothing
			return
		}
	}

	// Handle scrolling
	if wheelUp {
		window.ScrollUp(3) // Scroll 3 lines at a time
	} else {
		window.ScrollDown(3)
	}
}
