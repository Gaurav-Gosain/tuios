package app

import (
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/charmbracelet/x/ansi"
)

// Rate limits for guest-driven notifications. A guest that spams OSC 9 or BEL
// (for example `yes` piped to a terminal that beeps) must not flood the in-app
// notification stack or the host terminal.
const (
	notifyRateLimit = 500 * time.Millisecond
	bellRateLimit   = 200 * time.Millisecond
)

// NotificationMsg carries a guest desktop notification or bell from a window's
// PTY writer goroutine to the bubbletea Update loop, which calls ShowNotification.
// This mirrors ClipboardSetMsg: the callbacks run off the render goroutine and
// cannot mutate m.Notifications directly.
type NotificationMsg struct {
	Message  string
	Type     string
	Duration time.Duration
}

// ListenForNotification creates a command that waits for the next guest
// notification and delivers it to the Update loop as a NotificationMsg.
func ListenForNotification(ch chan NotificationMsg) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		notif, ok := <-ch
		if !ok {
			return nil
		}
		return notif
	}
}

// ensureNotificationChan lazily creates the notification channel. It is only
// called on the bubbletea goroutine (from Init and from window setup), so the
// nil check needs no lock.
func (m *OS) ensureNotificationChan() chan NotificationMsg {
	if m.PendingNotification == nil {
		m.PendingNotification = make(chan NotificationMsg, 16)
	}
	return m.PendingNotification
}

// setupNotificationPassthrough wires a window's guest notifications (OSC 9/777/99)
// and bell (BEL) to the in-app notification stack and, where appropriate, the host
// terminal. In-app notifications route through PendingNotification because these
// callbacks fire on the window's PTY writer goroutine while the render goroutine
// reads m.Notifications; the host writes go through KittyPassthrough.WriteToHost,
// which is mutex guarded and safe to call off-goroutine.
func (m *OS) setupNotificationPassthrough(window *terminal.Window) {
	if window == nil {
		return
	}

	ch := m.ensureNotificationChan()

	// Per-window rate-limit state, kept in the closure so each window is
	// independent and no shared OS map is touched from the PTY goroutine.
	var mu sync.Mutex
	var lastNotify, lastBell time.Time

	window.NotifyFunc = func(title, body string) {
		mu.Lock()
		if time.Since(lastNotify) < notifyRateLimit {
			mu.Unlock()
			return
		}
		lastNotify = time.Now()
		mu.Unlock()

		title = strings.TrimSpace(title)
		body = strings.TrimSpace(body)

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

		select {
		case ch <- NotificationMsg{Message: message, Type: "info", Duration: config.NotificationDuration}:
		default:
			// Channel full, drop (non-blocking).
		}

		// Forward to the host terminal so kitty/ghostty/wezterm raises a real
		// desktop notification (OSC 9). Prefer the body, fall back to the title.
		if m.KittyPassthrough != nil {
			hostText := body
			if hostText == "" {
				hostText = title
			}
			m.KittyPassthrough.WriteToHost([]byte(ansi.Notify(hostText)))
		}
	}

	window.BellFunc = func() {
		mu.Lock()
		if time.Since(lastBell) < bellRateLimit {
			mu.Unlock()
			return
		}
		lastBell = time.Now()
		mu.Unlock()

		select {
		case ch <- NotificationMsg{Message: "bell", Type: "info", Duration: config.NotificationDuration}:
		default:
			// Channel full, drop (non-blocking).
		}

		// The literal bell is intentionally not forwarded to the host: gating it
		// on focus would require reading OS window state (m.Windows/m.FocusedWindow)
		// from this PTY goroutine, which races the Update goroutine. The visual
		// bell above already identifies the window that rang.
	}
}
