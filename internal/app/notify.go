package app

import (
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

func (m *OS) notifyRateLimit() time.Duration {
	return time.Duration(m.Settings.NotifyRateLimitMS) * time.Millisecond
}

func (m *OS) bellRateLimit() time.Duration {
	return time.Duration(m.Settings.BellRateLimitMS) * time.Millisecond
}

// NotificationMsg carries a guest desktop notification or bell from a window's
// PTY writer goroutine to the bubbletea Update loop, which calls ShowNotification.
// This mirrors ClipboardSetMsg: the callbacks run off the render goroutine and
// cannot mutate m.Notifications directly.
type NotificationMsg struct {
	Message  string
	Type     string
	Duration time.Duration
	// WindowID is the pane that raised it, which makes the message clickable.
	// The session is resolved on delivery instead of being captured here: this
	// runs on a PTY goroutine, and reading m.SessionName from it would race the
	// Update loop that rewrites it on a session switch.
	WindowID string
	// Bell is set for the terminal bell, which also marks the pane's
	// workspace tab. See workspace_marks.go.
	Bell bool
}

func listenNotificationUntil(stop <-chan struct{}, ch chan NotificationMsg) tea.Cmd {
	return listenOnce(stop, ch, func(notif NotificationMsg) tea.Msg { return notif })
}

func (m *OS) ensureNotificationChan() chan NotificationMsg {
	if m.PendingNotification == nil {
		m.PendingNotification = make(chan NotificationMsg, 16)
	}
	return m.PendingNotification
}

func (m *OS) setupNotificationPassthrough(window *terminal.Window) {
	if window == nil {
		return
	}

	ch := m.ensureNotificationChan()
	windowID := window.ID

	var mu sync.Mutex
	var lastNotify, lastBell time.Time

	window.NotifyFunc = func(title, body string) {
		mu.Lock()
		if time.Since(lastNotify) < m.notifyRateLimit() {
			mu.Unlock()
			return
		}
		lastNotify = time.Now()
		mu.Unlock()

		title = notifyPlainText(title)
		body = notifyPlainText(body)

		message := body
		switch {
		case title != "" && body != "":
			message = title + ": " + body
		case title != "":
			message = title
		}
		if message == "" {
			return
		}
		message = capText(message, notifTextCap)

		select {
		case ch <- NotificationMsg{Message: message, Type: "info", Duration: m.Settings.NotificationDuration, WindowID: windowID}:
		default:
		}

		if m.KittyPassthrough != nil {
			hostText := body
			if hostText == "" {
				hostText = title
			}
			if seq := hostNotifySequence(hostText, outerNone); len(seq) > 0 {
				m.KittyPassthrough.WriteToHost(seq)
			}
		}
	}

	window.BellFunc = func() {
		mu.Lock()
		if time.Since(lastBell) < m.bellRateLimit() {
			mu.Unlock()
			return
		}
		lastBell = time.Now()
		mu.Unlock()

		select {
		case ch <- NotificationMsg{Message: "bell", Type: "info", Duration: m.Settings.NotificationDuration, WindowID: windowID, Bell: true}:
		default:
		}
	}
}

func (m *OS) ringBell() bool {
	mode := m.Settings.Bell
	if mode == config.BellForward || mode == config.BellBoth {
		m.writeHostSequence([]byte{0x07})
	}
	return mode == config.BellToast || mode == config.BellBoth || mode == ""
}
