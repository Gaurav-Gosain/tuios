package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss/v2"
	tea "github.com/charmbracelet/bubbletea/v2"
)

// CaptureKeyEvent captures a keyboard event for the showkeys overlay.
// It handles key formatting, modifier extraction, and history management.
func (m *OS) CaptureKeyEvent(msg tea.KeyPressMsg) {
	key := msg.Key()
	keyStr := msg.String()

	// Extract modifiers from the key event
	modifiers := []string{}
	if key.Mod&tea.ModCtrl != 0 {
		modifiers = append(modifiers, "Ctrl")
	}
	if key.Mod&tea.ModAlt != 0 {
		modifiers = append(modifiers, "Alt")
	}
	if key.Mod&tea.ModShift != 0 {
		modifiers = append(modifiers, "Shift")
	}

	// Format the key display string
	displayKey := formatKeyDisplay(keyStr, modifiers)

	// Check if the last key in history is the same as this one
	if len(m.RecentKeys) > 0 {
		lastKey := m.RecentKeys[len(m.RecentKeys)-1]
		if lastKey.Key == displayKey {
			// Same key pressed again, increment count
			m.RecentKeys[len(m.RecentKeys)-1].Count++
			m.RecentKeys[len(m.RecentKeys)-1].Timestamp = time.Now()
			return
		}
	}

	// Add new key event to history
	event := KeyEvent{
		Key:       displayKey,
		Modifiers: modifiers,
		Timestamp: time.Now(),
		Count:     1,
	}

	m.RecentKeys = append(m.RecentKeys, event)

	// Maintain max history size (ring buffer)
	if len(m.RecentKeys) > m.KeyHistoryMaxSize {
		m.RecentKeys = m.RecentKeys[1:]
	}
}

// formatKeyDisplay formats a key string for display in the showkeys overlay.
// It converts raw key codes to human-readable names with proper modifier formatting.
func formatKeyDisplay(keyStr string, modifiers []string) string {
	// Remove modifiers from the key string if present
	// The key string might be something like "ctrl+a", we want just "a"
	displayKey := keyStr

	// Handle special key names from Bubble Tea
	specialKeys := map[string]string{
		"enter":     "Enter",
		"esc":       "Esc",
		"tab":       "Tab",
		"backspace": "Backspace",
		"delete":    "Delete",
		"up":        "↑",
		"down":      "↓",
		"left":      "←",
		"right":     "→",
		"home":      "Home",
		"end":       "End",
		"pgup":      "PgUp",
		"pgdn":      "PgDn",
		"space":     "Space",
	}

	// If we have modifiers, extract the actual key from the string
	if len(modifiers) > 0 {
		// Key string is like "ctrl+a" or "ctrl+shift+b"
		// Extract the last part which is the actual key
		parts := strings.Split(keyStr, "+")
		if len(parts) > 0 {
			baseKey := parts[len(parts)-1]
			if special, ok := specialKeys[baseKey]; ok {
				displayKey = special
			} else {
				// Preserve case for single characters with modifiers
				displayKey = baseKey
			}
		}
	} else {
		// No modifiers, just format the key
		if special, ok := specialKeys[keyStr]; ok {
			displayKey = special
		} else if len(keyStr) == 1 {
			// Single character key - preserve case
			displayKey = keyStr
		}
	}

	return displayKey
}

// GetShowkeysDisplayText generates the formatted text for the showkeys overlay.
// It returns a formatted string of recent key presses ready for display.
func (m *OS) GetShowkeysDisplayText() string {
	if len(m.RecentKeys) == 0 {
		return ""
	}

	var sb strings.Builder

	for i, keyEvent := range m.RecentKeys {
		if i > 0 {
			sb.WriteString("  ")
		}

		// Build the key display with modifiers
		if len(keyEvent.Modifiers) > 0 {
			sb.WriteString(strings.Join(keyEvent.Modifiers, "+"))
			sb.WriteString(" + ")
		}

		// Add key with count if > 1
		if keyEvent.Count > 1 {
			sb.WriteString(keyEvent.Key)
			sb.WriteString(" ")
			sb.WriteRune('×')
			sb.WriteString(" ")
			// Use a simple count representation
			for j := 0; j < keyEvent.Count; j++ {
				sb.WriteRune('·')
			}
		} else {
			sb.WriteString(keyEvent.Key)
		}
	}

	return sb.String()
}

// CleanupExpiredKeys removes keys from the history that have expired based on timeout.
// Keys older than the timeout duration are removed.
func (m *OS) CleanupExpiredKeys(timeout time.Duration) {
	now := time.Now()
	for i := 0; i < len(m.RecentKeys); {
		if now.Sub(m.RecentKeys[i].Timestamp) > timeout {
			// Remove expired key
			m.RecentKeys = append(m.RecentKeys[:i], m.RecentKeys[i+1:]...)
		} else {
			i++
		}
	}
}

// renderShowkeys renders the showkeys overlay with styled key display.
// Returns the rendered content as a styled lipgloss string.
func (m *OS) renderShowkeys() string {
	if len(m.RecentKeys) == 0 {
		return ""
	}

	// Style for individual key pills - just background, no border
	keyPillStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("#2a2a4e")).
		Foreground(lipgloss.Color("#ffffff")).
		Bold(true)

	// Style for the pill characters (matching the background color for the pill effect)
	pillStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#2a2a4e"))

	var renderedKeys []string

	for _, keyEvent := range m.RecentKeys {
		var keyStr string

		// Build the key display with modifiers
		if len(keyEvent.Modifiers) > 0 {
			keyStr = strings.Join(keyEvent.Modifiers, "+") + " + " + keyEvent.Key
		} else {
			keyStr = keyEvent.Key
		}

		// Add count indicator if > 1
		if keyEvent.Count > 1 {
			keyStr += fmt.Sprintf(" ×%d", keyEvent.Count)
		}

		// Create pill-style element: « key »
		left := pillStyle.Render("「")
		content := keyPillStyle.Render(keyStr)
		right := pillStyle.Render("」")

		renderedKeys = append(renderedKeys, left+content+right)
	}

	// Join keys horizontally with minimal spacing
	keysContent := lipgloss.JoinHorizontal(lipgloss.Center, renderedKeys...)

	// Return just the styled keys content without extra container padding
	return keysContent
}
