package app

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

func logLine(m LogMessage) string {
	return m.Time.Format("15:04:05") + " [" + m.Level + "] " + m.Message
}

// CopyLogs puts the whole log on the clipboard.
func (m *OS) CopyLogs() tea.Cmd {
	return m.copyLogLines(m.LogMessages, "Copied the log.", "There is nothing in the log yet.")
}

// CopyLogErrors puts only the errors on the clipboard.
//
// Warnings are left out deliberately. This control exists for the paste into a
// bug report, and a warning that the build is a version behind is noise there.
func (m *OS) CopyLogErrors() tea.Cmd {
	errs := make([]LogMessage, 0, len(m.LogMessages))
	for _, entry := range m.LogMessages {
		if strings.EqualFold(entry.Level, "ERROR") {
			errs = append(errs, entry)
		}
	}
	return m.copyLogLines(errs, "Copied the errors.", "The log holds no errors.")
}

func (m *OS) copyLogLines(entries []LogMessage, done, empty string) tea.Cmd {
	if len(entries) == 0 {
		m.ShowNotification(empty, "info", m.Settings.NotificationDuration)
		return nil
	}
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		lines = append(lines, logLine(entry))
	}
	m.ShowNotification(done, "success", m.Settings.NotificationDuration)
	return tea.SetClipboard(strings.Join(lines, "\n"))
}
