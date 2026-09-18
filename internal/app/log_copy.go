package app

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// The log viewer's two copy controls.
//
// Its hints have advertised "E copy errors" and "A copy all" for as long as the
// viewer has existed, and the key handler ignored both: every key it does not
// recognise falls through to a bare return. So the viewer drew two promises and
// kept neither, which is worse than not offering them, because a person who
// presses one has no way to tell whether the copy failed or the clipboard did.
//
// Copying out of the viewer matters more than copying out of a pane, because
// the thing worth copying is usually an error somebody is about to paste into a
// report, and the viewer is exactly where that error is legible.

// logLine is one entry as a person would paste it: the time, the level, and the
// message, which is what the viewer draws.
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

// copyLogLines is the shared half: it says what happened either way, because a
// copy that quietly puts nothing on the clipboard is indistinguishable from one
// that did not run.
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
