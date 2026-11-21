package app

import (
	"fmt"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// Log adds a new log message to the log buffer.
func (m *OS) Log(level, format string, args ...any) {
	m.appendLog(LogMessage{
		Time:    time.Now(),
		Level:   level,
		Message: fmt.Sprintf(format, args...),
	})
}

func (m *OS) appendLog(entry LogMessage) {
	entry.Message = capText(entry.Message, logTextCap)
	atNewest := m.logList.Cursor >= len(m.LogMessages)-1
	m.LogMessages = append(m.LogMessages, entry)
	for len(m.LogMessages) > config.MaxLogMessages {
		drop := 0
		for i, e := range m.LogMessages[:len(m.LogMessages)-1] {
			if e.Level == "INFO" {
				drop = i
				break
			}
		}
		m.LogMessages = append(m.LogMessages[:drop], m.LogMessages[drop+1:]...)
		if drop < m.logList.Cursor {
			m.logList.Cursor--
		}
		if drop < m.logList.Scroll {
			m.logList.Scroll--
		}
	}
	m.logList.Cursor = max(m.logList.Cursor, 0)
	if atNewest {
		m.logList.Cursor = len(m.LogMessages) - 1
	}
}

// LogInfo logs an informational message. INFO logs are skipped entirely unless
// verbose logging is enabled, so the format string and args are never evaluated
// into the ring buffer in the common (non-debug) case.
//
//go:noinline
func (m *OS) LogInfo(format string, args ...any) {
	if !verboseLog() {
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

var sessionSideHooks = func() map[hooks.Event]bool {
	set := make(map[hooks.Event]bool)
	for _, ev := range session.SessionSideHookEvents() {
		set[ev] = true
	}
	return set
}()

func (m *OS) firesHere(event hooks.Event) bool {
	return !m.IsDaemonSession || !sessionSideHooks[event]
}

// HookRows reports what this client's hook table holds and what each command
// last did, for the list-hooks verb.
//
// It reports only the hooks this client fires. A daemon session's client still
// holds the session-side commands in its table, and listing one here would show
// it with no runs, which reads as "your hook never fired" when the daemon fired
// it. The daemon drops the other half of the split for the same reason.
func (m *OS) HookRows() []map[string]any {
	rows := m.HookManager.Rows("client")
	mine := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		name, _ := row["event"].(string)
		if m.firesHere(hooks.Event(name)) {
			mine = append(mine, row)
		}
	}
	return mine
}

// FireHookContext fires a hook event with an event-specific context. The
// workspace and session are filled in here so no caller has to remember them,
// and so every event carries them; leaving SessionID unset was why hook scripts
// could not tell which session invoked them.
//
// The dock is notified either way. A dock component watching an event is drawn
// by this client and has to refresh in it, whichever side ran the command.
func (m *OS) FireHookContext(event hooks.Event, ctx hooks.Context) {
	if m.HookManager == nil || !m.firesHere(event) {
		m.NotifyDockEvent(string(event))
		return
	}
	if ctx.Workspace == 0 {
		ctx.Workspace = m.CurrentWorkspace
	}
	ctx.SessionID = m.SessionName
	m.HookManager.Fire(event, ctx)
	m.NotifyDockEvent(string(event))
}

// LogWarn logs a warning message.
func (m *OS) LogWarn(format string, args ...any) {
	m.Log("WARN", format, args...)
}

// LogError logs an error message.
//
//go:noinline
func (m *OS) LogError(format string, args ...any) {
	m.Log("ERROR", format, args...)
}

const maxLiveNotifications = 16

func notificationLifetime(notifType string, requested time.Duration, s *config.Settings) (time.Duration, bool) {
	switch notifType {
	case "error":
		if s.NotificationErrorSticky {
			return 0, true
		}
		return max(requested, s.NotificationErrorDuration), false
	case "warning", "warn":
		return max(requested, s.NotificationWarningDuration), false
	default:
		if requested <= 0 {
			return 0, false
		}
		return max(requested, s.NotificationDuration), false
	}
}

// ShowNotification puts a message in the dock's right-hand block.
//
// Severity comes from notifType, and duration is a floor rather than the whole
// answer (see notificationLifetime).
//
//go:noinline
func (m *OS) ShowNotification(message, notifType string, duration time.Duration) {
	m.showNotification(message, notifType, "", duration, nil, 0, false)
}

func (m *OS) showAmbientNotification(message, notifType string, duration time.Duration) {
	m.showNotification(message, notifType, "", duration, nil, 0, true)
}

// ShowNotificationFrom is ShowNotification for a message that came from a pane.
// The message becomes clickable and gains a keyboard jump; everything else about
// it is identical.
func (m *OS) ShowNotificationFrom(message, notifType string, duration time.Duration, target NotifTarget) {
	m.showNotification(message, notifType, "", duration, &target, 0, false)
}

func (m *OS) showAgentNotification(message, notifType, agentState string, duration time.Duration, target NotifTarget) {
	m.showNotification(message, notifType, agentState, duration, &target, 0, false)
}

func (m *OS) showNotification(message, notifType, agentState string, duration time.Duration, target *NotifTarget, limit time.Duration, ambient bool) {
	message = capText(message, notifTextCap)
	source := m.notifSourceName(target)

	switch notifType {
	case "error":
		m.appendLog(LogMessage{Time: time.Now(), Level: "ERROR", Message: message, Source: source})
	case "warning", "warn":
		m.appendLog(LogMessage{Time: time.Now(), Level: "WARN", Message: message, Source: source})
	}

	if message == "" {
		return
	}

	if m.crash != nil {
		m.crashNotice = message
	}

	effective, sticky := notificationLifetime(notifType, duration, &m.Settings)
	if limit > 0 && !sticky {
		effective = min(effective, limit)
	}
	shown := effective > 0 || sticky

	if shown && m.calmHolds(notifType, agentState, target) {
		m.appendLog(LogMessage{Time: time.Now(), Level: "INFO", Message: message, Source: source})
		m.calm.held++
		m.MarkAllDirty()
		return
	}

	belowLevel := target == nil && notifSeverityRank(notifType) < notifLevelRank(m.Settings.NotificationLevel)

	if notifSeverityRank(notifType) < 2 && (!belowLevel || m.Settings.NotificationLogHidden) {
		if shown {
			m.appendLog(LogMessage{Time: time.Now(), Level: "INFO", Message: message, Source: source})
		} else {
			m.LogInfo("%s", message)
		}
	}
	if !shown || belowLevel {
		return
	}

	n := Notification{
		ID:        createID(),
		Message:   message,
		Type:      notifType,
		StartTime: time.Now(),
		Duration:  effective,
		Sticky:    sticky,
		Target:    target,

		AgentState: agentState,
		Source:     source,
		Ambient:    ambient,
	}
	m.Notifications = queueNotification(m.Notifications, n, n.StartTime)
	if target != nil || lipgloss.Width(message) > config.NotificationMaxWidth-notifChromeWidth {
		m.rememberMessage(messageEntry{ID: n.ID, Text: message, Level: notifType, Time: n.StartTime, Source: source, Target: target})
	}

	if len(m.Notifications) > maxLiveNotifications {
		m.Notifications = m.Notifications[len(m.Notifications)-maxLiveNotifications:]
	}
	if m.OnNotification != nil {
		m.OnNotification(message, notifType)
	}
}

func queueNotification(live []Notification, n Notification, now time.Time) []Notification {
	if last := len(live) - 1; n.Ambient && last >= 0 && !live[last].Ambient && !live[last].NotificationExpired(now) {
		live = append(live, live[last])
		live[last] = n
		return live
	}
	return append(live, n)
}

// ToggleLogViewer shows or hides the log overlay. Opening it lands on the
// newest entries: left at its last position, or at the top of the buffer, the
// viewer shows the oldest thing it holds, which is exactly the wrong place to
// look for what was missed.
func (m *OS) ToggleLogViewer() {
	m.ShowLogs = !m.ShowLogs
	if m.ShowLogs {
		m.logList.Cursor = max(len(m.LogMessages)-1, 0)
		m.logList.Scroll = m.logList.Cursor
	}
}

// NotificationExpired reports whether a message has outlived its duration. A
// sticky one never has.
func (n Notification) NotificationExpired(now time.Time) bool {
	if n.Sticky {
		return false
	}
	return now.Sub(n.StartTime) >= n.Duration
}

// CleanupNotifications retires expired messages and reports whether it removed
// any.
//
// The return value is what decouples dismissal from drawing. It is called from
// the tick, not from render composition: there a message could only expire on
// a frame drawn for some other reason, and a quiet session would serve the last
// frame from the render cache with the message still painted on it. The tick
// that retires something uses this result to draw one more frame so the
// message actually leaves the screen.
func (m *OS) CleanupNotifications() bool {
	if len(m.Notifications) == 0 || m.notifPaused() {
		return false
	}

	now := time.Now()
	active := m.Notifications[:0]
	for _, notif := range m.Notifications {
		if !notif.NotificationExpired(now) {
			active = append(active, notif)
		}
	}

	removed := len(active) != len(m.Notifications)
	m.Notifications = active
	return removed
}

// DismissNotifications takes the live messages off the dock and reports whether
// there was anything to take off.
//
// This is what esc does. Without it a sticky error had no exit at all, and
// every other message could only be waited out. It clears the whole queue
// rather than one message: the queue is drawn as a single block with a count,
// so dismissing one at a time would make the user press esc once per message
// they had never been given the chance to read individually anyway.
func (m *OS) DismissNotifications() bool {
	if len(m.Notifications) == 0 {
		return false
	}
	for _, n := range m.Notifications {
		if isRestartNotice(n.Target) {
			m.restartLater = true
		}
	}
	m.Notifications = nil
	return true
}

// ClearNotification takes the live messages that read exactly message off the
// dock and reports whether there were any.
//
// A notice that names a state, such as "ZOOM", has to leave when the state
// does. ShowNotification with an empty message cannot do that: an empty
// message draws nothing and removes nothing, so the notice stayed up for its
// whole lifetime after the state had ended. Only matching entries go, so a
// message that arrived in the meantime keeps its place.
//
// Only tuios's own notices match. A pane writes the text of its notifications
// (OSC 9, tuios notify), so a pane that sent "ZOOM" carries a Target and keeps
// its message when the user unzooms.
func (m *OS) ClearNotification(message string) bool {
	if len(m.Notifications) == 0 {
		return false
	}
	kept := m.Notifications[:0]
	for _, n := range m.Notifications {
		if n.Target != nil || n.Message != message {
			kept = append(kept, n)
		}
	}
	removed := len(kept) != len(m.Notifications)
	clear(m.Notifications[len(kept):])
	m.Notifications = kept
	return removed
}

func (m *OS) clearRestartNotice() {
	kept := m.Notifications[:0]
	for _, n := range m.Notifications {
		if !isRestartNotice(n.Target) {
			kept = append(kept, n)
		}
	}
	clear(m.Notifications[len(kept):])
	m.Notifications = kept
}

func (m *OS) dismissVisibleNotification() bool {
	if len(m.Notifications) == 0 {
		return false
	}
	if isRestartNotice(m.Notifications[len(m.Notifications)-1].Target) {
		m.restartLater = true
	}
	m.Notifications = m.Notifications[:len(m.Notifications)-1]
	return true
}
