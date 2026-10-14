package app

import (
	"time"

	tea "charm.land/bubbletea/v2"
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

// BrowserPasteKeys is what the browser in front of a tuios-web session does
// with the paste keys. The page decides it, and tuios-web tells the session.
type BrowserPasteKeys struct {
	// Apple is a Mac or an iOS device, where Cmd+V pastes.
	Apple bool
	// CtrlVToProgram is set when tuios-web runs with --ctrl-v-to-program,
	// so plain Ctrl+V reaches the pane and does not paste.
	CtrlVToProgram bool
}

// keys names the keys that paste, in the form the dock shows keys.
func (k BrowserPasteKeys) keys() string {
	switch {
	case k.Apple:
		return "cmd+v"
	case k.CtrlVToProgram:
		return "ctrl+shift+v or shift+insert"
	default:
		return "ctrl+v, ctrl+shift+v or shift+insert"
	}
}

// ClipboardReadUnsupportedReason explains why tuios cannot read the clipboard
// here, or "" when it can. It says what to do instead.
//
// A browser tab is the one client where the answer is known in advance. The
// terminal in the page never replies to the OSC 52 read query, because reading
// the clipboard is a permission the browser grants to a user gesture and not to
// a program on the far end of a socket. The browser's own paste is a real user
// gesture, so it works, and it arrives here as a bracketed paste.
//
// The page keeps the paste keys for the browser, so they do not reach tuios
// in a browser. This note is for the pane menu's Paste row and a paste key
// the user bound to some other chord.
func (m *OS) ClipboardReadUnsupportedReason() string {
	if !m.BrowserClient {
		return ""
	}
	return "The browser does not give the clipboard to tuios. Press " + m.BrowserPaste.keys() + " to paste."
}

// RequestHostPaste asks the terminal for its clipboard, or says why it cannot.
// It is the one way in: the paste key and the pane menu's Paste row both call
// it, so neither can offer a paste the other has already found impossible.
func (m *OS) RequestHostPaste() tea.Cmd {
	if reason := m.ClipboardReadUnsupportedReason(); reason != "" {
		m.ShowNotification(reason, "warning", m.Settings.NotificationDuration)
		return nil
	}
	m.pasteSeq++
	m.pastePending = true
	m.pasteAskedAt = time.Now()
	seq := m.pasteSeq

	// A terminal that never answers the OSC 52 read is asked natively instead,
	// when the human is at this machine (a local VTE terminal, or a loopback
	// SSH session). The reply is a tea.ClipboardMsg, so it lands in the same
	// handler the OSC 52 answer does. See clipboard_local.go.
	if m.nativeClipboardAllowed() {
		if tool := DetectClipboardTool(); tool != nil {
			return tea.Batch(
				func() tea.Msg {
					text, err := tool.Read()
					if err != nil {
						return PasteTimeoutMsg{Seq: seq}
					}
					return tea.ClipboardMsg{Content: text, Selection: 'c'}
				},
				tea.Tick(hostPasteTimeout, func(time.Time) tea.Msg { return PasteTimeoutMsg{Seq: seq} }),
			)
		}
	}

	return tea.Batch(
		tea.ReadClipboard,
		tea.Tick(hostPasteTimeout, func(time.Time) tea.Msg { return PasteTimeoutMsg{Seq: seq} }),
	)
}

// hostPasteReplyWindow is how long after a clipboard query tuios still takes
// the answer as the reply to it. It is long because a terminal can ask the
// user before it answers.
const hostPasteReplyWindow = time.Minute

// NotePasteArrived records that the terminal answered, which disarms the
// timeout. Called from the clipboard message handler.
func (m *OS) NotePasteArrived() {
	m.pastePending = false
}

// ClaimPasteReply reports whether a clipboard reply answers a query tuios
// sent, and uses that query up, so one query pastes at most once.
//
// A reply that answers nothing must not be pasted. The host terminal sends a
// clipboard reply to whoever asked, and tuios is not the only one who can ask:
// a pane can print its own OSC 52 query, or a sequence that makes the terminal
// answer one, and the reply then arrives here as if the user had pressed the
// paste key. Typed into the focused pane, it would run.
//
// An empty reply does not use the query up. It pastes nothing, and some
// terminals answer at once with an empty clipboard before the real answer
// arrives.
func (m *OS) ClaimPasteReply(content string) bool {
	asked := m.pasteAskedAt
	if content != "" {
		m.pasteAskedAt = time.Time{}
	}
	return !asked.IsZero() && time.Since(asked) <= hostPasteReplyWindow
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
