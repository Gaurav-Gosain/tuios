package terminal

import (
	"strings"
	"unicode/utf8"
)

// Bracketed paste delimiters, DECSET 2004.
const (
	bracketedPasteStart = "\x1b[200~"
	bracketedPasteEnd   = "\x1b[201~"
)

// SanitizePaste removes from text every character that could act as a
// terminal control rather than as pasted text: ESC, the other C0 controls
// except tab, line feed and carriage return, DEL, the C1 controls, and bytes
// that are not valid UTF-8.
//
// The ESC is the one that matters. Text holding ESC[201~ ends a bracketed
// paste early, and whatever follows reaches the application as typed input,
// so a shell runs it. The text can come from anywhere a pane can reach: an
// OSC 52 write, a hint match, a line of scrollback. xterm and kitty strip the
// same characters from a paste for the same reason. The rest go too because a
// paste is text, and a lone BEL, NUL or C1 CSI in it is never what the person
// meant to type.
func SanitizePaste(text string) string {
	clean := true
	for i := 0; i < len(text); i++ {
		if c := text[i]; c < 0x20 && c != '\t' && c != '\n' && c != '\r' || c >= 0x7f {
			clean = false
			break
		}
	}
	if clean {
		return text
	}
	var b strings.Builder
	b.Grow(len(text))
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		i += size
		switch {
		case r == utf8.RuneError && size == 1:
			continue
		case r == '\t' || r == '\n' || r == '\r':
		case r < 0x20, r >= 0x7f && r <= 0x9f:
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

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
