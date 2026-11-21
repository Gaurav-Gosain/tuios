package app

import (
	"fmt"
	"runtime"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/charmbracelet/x/ansi"
)

// CaptureKeyEvent captures a keyboard event for the showkeys overlay.
// It handles key formatting, modifier extraction, and history management.
//
// A modifier pressed on its own (kitty keyboard reports it as a key, such as
// leftsuper) is not recorded: it is part of the chord that follows, which
// names it.
func (m *OS) CaptureKeyEvent(msg tea.KeyPressMsg) {
	key := msg.Key()
	if modifierOnlyKey(key.Code) {
		return
	}
	keyStr := msg.String()

	modifiers := []string{}
	if key.Mod&tea.ModCtrl != 0 {
		modifiers = append(modifiers, "Ctrl")
	}
	if key.Mod&tea.ModAlt != 0 {
		modifiers = append(modifiers, "Alt")
	}
	if key.Mod&tea.ModSuper != 0 {
		modifiers = append(modifiers, superLabel())
	}
	if key.Mod&tea.ModHyper != 0 {
		modifiers = append(modifiers, "Hyper")
	}
	if key.Mod&tea.ModMeta != 0 {
		modifiers = append(modifiers, "Meta")
	}
	if key.Mod&tea.ModShift != 0 && !isSingleLetter(keyStr) {
		modifiers = append(modifiers, "Shift")
	}

	displayKey := formatKeyDisplay(keyStr, modifiers)

	if len(m.RecentKeys) > 0 {
		lastKey := m.RecentKeys[len(m.RecentKeys)-1]
		if lastKey.Key == displayKey && slices.Equal(lastKey.Modifiers, modifiers) {
			m.RecentKeys[len(m.RecentKeys)-1].Count++
			m.RecentKeys[len(m.RecentKeys)-1].Timestamp = time.Now()
			return
		}
	}

	event := KeyEvent{
		Key:       displayKey,
		Modifiers: modifiers,
		Timestamp: time.Now(),
		Count:     1,
	}

	m.RecentKeys = append(m.RecentKeys, event)

	if len(m.RecentKeys) > m.showKeyCount() {
		m.RecentKeys = m.RecentKeys[1:]
	}
}

func modifierOnlyKey(code rune) bool {
	switch {
	case code >= tea.KeyLeftShift && code <= tea.KeyIsoLevel5Shift:
		return true
	case code == tea.KeyCapsLock, code == tea.KeyScrollLock, code == tea.KeyNumLock:
		return true
	}
	return false
}

func superLabel() string {
	if runtime.GOOS == "darwin" {
		return "Cmd"
	}
	return "Super"
}

func isSingleLetter(keyStr string) bool {
	return len(keyStr) == 1 && ((keyStr[0] >= 'a' && keyStr[0] <= 'z') || (keyStr[0] >= 'A' && keyStr[0] <= 'Z'))
}

func formatKeyDisplay(keyStr string, modifiers []string) string {
	displayKey := keyStr

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

	if len(modifiers) > 0 {
		parts := strings.Split(keyStr, "+")
		if len(parts) > 0 {
			baseKey := parts[len(parts)-1]
			if special, ok := specialKeys[baseKey]; ok {
				displayKey = special
			} else {
				displayKey = baseKey
			}
		}
	} else {
		if special, ok := specialKeys[keyStr]; ok {
			displayKey = special
		} else if len(keyStr) == 1 {
			displayKey = keyStr
		}
	}

	return displayKey
}

// CleanupExpiredKeys removes keys from the history that have expired based on timeout.
// Keys older than the timeout duration are removed.
func (m *OS) CleanupExpiredKeys(timeout time.Duration) {
	now := time.Now()
	for i := 0; i < len(m.RecentKeys); {
		if now.Sub(m.RecentKeys[i].Timestamp) > timeout {
			m.RecentKeys = slices.Delete(m.RecentKeys, i, i+1)
		} else {
			i++
		}
	}
}

func (m *OS) renderShowkeysFitted(maxWidth int) string {
	out := m.renderShowkeys()
	if maxWidth <= 0 || lipgloss.Width(out) <= maxWidth {
		return out
	}
	keys := m.RecentKeys
	for len(keys) > 1 && lipgloss.Width(out) > maxWidth {
		keys = keys[1:]
		out = renderShowkeysFrom(keys, &m.Settings)
	}
	if lipgloss.Width(out) > maxWidth {
		out = ansi.Truncate(out, maxWidth, "")
	}
	return out
}

func (m *OS) renderShowkeys() string {
	return renderShowkeysFrom(m.RecentKeys, &m.Settings)
}

func renderShowkeysFrom(recentKeys []KeyEvent, s *config.Settings) string {
	if len(recentKeys) == 0 {
		return ""
	}

	keyBgColor, keyFgColor := theme.ShowkeysKey()
	pillColor := keyBgColor

	leaderKeyBgColor, leaderKeyFgColor := theme.ShowkeysLeader()
	leaderKeyPillColor := leaderKeyBgColor

	keyPillStyle := lipgloss.NewStyle().
		Background(keyBgColor).
		Foreground(keyFgColor).
		Bold(true)

	leaderKeyPillStyle := lipgloss.NewStyle().
		Background(leaderKeyBgColor).
		Foreground(leaderKeyFgColor).
		Bold(true)

	pillStyle := lipgloss.NewStyle().
		Foreground(pillColor)

	leaderKeyPillStyle2 := lipgloss.NewStyle().
		Foreground(leaderKeyPillColor)

	var renderedKeys []string

	for _, keyEvent := range recentKeys {
		var keyStr string
		var modifierStr string

		if len(keyEvent.Modifiers) > 0 {
			modifierStr = strings.Join(keyEvent.Modifiers, "+") + " + "
			keyStr = modifierStr + keyEvent.Key
		} else {
			keyStr = keyEvent.Key
		}

		var normalizedKeyCombination string
		if len(keyEvent.Modifiers) > 0 {
			normalizedKeyCombination = strings.ToLower(strings.Join(keyEvent.Modifiers, "+")) + "+" + strings.ToLower(keyEvent.Key)
		} else {
			normalizedKeyCombination = strings.ToLower(keyEvent.Key)
		}

		if keyEvent.Count > 1 {
			keyStr += fmt.Sprintf(" ×%d", keyEvent.Count)
		}

		isLeaderKey := config.IsLeaderPress(config.CanonicalKey(normalizedKeyCombination), s.LeaderKey)

		var left, content, right string
		if isLeaderKey {
			left = leaderKeyPillStyle2.Render(s.GetWindowPillLeft())
			content = leaderKeyPillStyle.Render(" " + keyStr + " ")
			right = leaderKeyPillStyle2.Render(s.GetWindowPillRight())
		} else {
			left = pillStyle.Render(s.GetWindowPillLeft())
			content = keyPillStyle.Render(" " + keyStr + " ")
			right = pillStyle.Render(s.GetWindowPillRight())
		}

		renderedKeys = append(renderedKeys, left+content+right)
	}

	keysContent := strings.Join(renderedKeys, " ")

	return keysContent
}

func (m *OS) showKeyCount() int {
	if n := m.Settings.ShowKeyCount; n > 0 {
		return n
	}
	return config.ShowKeyCountDefault
}
