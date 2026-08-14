package app

import (
	"os"
	"os/exec"

	tea "charm.land/bubbletea/v2"
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
