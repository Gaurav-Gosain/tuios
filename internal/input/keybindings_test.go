package input

import (
	"bytes"
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

// TestLegacyKeyEncoding pins the xterm-style bytes the legacy encoder sends a
// pane: the modifier parameter, cursor keys, CSI tilde keys and function keys.
func TestLegacyKeyEncoding(t *testing.T) {
	modParam := func(mod tea.KeyMod) []byte { return []byte{byte('0' + getModParam(mod))} }
	tests := []struct {
		name string
		got  []byte
		want []byte
	}{
		{"mod none", modParam(0), []byte("1")},
		{"mod shift", modParam(tea.ModShift), []byte("2")},
		{"mod alt", modParam(tea.ModAlt), []byte("3")},
		{"mod shift+alt", modParam(tea.ModShift | tea.ModAlt), []byte("4")},
		{"mod ctrl", modParam(tea.ModCtrl), []byte("5")},
		{"mod shift+ctrl", modParam(tea.ModShift | tea.ModCtrl), []byte("6")},
		{"mod alt+ctrl", modParam(tea.ModAlt | tea.ModCtrl), []byte("7")},
		{"mod shift+alt+ctrl", modParam(tea.ModShift | tea.ModAlt | tea.ModCtrl), []byte("8")},
		// High bits are masked off.
		{"mod with extra bits", modParam(tea.ModShift | tea.KeyMod(128)), []byte("2")},

		{"up", getCursorSequence(tea.KeyUp), []byte("\x1b[A")},
		{"down", getCursorSequence(tea.KeyDown), []byte("\x1b[B")},
		{"right", getCursorSequence(tea.KeyRight), []byte("\x1b[C")},
		{"left", getCursorSequence(tea.KeyLeft), []byte("\x1b[D")},
		{"home", getCursorSequence(tea.KeyHome), []byte("\x1b[H")},
		{"end", getCursorSequence(tea.KeyEnd), []byte("\x1b[F")},
		{"cursor unknown", getCursorSequence('x'), nil},

		{"csi single digit", buildCSISequence(5, 1), []byte("\x1b[5~")},
		{"csi double digit", buildCSISequence(15, 1), []byte("\x1b[15~")},
		{"csi single digit with mod", buildCSISequence(5, 2), []byte("\x1b[5;2~")},
		{"csi double digit with mod", buildCSISequence(15, 3), []byte("\x1b[15;3~")},
		{"csi F9", buildCSISequence(20, 1), []byte("\x1b[20~")},
		{"csi shift+F9", buildCSISequence(20, 2), []byte("\x1b[20;2~")},

		{"F1", getFunctionKeySequence(tea.KeyF1, 1), []byte("\x1bOP")},
		{"F2", getFunctionKeySequence(tea.KeyF2, 1), []byte("\x1bOQ")},
		{"F3", getFunctionKeySequence(tea.KeyF3, 1), []byte("\x1bOR")},
		{"F4", getFunctionKeySequence(tea.KeyF4, 1), []byte("\x1bOS")},
		{"F5", getFunctionKeySequence(tea.KeyF5, 1), []byte("\x1b[15~")},
		{"F12", getFunctionKeySequence(tea.KeyF12, 1), []byte("\x1b[24~")},
		{"shift+F1", getFunctionKeySequence(tea.KeyF1, 2), []byte("\x1b[1;2P")},
		{"shift+F5", getFunctionKeySequence(tea.KeyF5, 2), []byte("\x1b[15;2~")},
		{"function unknown", getFunctionKeySequence('x', 1), nil},
	}
	for _, tt := range tests {
		if !bytes.Equal(tt.got, tt.want) {
			t.Errorf("%s: got %q, want %q", tt.name, tt.got, tt.want)
		}
	}
}

// TestHomeEndFollowDECCKM pins the fix for a pane whose shell cannot move its
// caret: under DECCKM (application cursor keys) Home and End must be sent as
// SS3, the sequences xterm sends and the ones terminfo's khome/kend name, not
// as the CSI forms the special-key table used unconditionally before.
func TestHomeEndFollowDECCKM(t *testing.T) {
	tests := []struct {
		name      string
		code      rune
		appCursor bool
		want      []byte
	}{
		{"home, application mode", tea.KeyHome, true, []byte("\x1bOH")},
		{"home, normal mode", tea.KeyHome, false, []byte("\x1b[H")},
		{"end, application mode", tea.KeyEnd, true, []byte("\x1bOF")},
		{"end, normal mode", tea.KeyEnd, false, []byte("\x1b[F")},
	}
	for _, tt := range tests {
		got := getRawKeyBytesWithMode(tea.KeyPressMsg{Code: tt.code}, tt.appCursor)
		if !bytes.Equal(got, tt.want) {
			t.Errorf("%s: got %q, want %q", tt.name, got, tt.want)
		}
	}
}

// TestKeysWithoutALegacyEncodingReachThePane feeds the bytes a host terminal
// really sends through the decoder tuios reads them with, and pins what a pane
// without the kitty protocol receives. Every one of these used to produce no
// bytes at all: keypad Enter did nothing, and neither did the keypad with
// NumLock off, SS3 keypad digits, Begin, or F13 and up.
func TestKeysWithoutALegacyEncodingReachThePane(t *testing.T) {
	tests := []struct {
		name       string
		host       string
		normal     string
		appCursors string
	}{
		{"keypad enter, kitty", "\x1b[57414u", "\r", "\r"},
		{"keypad enter, DECKPAM", "\x1bOM", "\r", "\r"},
		{"ctrl+keypad enter", "\x1b[57414;5u", "\n", "\n"},
		{"keypad 1, kitty", "\x1b[57400u", "1", "1"},
		// NumLock on rides along as a lock bit (129 = 1 + 128). The kitty
		// encoder hands this key to the legacy one under disambiguate, so the
		// lock bit must not turn it into something other than the digit.
		{"keypad 1, kitty, numlock on", "\x1b[57400;129u", "1", "1"},
		{"keypad plus, kitty, numlock on", "\x1b[57413;129u", "+", "+"},
		// Text keys the kitty encoder hands back under disambiguate when the
		// host was asked for report-all-keys: the legacy side sends the text.
		{"a, kitty, numlock on", "\x1b[97;129u", "a", "a"},
		{"a, kitty, capslock on", "\x1b[97;65u", "A", "A"},
		{"e acute, kitty", "\x1b[233u", "é", "é"},
		{"keypad 1, DECKPAM", "\x1bOq", "1", "1"},
		{"keypad plus, DECKPAM", "\x1bOk", "+", "+"},
		{"keypad up, numlock off", "\x1b[57419u", "\x1b[A", "\x1bOA"},
		{"keypad home, numlock off", "\x1b[57423u", "\x1b[H", "\x1bOH"},
		{"keypad delete, numlock off", "\x1b[57426u", "\x1b[3~", "\x1b[3~"},
		{"keypad begin, numlock off", "\x1b[57427u", "\x1b[E", "\x1bOE"},
		{"begin", "\x1b[E", "\x1b[E", "\x1bOE"},
		{"shift+keypad up", "\x1b[57419;2u", "\x1b[1;2A", "\x1b[1;2A"},
		{"f13", "\x1b[57376u", "\x1b[1;2P", "\x1b[1;2P"},
		{"f17", "\x1b[57380u", "\x1b[15;2~", "\x1b[15;2~"},
		{"f25", "\x1b[57388u", "\x1b[1;5P", "\x1b[1;5P"},
		{"ctrl+delete", "\x1b[3;5~", "\x1b[3;5~", "\x1b[3;5~"},
		{"shift+pgup", "\x1b[5;2~", "\x1b[5;2~", "\x1b[5;2~"},
	}
	var d uv.EventDecoder
	for _, tt := range tests {
		_, ev := d.Decode([]byte(tt.host))
		key, ok := ev.(uv.KeyPressEvent)
		if !ok {
			t.Fatalf("%s: %q decoded as %T, want a key press", tt.name, tt.host, ev)
		}
		msg := tea.KeyPressMsg(key)
		if got := getRawKeyBytesWithMode(msg, false); string(got) != tt.normal {
			t.Errorf("%s, normal mode: got %q, want %q", tt.name, got, tt.normal)
		}
		if got := getRawKeyBytesWithMode(msg, true); string(got) != tt.appCursors {
			t.Errorf("%s, application mode: got %q, want %q", tt.name, got, tt.appCursors)
		}
	}
}
