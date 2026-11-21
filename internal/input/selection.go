package input

import (
	"fmt"

	"github.com/Gaurav-Gosain/tuios/internal/app"
)

func forwardPasteToFocused(o *app.OS, text string) bool {
	return o.PasteIntoFocused(text)
}

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
