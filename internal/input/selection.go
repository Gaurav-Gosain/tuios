// Package input implements clipboard paste routing for TUIOS.
package input

import (
	"fmt"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// forwardPasteToFocused sends paste text to the focused window's PTY, wrapping it in
// bracketed-paste markers when the inner app has that mode enabled and sending it raw
// otherwise. It never touches o.ClipboardContent and never shows a notification.
//
// It is used both by TUIOS's own clipboard paste (which layers notifications on top)
// and by the incoming-terminal-paste path, where a tea.PasteMsg is passthrough input
// (for example an fcitx5 IME commit) and must be delivered silently.
//
// SendInput() is used rather than writing to the emulator's internal pipe,
// which in daemon mode is drained by StartDaemonResponseReader() so the data
// would never reach the PTY; SendInput() routes through DaemonWriteFunc.
// Returns false when there is no focused window or the write fails.
func forwardPasteToFocused(o *app.OS, text string) bool {
	focusedWindow := o.GetFocusedWindow()
	if focusedWindow == nil {
		return false
	}

	pasteContent := text
	if focusedWindow.Terminal != nil && focusedWindow.Terminal.BracketedPasteEnabled() {
		pasteContent = "\x1b[200~" + pasteContent + "\x1b[201~"
	}

	return focusedWindow.SendInput([]byte(pasteContent)) == nil
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
		o.ShowNotification("Clipboard is empty", "warning", config.NotificationDuration)
		return
	}

	if !forwardPasteToFocused(o, o.ClipboardContent) {
		o.ShowNotification("Paste failed", "error", config.NotificationDuration)
		return
	}

	o.ShowNotification(fmt.Sprintf("Pasted %d chars", len(o.ClipboardContent)), "success", config.NotificationDuration)
}
