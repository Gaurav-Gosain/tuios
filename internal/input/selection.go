package input

import (
	"fmt"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// forwardPasteToFocused sends paste text to the focused window's PTY, wrapping it in
// bracketed-paste markers when the inner app has that mode enabled and sending it raw
// otherwise. It never touches o.ClipboardContent and never shows a notification.
//
// With a multifocus set, the paste also goes to every window in the set, the way
// typed keys do (see HandleTerminalModeKey). Each window gets the markers only
// when its own app has bracketed paste on: a shell with it on and a program with
// it off must each see the paste the way they asked for it.
//
// It is used both by TUIOS's own clipboard paste (which layers notifications on top)
// and by the incoming-terminal-paste path, where a tea.PasteMsg is passthrough input
// (for example an fcitx5 IME commit) and must be delivered silently.
//
// SendInput() is used rather than writing to the emulator's internal pipe,
// which in daemon mode is drained by StartDaemonResponseReader() so the data
// would never reach the PTY; SendInput() routes through DaemonWriteFunc.
// Returns false when there is no focused window, the focused window is in copy
// mode, or the write to it fails. A failed write to another window in the set is
// not reported, as with keys.
func forwardPasteToFocused(o *app.OS, text string) bool {
	focusedWindow := o.GetFocusedWindow()
	if focusedWindow == nil {
		return false
	}
	if pasteBlockedByCopyMode(focusedWindow) {
		return false
	}

	ok := sendPaste(focusedWindow, text) == nil
	if len(o.MultifocusSet) > 0 {
		for idx, w := range o.Windows {
			if idx != o.FocusedWindow && o.MultifocusSet[w.ID] {
				_ = sendPaste(w, text)
			}
		}
	}
	return ok
}

// pasteBlockedByCopyMode prepares the focused window for a paste and reports
// whether copy mode must drop it.
//
// A scroll gesture leaves the pane in an implicit copy mode. A paste, like a
// typed key (see HandleTerminalModeKey), means the reading is over: snap back
// to live output and let the paste through, to this pane and to the set.
//
// Real copy mode, plain or multi, takes keys as motions. A paste is not a
// motion, and it must not reach the shell under the copy-mode view: in multi
// copy mode every pane of the set is in copy mode, and a paste would land in
// all of their shells unseen. So the paste is dropped.
func pasteBlockedByCopyMode(w *terminal.Window) bool {
	if w.InImplicitCopyMode() {
		w.ExitCopyMode()
	}
	return w.InCopyMode()
}

// sendPaste pastes text into one window. It goes through Window.Paste, which
// drops control characters, so a clipboard holding ESC[201~ cannot end the
// bracketed paste early in any pane of the set and have the rest run as
// typed input.
func sendPaste(w *terminal.Window, text string) error {
	return w.Paste(text)
}

// handleClipboardPaste processes stored clipboard content and sends it to the focused
// terminal, notifying the user of the result. This is the path for TUIOS's own paste
// actions (Cmd/Ctrl+V and the OSC 52 clipboard read response), not for incoming
// terminal paste.
func handleClipboardPaste(o *app.OS) {
	if o.GetFocusedWindow() == nil {
		return
	}

	if o.ClipboardContent == "" {
		o.ShowNotification("Clipboard is empty", "warning", o.Settings.NotificationDuration)
		return
	}

	if o.GetFocusedWindow().CopyModeVisible() {
		o.ShowNotification("Cannot paste in copy mode. Exit copy mode first.", "warning", o.Settings.NotificationDuration)
		return
	}

	if !forwardPasteToFocused(o, o.ClipboardContent) {
		o.ShowNotification("Paste failed", "error", o.Settings.NotificationDuration)
		return
	}

	o.ShowNotification(fmt.Sprintf("Pasted %d chars", len(o.ClipboardContent)), "success", o.Settings.NotificationDuration)
}
