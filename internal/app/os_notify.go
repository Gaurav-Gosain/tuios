package app

import (
	"fmt"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/Gaurav-Gosain/tuios/internal/ui"
)

// Log adds a new log message to the log buffer.
func (m *OS) Log(level, format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	logMsg := LogMessage{
		Time:    time.Now(),
		Level:   level,
		Message: message,
	}

	// Check if we're at the bottom before adding new log
	wasAtBottom := false
	if m.ShowLogs {
		maxDisplayHeight := max(m.Height-8, 8)
		totalLogs := len(m.LogMessages)

		// Fixed overhead: title (1) + blank after title (1) + blank before hint (1) + hint (1) = 4
		fixedLines := 4
		// If scrollable, add scroll indicator: blank (1) + indicator (1) = 2
		if totalLogs > maxDisplayHeight-fixedLines {
			fixedLines = 6
		}
		logsPerPage := max(maxDisplayHeight-fixedLines, 1)

		maxScroll := max(totalLogs-logsPerPage, 0)
		// Consider "at bottom" if within 2 lines of the end (to handle edge cases)
		wasAtBottom = m.LogScrollOffset >= maxScroll-2
	}

	// Keep only last MaxLogMessages messages
	m.LogMessages = append(m.LogMessages, logMsg)
	if len(m.LogMessages) > config.MaxLogMessages {
		m.LogMessages = m.LogMessages[len(m.LogMessages)-config.MaxLogMessages:]
	}

	// Auto-scroll to bottom if we were already at bottom (sticky scroll)
	if wasAtBottom && m.ShowLogs {
		// Recalculate maxScroll with the new log added
		maxDisplayHeight := max(m.Height-8, 8)
		totalLogs := len(m.LogMessages)
		fixedLines := 4
		if totalLogs > maxDisplayHeight-fixedLines {
			fixedLines = 6
		}
		logsPerPage := max(maxDisplayHeight-fixedLines, 1)
		maxScroll := max(totalLogs-logsPerPage, 0)
		m.LogScrollOffset = maxScroll
	}
}

// LogInfo logs an informational message. INFO logs are skipped entirely unless
// verbose logging is enabled, so the format string and args are never evaluated
// into the ring buffer in the common (non-debug) case.
func (m *OS) LogInfo(format string, args ...any) {
	if !verboseLog {
		return
	}
	m.Log("INFO", format, args...)
}

// FireHook fires a hook event for a window, with the current workspace and
// session as context.
func (m *OS) FireHook(event hooks.Event, windowID, windowName string) {
	m.FireHookContext(event, hooks.Context{
		WindowID:   windowID,
		WindowName: windowName,
	})
}

// FireHookContext fires a hook event with an event-specific context. The
// workspace and session are filled in here so no caller has to remember them,
// and so every event carries them; leaving SessionID unset was why hook scripts
// could not tell which session invoked them.
func (m *OS) FireHookContext(event hooks.Event, ctx hooks.Context) {
	if m.HookManager == nil {
		return
	}
	if ctx.Workspace == 0 {
		ctx.Workspace = m.CurrentWorkspace
	}
	ctx.SessionID = m.SessionName
	m.HookManager.Fire(event, ctx)
}

// LogWarn logs a warning message.
func (m *OS) LogWarn(format string, args ...any) {
	m.Log("WARN", format, args...)
}

// LogError logs an error message.
func (m *OS) LogError(format string, args ...any) {
	m.Log("ERROR", format, args...)
}

// ShowNotification displays a temporary notification with animation.
func (m *OS) ShowNotification(message, notifType string, duration time.Duration) {
	notif := Notification{
		ID:        createID(),
		Message:   message,
		Type:      notifType,
		StartTime: time.Now(),
		Duration:  duration,
	}

	// Create fade-in animation (uses getter so it's instant when animations disabled)
	notif.Animation = &ui.Animation{
		StartTime: time.Now(),
		Duration:  config.GetAnimationDuration(),
		Progress:  0.0,
		Complete:  false,
	}

	m.Notifications = append(m.Notifications, notif)

	// Also log the notification
	switch notifType {
	case "error":
		m.LogError("%s", message)
	case "warning":
		m.LogWarn("%s", message)
	default:
		m.LogInfo("%s", message)
	}
}

// CleanupNotifications removes expired notifications.
func (m *OS) CleanupNotifications() {
	now := time.Now()
	var active []Notification

	for _, notif := range m.Notifications {
		if now.Sub(notif.StartTime) < notif.Duration {
			active = append(active, notif)
		}
	}

	m.Notifications = active
}
