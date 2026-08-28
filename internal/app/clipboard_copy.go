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
	return m.clipboardWriteCmd(text)
}

// clipboardWriteCmd prefers a native system clipboard (wl-copy, xclip, pbcopy)
// when one is reachable, and falls back to OSC 52 (tea.SetClipboard) otherwise.
// OSC 52 is the right channel for remote clients with no local clipboard of
// their own; a native tool is the right channel for terminals like GNOME
// Terminal/Ptyxis whose VTE never implements OSC 52.
//
// The native path is only taken for a session that is both local (not SSH, not
// a browser client: under tuios ssh / tuios-web, WAYLAND_DISPLAY/DISPLAY
// describe the operator's desktop, and a native read/write would move data
// between two people) and VTE-hosted (the only family that never implements
// OSC 52; everywhere else spawning wl-copy/xclip would just duplicate entries
// in the clipboard manager on every selection for no gain).
func (m *OS) clipboardWriteCmd(text string) tea.Cmd {
	local := !m.IsSSHMode && !m.BrowserClient
	// The write is wrapped so the native path runs *and* the same
	// setClipboardMsg that tea.SetClipboard produces is still returned: Bubble
	// Tea forwards that message as OSC 52, which is harmless in terminals that do
	// not implement it and a second, redundant path in those that do. The
	// double write is idempotent (same text), and the native one is what makes
	// VTE-based terminals (GNOME Terminal, Ptyxis) work at all.
	return func() tea.Msg {
		if local && ShouldUseNativeClipboard(m.Settings.ClipboardLocalFallback) {
			// Best-effort: if the native write fails (permissions, compositor
			// gone), the OSC 52 message below is still the safety net.
			_ = DetectClipboardTool().Write(text)
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
	return m.clipboardWriteCmd(text)
}

// PendingCopyText reports the text a deferred write is holding, for tests and
// for anything that needs to know a gesture has not settled yet.
func (m *OS) PendingCopyText() string { return m.pendingCopy }
