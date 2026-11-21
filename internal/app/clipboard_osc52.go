package app

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

type clipboardAsk struct {
	seq      uint64
	version  uint64
	text     string
	windowID string
	raised   time.Time
	shown    time.Time
}

const clipboardAskSettle = 500 * time.Millisecond

const clipboardPreviewLen = 24

func (m *OS) paneClipboardWrite(msg ClipboardSetMsg) tea.Cmd {
	mode := m.Settings.OSC52Write
	switch mode {
	case config.OSC52WriteOff:
		m.LogInfo("A pane set the clipboard. osc52_write is off, so the host clipboard is not changed.")
		return nil
	case config.OSC52WriteOn:
		return m.clipboardWriteCmd(msg.Text)
	case config.OSC52WriteAsk:
	default:
		if fw := m.GetFocusedWindow(); fw != nil && fw.ID == msg.WindowID {
			m.noteFocusedCopy(msg)
			return m.clipboardWriteCmd(msg.Text)
		}
	}
	m.askClipboardWrite(msg)
	return nil
}

func (m *OS) noteFocusedCopy(msg ClipboardSetMsg) {
	if !strings.ContainsFunc(msg.Text, func(r rune) bool {
		return (r < 0x20 && r != '\t') || r == 0x7f || (r >= 0x80 && r <= 0x9f)
	}) {
		return
	}
	lines := strings.Count(strings.TrimRight(msg.Text, "\n"), "\n") + 1
	preview := []rune(notifyPlainText(msg.Text))
	quoted := string(preview)
	if len(preview) > clipboardPreviewLen {
		quoted = string(preview[:clipboardPreviewLen]) + "..."
	}
	text := fmt.Sprintf("%s copied %d lines to the clipboard: %q.", m.clipboardPaneName(msg.WindowID), lines, quoted)
	if id := m.clipboardCopyNotes[msg.WindowID]; id != "" {
		for i := range m.Notifications {
			if m.Notifications[i].ID == id {
				m.Notifications[i].Message = text
				m.Notifications[i].StartTime = time.Now()
				return
			}
		}
	}
	before := len(m.Notifications)
	m.ShowNotificationFrom(text, "info", m.Settings.NotificationDuration, NotifTarget{WindowID: msg.WindowID})
	if n := len(m.Notifications); n > 0 && (n > before || m.Notifications[n-1].Message == text) {
		if m.clipboardCopyNotes == nil {
			m.clipboardCopyNotes = make(map[string]string)
		}
		m.clipboardCopyNotes[msg.WindowID] = m.Notifications[n-1].ID
	}
}

func (m *OS) askClipboardWrite(msg ClipboardSetMsg) {
	if m.clipboardAsks == nil {
		m.clipboardAsks = make(map[string]*clipboardAsk)
	}
	for id := range m.clipboardAsks {
		if m.windowByID(id) == nil {
			delete(m.clipboardAsks, id)
		}
	}
	ask := m.clipboardAsks[msg.WindowID]
	if ask == nil {
		m.clipboardAskSeq++
		ask = &clipboardAsk{seq: m.clipboardAskSeq, windowID: msg.WindowID}
		m.clipboardAsks[msg.WindowID] = ask
	}
	changed := ask.text != msg.Text && ask.version > 0
	if ask.text != msg.Text || ask.version == 0 {
		ask.version++
		ask.shown = time.Now()
	}
	ask.text = msg.Text
	text := m.clipboardAskText(ask)
	if changed {
		text = "The text changed. " + text
	}

	for i := range m.Notifications {
		if t := m.Notifications[i].Target; t != nil && t.ClipboardAsk == ask.seq {
			m.Notifications[i].Message = text
			m.Notifications[i].Target = &NotifTarget{ClipboardAsk: ask.seq, ClipboardVersion: ask.version}
			return
		}
	}
	if !ask.raised.IsZero() && time.Since(ask.raised) < m.notifyRateLimit() {
		return
	}
	ask.raised = time.Now()
	ask.shown = ask.raised
	m.ShowNotificationFrom(text, "warning", 2*m.Settings.NotificationDuration, NotifTarget{ClipboardAsk: ask.seq, ClipboardVersion: ask.version})
}

func (m *OS) clipboardAskText(ask *clipboardAsk) string {
	preview := []rune(notifyPlainText(ask.text))
	quoted := string(preview)
	if len(preview) > clipboardPreviewLen {
		quoted = string(preview[:clipboardPreviewLen]) + "..."
	}
	return fmt.Sprintf("%s asks to copy %d characters to the clipboard: %q. Click this message to allow it.",
		m.clipboardPaneName(ask.windowID), len([]rune(ask.text)), quoted)
}

func (m *OS) clipboardPaneName(windowID string) string {
	for i, w := range m.Windows {
		if w == nil || w.ID != windowID {
			continue
		}
		if name := printableTitle(w.CustomName); name != "" {
			return "Pane " + name
		}
		return fmt.Sprintf("Pane %d", i+1)
	}
	return "A pane"
}

func (m *OS) clipboardAskSettling(seq uint64) bool {
	for _, a := range m.clipboardAsks {
		if a.seq == seq {
			return time.Since(a.shown) < clipboardAskSettle
		}
	}
	return false
}

func (m *OS) allowClipboardAsk(seq, version uint64) {
	var ask *clipboardAsk
	for _, a := range m.clipboardAsks {
		if a.seq == seq {
			ask = a
			break
		}
	}
	if ask == nil {
		m.ShowNotification("That clipboard request is no longer open.", "info", m.Settings.NotificationDuration)
		return
	}
	if ask.version != version {
		m.ShowNotification("The text changed. Check the new request, then click it again.", "warning", m.Settings.NotificationDuration)
		return
	}
	if m.clipboardAskSettling(seq) {
		m.ShowNotification("Read the request, then click it again to allow it.", "info", m.Settings.NotificationDuration)
		return
	}
	delete(m.clipboardAsks, ask.windowID)
	text := ask.text
	m.clipboardApproved = &text
	m.ShowNotification(fmt.Sprintf("Copied %d characters to the clipboard.", len([]rune(text))), "success", m.Settings.NotificationDuration)
}

// ClipboardApprovalCmd returns the host clipboard write the user just allowed,
// once, or nil. The input handler calls it after every input, since a click
// or a key is what allows a write.
func (m *OS) ClipboardApprovalCmd() tea.Cmd {
	if m.clipboardApproved == nil {
		return nil
	}
	text := *m.clipboardApproved
	m.clipboardApproved = nil
	return m.clipboardWriteCmd(text)
}
