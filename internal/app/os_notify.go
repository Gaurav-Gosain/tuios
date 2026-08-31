package app

import (
	"fmt"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/Gaurav-Gosain/tuios/internal/session"
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

// sessionSideHooks are the events the daemon fires for a daemon session. The
// set is the daemon's own list rather than a copy, so the two sides cannot
// disagree about which events each owns.
var sessionSideHooks = func() map[hooks.Event]bool {
	set := make(map[hooks.Event]bool)
	for _, ev := range session.SessionSideHookEvents() {
		set[ev] = true
	}
	return set
}()

// firesHere reports whether this client runs the command for an event.
//
// A daemon session's window set, focus, workspace and agent states belong to the
// daemon, and the daemon fires their hooks. This client stays silent on those,
// which is what makes three attached clients produce one firing rather than
// three, and what makes the same hook fire when nobody is attached at all. A
// standalone tuios has no daemon, so it fires everything itself.
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
	// A dock component watching this event refreshes from the same firing. The
	// hook table and the component list are the two things a person wires to
	// "when X happens", so they are wired to the same X.
	m.NotifyDockEvent(string(event))
}

// LogWarn logs a warning message.
func (m *OS) LogWarn(format string, args ...any) {
	m.Log("WARN", format, args...)
}

// LogError logs an error message.
func (m *OS) LogError(format string, args ...any) {
	m.Log("ERROR", format, args...)
}

// maxLiveNotifications bounds the queue. Everything past the newest message is
// only ever reported as a count, so there is no reason to keep an unbounded
// backlog of them alive; the log viewer holds the full history.
const maxLiveNotifications = 16

// notificationLifetime resolves how long a message of this severity stays up,
// and whether it stays until dismissed.
//
// The caller's duration is a floor, not the answer. Every call site passes some
// duration it picked without much thought (config.NotificationDuration, that
// same value doubled, a literal two seconds), and the old default of 1500ms
// meant most of them were unreadable. Severity now decides the minimum, and a
// caller that deliberately asked for longer than the severity's default still
// gets the longer one.
//
// A non-positive duration means "do not show this", but only for info and
// success. Copy mode pushes its state indicators that way ("VISUAL", a bare
// "f", the pending count), and they have never been visible; promoting them to
// six-second dock messages is a copy-mode decision and not this change's to
// make.
//
// A warning or an error is shown whatever duration it was handed. Passing zero
// for one of those was never a considered choice, it was a caller reaching for
// "no timeout" and getting "no notification": os_selection asks for a sticky
// error when a capture fails and got silence, which is the worst possible
// outcome for the one message class the user cannot afford to miss.
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
// The signature is unchanged, so no call site had to be touched: severity comes
// from notifType exactly as it always did, and duration is now a floor rather
// than the whole answer (see notificationLifetime).
func (m *OS) ShowNotification(message, notifType string, duration time.Duration) {
	m.showNotification(message, notifType, duration, nil)
}

// ShowNotificationFrom is ShowNotification for a message that came from a pane.
// The message becomes clickable and gains a keyboard jump; everything else about
// it is identical.
func (m *OS) ShowNotificationFrom(message, notifType string, duration time.Duration, target NotifTarget) {
	m.showNotification(message, notifType, duration, &target)
}

func (m *OS) showNotification(message, notifType string, duration time.Duration, target *NotifTarget) {
	// Always log, even for a message that will not be shown: the log viewer is
	// where a message that was dropped or has already expired is read.
	switch notifType {
	case "error":
		m.LogError("%s", message)
	case "warning", "warn":
		m.LogWarn("%s", message)
	default:
		m.LogInfo("%s", message)
	}

	// An empty message has nothing to draw. It reaches here from copy mode,
	// whose handlers use it to mean "clear what I put up", and drawing an empty
	// block for it would leave a bare cap sitting on the dock.
	if message == "" {
		return
	}

	// The crash overlay stands in for the dock while it is up.
	//
	// A notification is drawn in the dock, and the dock is drawn by the
	// compositor, which the crash overlay replaces entirely. So a message
	// raised while the overlay is up has nowhere to go: pressing c copies the
	// report and the screen does not change, which reads as a key that does not
	// work. Mirroring it onto the overlay is done here, before the lifetime
	// gate below, so a message that would be dropped for being too short-lived
	// still reaches the one surface that is on screen. See CopyCrashReport.
	if m.crash != nil {
		m.crashNotice = message
	}

	effective, sticky := notificationLifetime(notifType, duration, &m.Settings)
	if effective <= 0 && !sticky {
		return
	}

	m.Notifications = append(m.Notifications, Notification{
		ID:        createID(),
		Message:   message,
		Type:      notifType,
		StartTime: time.Now(),
		Duration:  effective,
		Sticky:    sticky,
		Target:    target,
	})

	if len(m.Notifications) > maxLiveNotifications {
		m.Notifications = m.Notifications[len(m.Notifications)-maxLiveNotifications:]
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
// The return value is what decouples dismissal from drawing. This used to be
// called from inside render composition, so a message could only expire on a
// frame that was being drawn for some other reason; when the session went quiet
// the last frame was served from the render cache with the toast still painted
// on it, once for seventeen seconds. It is now called from the tick, and the
// tick that retires something uses this result to draw one more frame so the
// message actually leaves the screen.
func (m *OS) CleanupNotifications() bool {
	if len(m.Notifications) == 0 {
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
	m.Notifications = nil
	return true
}

// dismissVisibleNotification pops the message the block is currently drawing,
// revealing whatever was queued behind it. This is the granularity esc never
// had: the mouse addresses one message at a time, so the +N counter doubles as
// the way to read the queue.
func (m *OS) dismissVisibleNotification() bool {
	if len(m.Notifications) == 0 {
		return false
	}
	m.Notifications = m.Notifications[:len(m.Notifications)-1]
	return true
}
