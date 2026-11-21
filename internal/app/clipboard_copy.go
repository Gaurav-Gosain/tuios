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

// CopyToClipboard writes text to the system clipboard now and says so. It is
// the unambiguous path: a drag release, where the gesture ended when the button
// came up and nothing can supersede it.
func (m *OS) CopyToClipboard(text string) tea.Cmd {
	if text == "" {
		return nil
	}
	m.CancelPendingCopy()
	m.NoteCopyFlash(m.GetFocusedWindow())
	m.ShowNotification(fmt.Sprintf("Copied %d chars", len(text)), "success", m.Settings.NotificationDuration)
	return m.clipboardWriteCmd(text)
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
	m.NoteCopyFlash(m.GetFocusedWindow())
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
	return tea.Batch(m.clipboardWriteCmd(text), m.SaveToPasteBuffers(text))
}

// PendingCopyText reports the text a deferred write is holding, for tests and
// for anything that needs to know a gesture has not settled yet.
func (m *OS) PendingCopyText() string { return m.pendingCopy }

func (m *OS) nativeClipboardAllowed() bool {
	if m.RemoteClient && !m.SSHIsLoopback {
		return false
	}
	return ShouldUseNativeClipboard(m.SSHIsLoopback)
}

func (m *OS) clipboardWriteCmd(text string) tea.Cmd {
	set := tea.SetClipboard(text)
	return func() tea.Msg {
		if m.nativeClipboardAllowed() {
			if tool := DetectClipboardTool(); tool != nil {
				_ = tool.Write(text)
			}
		}
		return set()
	}
}

// CopyFromEditor copies text a person selected in a text editor (a reply, a
// task, a reason) as a yank does: to the clipboard and the paste buffers.
// Unlike CopyToClipboard it shows no sweep on the focused pane, which the
// text did not come from.
func (m *OS) CopyFromEditor(text string) tea.Cmd {
	if text == "" {
		return nil
	}
	m.CancelPendingCopy()
	m.ShowNotification(fmt.Sprintf("Copied %d chars", len(text)), "success", m.Settings.NotificationDuration)
	return tea.Batch(m.clipboardWriteCmd(text), m.SaveToPasteBuffers(text))
}
