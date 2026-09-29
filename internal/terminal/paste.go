package terminal

import "github.com/Gaurav-Gosain/tuios/internal/vt"

// Bracketed paste delimiters, DECSET 2004.
const (
	bracketedPasteStart = "\x1b[200~"
	bracketedPasteEnd   = "\x1b[201~"
)

// SanitizePaste is vt.SanitizePaste. See there.
func SanitizePaste(text string) string { return vt.SanitizePaste(text) }

// PastePayload is the bytes a paste of text sends to this window's
// application: the text without control characters, wrapped in the bracketed
// paste delimiters when the application turned bracketed paste on.
func (w *Window) PastePayload(text string) []byte {
	text = SanitizePaste(text)
	if w != nil && w.Terminal != nil && w.Terminal.BracketedPasteEnabled() {
		return []byte(bracketedPasteStart + text + bracketedPasteEnd)
	}
	return []byte(text)
}

// Paste sends text to the window's application as a paste. Every path that
// pastes into a pane goes through here, so none of them can skip the
// sanitizing.
func (w *Window) Paste(text string) error {
	return w.SendInput(w.PastePayload(text))
}
