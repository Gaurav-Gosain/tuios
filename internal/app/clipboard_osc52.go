package app

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// A program in a pane sets the clipboard with OSC 52. The pane always keeps
// its own copy (terminal.Window.setClipboard), so the program reads back what
// it wrote. Whether the host clipboard gets it too is
// appearance.selection.osc52_write:
//
//   - off: never.
//   - ask: the dock asks, and a click on the message allows it. The key that
//     jumps to a message skips these, so no stray key allows a write.
//   - focused (the default): the focused pane writes and the dock says so. A
//     write from any other pane asks.
//   - on: every pane writes.
//
// The reason for a setting at all is that OSC 52 is output, and any output can
// carry it: a file an agent prints, a log line, a web page in a text browser.
// Unchecked, whatever a pane prints can put a command on the clipboard for the
// user to paste later. See config.OSC52WriteModes for why focused is the
// default.

// clipboardAsk is a pane's clipboard write waiting for the user. There is at
// most one per pane: a newer write from the same pane replaces its text, so a
// pane that writes fifty times raises one message, not fifty.
type clipboardAsk struct {
	seq      uint64
	version  uint64
	text     string
	windowID string
	// raised is when the ask last put a message on the dock. A pane whose
	// message the user dismissed raises a new one at most once per
	// notifyRateLimit, the limit guest notifications keep.
	raised time.Time
}

// clipboardPreviewLen is how many characters of the text an ask shows.
const clipboardPreviewLen = 24

// paneClipboardWrite decides what a pane's OSC 52 write does to the host
// clipboard and returns the command that writes it, or nil.
func (m *OS) paneClipboardWrite(msg ClipboardSetMsg) tea.Cmd {
	mode := m.Settings.OSC52Write
	switch mode {
	case config.OSC52WriteOff:
		m.LogInfo("A pane set the clipboard. osc52_write is off, so the host clipboard is not changed.")
		return nil
	case config.OSC52WriteOn:
		return tea.SetClipboard(msg.Text)
	case config.OSC52WriteAsk:
	default:
		// focused, and anything unknown, which is treated as the default.
		if fw := m.GetFocusedWindow(); fw != nil && fw.ID == msg.WindowID {
			m.noteFocusedCopy(msg)
			return tea.SetClipboard(msg.Text)
		}
	}
	m.askClipboardWrite(msg)
	return nil
}

// noteFocusedCopy says on the dock that the focused pane set the clipboard,
// when the text is of the kind that runs something if pasted: it holds a line
// break or a control character. A one-line yank, which is most of what an
// editor copies, says nothing. Each pane has one such line, updated in place,
// so a pane that copies often does not push other messages off the dock.
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

// askClipboardWrite records a pane's write as waiting and shows, or updates,
// the one dock message for that pane.
func (m *OS) askClipboardWrite(msg ClipboardSetMsg) {
	if m.clipboardAsks == nil {
		m.clipboardAsks = make(map[string]*clipboardAsk)
	}
	// An ask from a pane that has closed can never be read in context again.
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
	}
	ask.text = msg.Text
	text := m.clipboardAskText(ask)
	if changed {
		text = "The text changed. " + text
	}

	// The pane's message is still up: the newest text replaces it in place,
	// and its target moves to the new version. A click aimed at the old text
	// then no longer matches what was drawn, and is dropped.
	for i := range m.Notifications {
		if t := m.Notifications[i].Target; t != nil && t.ClipboardAsk == ask.seq {
			m.Notifications[i].Message = text
			m.Notifications[i].Target = &NotifTarget{ClipboardAsk: ask.seq, ClipboardVersion: ask.version}
			return
		}
	}
	if !ask.raised.IsZero() && time.Since(ask.raised) < notifyRateLimit {
		return
	}
	ask.raised = time.Now()
	m.ShowNotificationFrom(text, "warning", 2*m.Settings.NotificationDuration, NotifTarget{ClipboardAsk: ask.seq, ClipboardVersion: ask.version})
}

// clipboardAskText is the dock message for an ask.
func (m *OS) clipboardAskText(ask *clipboardAsk) string {
	preview := []rune(notifyPlainText(ask.text))
	quoted := string(preview)
	if len(preview) > clipboardPreviewLen {
		quoted = string(preview[:clipboardPreviewLen]) + "..."
	}
	return fmt.Sprintf("%s asks to copy %d characters to the clipboard: %q. Click this message to allow it.",
		m.clipboardPaneName(ask.windowID), len([]rune(ask.text)), quoted)
}

// clipboardPaneName is how a clipboard message names the pane that wrote: by
// the name the user gave it, or by its number. Never by the title the pane
// sets itself, since the pane could name itself after another one.
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

// allowClipboardAsk lets the waiting write with this number through.
// version is the text version the activated message showed. An ask whose text
// has changed since is not allowed.
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
	return tea.SetClipboard(text)
}
