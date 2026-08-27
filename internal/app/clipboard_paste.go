package app

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// hostPasteTimeout is how long tuios waits for the terminal to answer the OSC 52
// clipboard query before it tells the user nothing came back.
//
// The query is a request to a terminal that is free to ignore it, and many do:
// kitty answers a write and refuses a read unless clipboard_control names
// read-clipboard, and Ghostty asks the user first. Bubble Tea's ReadClipboard
// has no deadline of its own, so without this the refusal reads as a paste that
// simply never happens.
const hostPasteTimeout = 2 * time.Second

// PasteTimeoutMsg fires when a clipboard query has gone unanswered for
// hostPasteTimeout. Seq names the request it belongs to, so a reply that already
// landed, or a newer request, makes it a no-op.
type PasteTimeoutMsg struct {
	Seq uint64
}

// ClipboardReadUnsupportedReason explains why tuios cannot read the clipboard
// here, or "" when it can. It says what to do instead.
//
// A browser tab is the one client where the answer is known in advance. The
// terminal in the page never replies to the OSC 52 read query, because reading
// the clipboard is a permission the browser grants to a user gesture and not to
// a program on the far end of a socket. The browser's own paste is a real user
// gesture, so it works, and it arrives here as a bracketed paste.
func (m *OS) ClipboardReadUnsupportedReason() string {
	if !m.BrowserClient {
		return ""
	}
	return "The browser does not give the clipboard to tuios. Press ctrl+v to paste, or cmd+v on a Mac."
}

// RequestHostPaste asks the terminal for its clipboard, or says why it cannot.
// It is the one way in: the paste key and the pane menu's Paste row both call
// it, so neither can offer a paste the other has already found impossible.
func (m *OS) RequestHostPaste() tea.Cmd {
	if reason := m.ClipboardReadUnsupportedReason(); reason != "" {
		m.ShowNotification(reason, "warning", config.NotificationDuration)
		return nil
	}
	m.pasteSeq++
	m.pastePending = true
	seq := m.pasteSeq
	return tea.Batch(
		tea.ReadClipboard,
		tea.Tick(hostPasteTimeout, func(time.Time) tea.Msg { return PasteTimeoutMsg{Seq: seq} }),
	)
}

// NotePasteArrived records that the terminal answered, which disarms the
// timeout. Called from the clipboard message handler.
func (m *OS) NotePasteArrived() {
	m.pastePending = false
}

// pasteTimedOut reports whether msg belongs to a query that is still waiting,
// and disarms it. A stale timer answers false and says nothing.
func (m *OS) pasteTimedOut(msg PasteTimeoutMsg) bool {
	if !m.pastePending || msg.Seq != m.pasteSeq {
		return false
	}
	m.pastePending = false
	return true
}
