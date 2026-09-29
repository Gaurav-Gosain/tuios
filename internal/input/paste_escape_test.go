package input

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// pastedBody checks that sent is one bracketed paste whose body holds no ESC,
// and returns the body.
func pastedBody(t *testing.T, sent string) string {
	t.Helper()
	if !strings.HasPrefix(sent, "\x1b[200~") || !strings.HasSuffix(sent, "\x1b[201~") {
		t.Fatalf("PTY received %q, want one bracketed paste", sent)
	}
	body := strings.TrimSuffix(strings.TrimPrefix(sent, "\x1b[200~"), "\x1b[201~")
	if strings.ContainsRune(body, 0x1b) {
		t.Fatalf("paste body carried an ESC to the pane: %q", body)
	}
	return body
}

// A clipboard can hold ESC[201~. Pasted verbatim, it ends the bracketed paste
// early and the shell runs the rest as typed input. xterm and kitty drop ESC
// from pasted text, and tuios must do the same on its own paste path.
func TestClipboardPasteCannotEndTheBracketEarly(t *testing.T) {
	o, sent := pasteHarness(t, true)
	o.ClipboardContent = "safe\x1b[201~touch /tmp/pwned\n"

	handleClipboardPaste(o)

	body := pastedBody(t, sent.String())
	if !strings.Contains(body, "touch /tmp/pwned") {
		t.Fatalf("paste body lost its text: %q", body)
	}
}

// The outer terminal's paste goes through the same wrapper, and gets the same
// treatment.
func TestIncomingPasteCannotEndTheBracketEarly(t *testing.T) {
	o, sent := pasteHarness(t, true)

	_, _ = HandleInput(tea.PasteMsg{Content: "a\x1b[201~b\x1b[200~c"}, o)

	if body := pastedBody(t, sent.String()); body != "a[201~b[200~c" {
		t.Fatalf("paste body = %q, want the text with ESC removed", body)
	}
}

// Without bracketed paste the text still loses ESC and other control
// characters, but keeps tabs and line breaks.
func TestUnbracketedPasteDropsControlCharacters(t *testing.T) {
	o, sent := pasteHarness(t, false)

	_, _ = HandleInput(tea.PasteMsg{Content: "a\x1b[31mb\x07c\td\ne\x9bf"}, o)

	if got := sent.String(); got != "a[31mbc\td\nef" {
		t.Fatalf("PTY received %q, want control characters removed", got)
	}
}
