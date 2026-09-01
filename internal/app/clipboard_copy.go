package app

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
)

// PendingCopyMsg fires when a multi-click selection's window has closed and the
// gesture can finally be read as what it was. Seq names the gesture it belongs
// to; a message whose sequence has been superseded is dropped.
type PendingCopyMsg struct {
	Seq uint64
}

// The clipboard write for a multi-click selection is deferred, because until
// the multi-click window closes nobody knows what the gesture is.
//
// A triple-click arrives as a double-click followed by a third press. Copying
// on each release wrote the word first and the line second, so every
// triple-click clobbered the clipboard with the wrong text on the way to the
// right one, and a paste that landed in between got the word. Waiting until no
// further click can join the gesture means only its final reading is ever
// written.
//
// Only the write waits. The highlight is applied on the press, so a
// double-click still shows the word instantly and visibly widens to the line
// on the third click. Deferring the feedback as well would trade one problem
// for a laggier one.

// CopyToClipboard writes text to the system clipboard now and says so. It is
// the unambiguous path: a drag release, where the gesture ended when the button
// came up and nothing can supersede it.
func (m *OS) CopyToClipboard(text string) tea.Cmd {
	if text == "" {
		return nil
	}
	m.CancelPendingCopy()
	m.ShowNotification(fmt.Sprintf("Copied %d chars", len(text)), "success", m.Settings.NotificationDuration)
	return m.WriteClipboard(text)
}

// WriteClipboard writes text to the system clipboard and is the one write
// path: copy mode's y, the scrollback browser, link and path copies, drag and
// multi-click selections, and the pane menu all land here. It prefers a native
// system clipboard (wl-copy, xclip, pbcopy) when one is reachable for this
// session, and always returns the OSC 52 message (tea.SetClipboard) on top.
//
// The native write is decided by nativeClipboardTool, here on the Update loop;
// the Cmd it returns only closes over the tool pointer and the text. The OSC
// 52 message is still returned when the native path ran: in the VTE terminals
// the gate allows it is ignored (they do not implement OSC 52, which is why
// the native tool exists at all), and if the host detection ever misfires on a
// terminal that does implement it, the query lands the text anyway. Nothing
// ever writes twice: where OSC 52 works, the native path is off, and where the
// native path is on, OSC 52 is ignored.
func (m *OS) WriteClipboard(text string) tea.Cmd {
	tool := m.nativeClipboardTool()
	return func() tea.Msg {
		if tool != nil {
			// Best-effort: if the native write fails (permissions, compositor
			// gone), the OSC 52 message below is still the safety net.
			_ = tool.Write(text)
		}
		return tea.SetClipboard(text)()
	}
}

// DeferCopyToClipboard holds text back until delay has passed with no further
// click, then writes it. A press arriving in the meantime calls
// CancelPendingCopy and this write never happens.
//
// The text is captured now rather than re-read when the timer fires: it is
// correct at this instant, and the pane may have scrolled or been closed by the
// time the message arrives.
func (m *OS) DeferCopyToClipboard(text string, delay time.Duration) tea.Cmd {
	if text == "" {
		return nil
	}
	m.selectionSeq++
	m.pendingCopy = text
	seq := m.selectionSeq
	return tea.Tick(max(delay, time.Millisecond), func(time.Time) tea.Msg {
		return PendingCopyMsg{Seq: seq}
	})
}

// CancelPendingCopy discards a deferred write and makes its timer a no-op when
// it fires. Every press calls this, so the clipboard only ever ends up holding
// what the last completed gesture selected.
func (m *OS) CancelPendingCopy() {
	m.selectionSeq++
	m.pendingCopy = ""
}

// HandlePendingCopy performs a deferred write if seq is still the current
// gesture. A superseded sequence means a later click reinterpreted the
// selection, and the text this timer was carrying was never what the user
// ended up with.
func (m *OS) HandlePendingCopy(seq uint64) tea.Cmd {
	if seq != m.selectionSeq || m.pendingCopy == "" {
		return nil
	}
	text := m.pendingCopy
	m.pendingCopy = ""
	m.ShowNotification(fmt.Sprintf("Copied %d chars", len(text)), "success", m.Settings.NotificationDuration)
	return m.WriteClipboard(text)
}

// PendingCopyText reports the text a deferred write is holding, for tests and
// for anything that needs to know a gesture has not settled yet.
func (m *OS) PendingCopyText() string { return m.pendingCopy }
