package input

import (
	"fmt"

	"github.com/Gaurav-Gosain/tuios/internal/app"
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
// not reported, as with keys. The work is app.PasteIntoFocused, which the paste
// buffers use too.
func forwardPasteToFocused(o *app.OS, text string) bool {
	return o.PasteIntoFocused(text)
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
