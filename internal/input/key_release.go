package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// forwardKeyReleaseToFocused passes a key release on to the focused pane when
// that pane asked for releases, and reports whether it did.
//
// Only a pane that pushed the kitty keyboard protocol's event-type flag gets
// them, so nothing changes for the shells and editors that never ask. The ones
// that do ask cannot work without them: a compositor in a pane turns each report
// into a wl_keyboard event, and a press with no matching release leaves the key
// held down, which is how one Enter became a screenful of them.
//
// The gate is terminal mode, the same one bracketed paste passes through, so a
// release struck while an overlay or window management has the keyboard is
// dropped rather than delivered to a pane that never saw the press.
func forwardKeyReleaseToFocused(msg tea.KeyReleaseMsg, o *app.OS) bool {
	if o.Mode != app.TerminalMode {
		return false
	}
	window := o.GetFocusedWindow()
	if window == nil || window.Terminal == nil {
		return false
	}
	encoded := vt.EncodeKeyReleaseCSIu(vtKeyFromBubbletea(tea.KeyPressMsg(msg.Key())), window.Terminal.KittyKeyboardFlags())
	if encoded == "" {
		return false
	}
	return window.SendInput([]byte(encoded)) == nil
}
