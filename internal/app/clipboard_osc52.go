package app

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// A program in a pane sets the clipboard with OSC 52. The pane always keeps
// its own copy (terminal.Window.setClipboard), so the program reads back what
// it wrote. Whether the host clipboard gets it too is
// appearance.selection.osc52_write:
//
//   - off: never.
//   - ask: the dock asks, and activating the message allows it.
//   - focused (the default): the focused pane writes and the dock says so. A
//     write from any other pane asks.
//   - on: every pane writes.
//
// The reason for a setting at all is that OSC 52 is output, and any output can
// carry it: a file an agent prints, a log line, a web page in a text browser.
// Unchecked, whatever a pane prints can put a command on the clipboard for the
// user to paste later. See config.OSC52WriteModes for why focused is the
// default.

// clipboardAsk is a pane's clipboard write waiting for the user.
type clipboardAsk struct {
	seq  uint64
	text string
}

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
			m.ShowNotificationFrom(
				fmt.Sprintf("%s copied %d characters to the clipboard.", m.clipboardPaneName(msg.WindowID), len([]rune(msg.Text))),
				"info", m.Settings.NotificationDuration, NotifTarget{WindowID: msg.WindowID})
			return tea.SetClipboard(msg.Text)
		}
	}
	m.clipboardAskSeq++
	m.clipboardAsk = &clipboardAsk{seq: m.clipboardAskSeq, text: msg.Text}
	m.ShowNotificationFrom(
		fmt.Sprintf("%s asks to copy %d characters to the clipboard. Click this message to allow it.", m.clipboardPaneName(msg.WindowID), len([]rune(msg.Text))),
		"warning", 2*m.Settings.NotificationDuration, NotifTarget{ClipboardAsk: m.clipboardAskSeq})
	return nil
}

// clipboardPaneName is how a clipboard message names the pane that wrote.
func (m *OS) clipboardPaneName(windowID string) string {
	if w := m.windowByID(windowID); w != nil {
		if name := printableTitle(w.CustomName); name != "" {
			return "Pane " + name
		}
		if name := printableTitle(w.Title()); name != "" {
			return "Pane " + name
		}
	}
	return "A pane"
}

// allowClipboardAsk lets the waiting write with this number through. Only the
// newest write waits, so an older message has nothing left to allow.
func (m *OS) allowClipboardAsk(seq uint64) {
	ask := m.clipboardAsk
	if ask == nil || ask.seq != seq {
		m.ShowNotification("That clipboard request is no longer open.", "info", m.Settings.NotificationDuration)
		return
	}
	m.clipboardAsk = nil
	m.clipboardApproved = &ask.text
	m.ShowNotification(fmt.Sprintf("Copied %d characters to the clipboard.", len([]rune(ask.text))), "success", m.Settings.NotificationDuration)
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
