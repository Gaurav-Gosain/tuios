package app

import (
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
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
	// Skip Shift modifier for single letter keys - it's implied by uppercase
	// We only show Shift for non-letter keys (like Shift+Space, Shift+Tab, etc.)
	if key.Mod&tea.ModShift != 0 && !isSingleLetter(keyStr) {
		modifiers = append(modifiers, "Shift")
	}

	// Format the key display string
	displayKey := formatKeyDisplay(keyStr, modifiers)

	// Check if the last key in history is the same as this one
	// Must match both the key AND the modifiers
	if len(m.RecentKeys) > 0 {
		lastKey := m.RecentKeys[len(m.RecentKeys)-1]
		// Check if key is the same AND modifiers match
		if lastKey.Key == displayKey && modifiersMatch(lastKey.Modifiers, modifiers) {
			// Same key with same modifiers pressed again, increment count
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

// isSingleLetter checks if a key string is a single letter (for shift detection)
func isSingleLetter(keyStr string) bool {
	return len(keyStr) == 1 && ((keyStr[0] >= 'a' && keyStr[0] <= 'z') || (keyStr[0] >= 'A' && keyStr[0] <= 'Z'))
}

// modifiersMatch checks if two modifier slices are equal
func modifiersMatch(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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
			for range keyEvent.Count {
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
			m.RecentKeys = slices.Delete(m.RecentKeys, i, i+1)
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

	// Use a muted but slightly more colorful background
	// #3a3a5e is a nice muted purple-blue, warmer than the pure dark gray
	keyBgColor := lipgloss.Color("#3a3a5e")
	pillColor := lipgloss.Color("#3a3a5e")

	// Accent color for the leader key (bright cyan for visibility)
	leaderKeyBgColor := lipgloss.Color("#00d9ff")
	leaderKeyPillColor := lipgloss.Color("#00d9ff")

	// Style for individual key pills - background with text
	keyPillStyle := lipgloss.NewStyle().
		Background(keyBgColor).
		Foreground(lipgloss.Color("#ffffff")).
		Bold(true)

	// Style for leader key pills
	leaderKeyPillStyle := lipgloss.NewStyle().
		Background(leaderKeyBgColor).
		Foreground(lipgloss.Color("#000000")).
		Bold(true)

	// Style for the pill characters (Powerline semicircles)
	// Colored to match the background so they blend nicely
	pillStyle := lipgloss.NewStyle().
		Foreground(pillColor)

	leaderKeyPillStyle2 := lipgloss.NewStyle().
		Foreground(leaderKeyPillColor)

	var renderedKeys []string

	for _, keyEvent := range m.RecentKeys {
		var keyStr string
		var modifierStr string

		// Build the key display with modifiers
		if len(keyEvent.Modifiers) > 0 {
			modifierStr = strings.Join(keyEvent.Modifiers, "+") + " + "
			keyStr = modifierStr + keyEvent.Key
		} else {
			keyStr = keyEvent.Key
		}

		// Build the normalized key combination (without display formatting) for leader key comparison
		// e.g., "ctrl+a" instead of "Ctrl + A"
		var normalizedKeyCombination string
		if len(keyEvent.Modifiers) > 0 {
			normalizedKeyCombination = strings.ToLower(strings.Join(keyEvent.Modifiers, "+")) + "+" + strings.ToLower(keyEvent.Key)
		} else {
			normalizedKeyCombination = strings.ToLower(keyEvent.Key)
		}

		// Add count indicator if > 1
		if keyEvent.Count > 1 {
			keyStr += fmt.Sprintf(" ×%d", keyEvent.Count)
		}

		// Check if this is the leader key
		isLeaderKey := normalizedKeyCombination == strings.ToLower(config.LeaderKey)

		// Create pill-style element using Powerline semicircles: ▌ key ▐
		var left, content, right string
		if isLeaderKey {
			// Use accent colors for leader key
			left = leaderKeyPillStyle2.Render(config.GetWindowPillLeft())
			content = leaderKeyPillStyle.Render(" " + keyStr + " ")
			right = leaderKeyPillStyle2.Render(config.GetWindowPillRight())
		} else {
			// Use default colors for regular keys
			left = pillStyle.Render(config.GetWindowPillLeft())
			content = keyPillStyle.Render(" " + keyStr + " ")
			right = pillStyle.Render(config.GetWindowPillRight())
		}

		renderedKeys = append(renderedKeys, left+content+right)
	}

	// Join keys horizontally with 1 cell padding between them
	keysContent := strings.Join(renderedKeys, " ")

	// Return just the styled keys content
	return keysContent
}
