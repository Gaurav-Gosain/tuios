package vt

import (
	"fmt"
	"io"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// TestKittyKeyboardFlags drives the kitty keyboard flag stack through the
// sequences a guest sends: push (CSI > u), pop (CSI < u), set in each of its
// three modes (CSI = u), and a full reset.
func TestKittyKeyboardFlags(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want int
	}{
		{"push", "\x1b[>1u", 1},
		{"pop returns to the entry below", "\x1b[>3u\x1b[>15u\x1b[<1u", 3},
		{"pop two", "\x1b[>1u\x1b[>3u\x1b[<2u", 0},
		{"pop below the base stops at the base", "\x1b[>1u\x1b[<5u", 0},
		{"set mode 1 replaces the flags", "\x1b[>1u\x1b[=2;1u", 2},
		{"set mode 2 adds to the flags", "\x1b[>1u\x1b[=2;2u", 3},
		{"set mode 3 removes from the flags", "\x1b[>3u\x1b[=2;3u", 1},
		{"RIS clears the stack", "\x1b[>15u\x1bc", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEmulator(80, 24)
			defer e.Close()
			_, _ = e.Write([]byte(tc.in))
			if got := e.KittyKeyboardFlags(); got != tc.want {
				t.Errorf("flags = %d, want %d", got, tc.want)
			}
		})
	}

	t.Run("query via CSI ? u", func(t *testing.T) {
		e := NewEmulator(80, 24)
		defer e.Close()
		_, _ = e.Write([]byte("\x1b[>5u"))

		responseChan := make(chan string, 1)
		go func() {
			buf := make([]byte, 256)
			n, err := e.Read(buf)
			if err != nil && err != io.EOF {
				responseChan <- "read error: " + err.Error()
				return
			}
			responseChan <- string(buf[:n])
		}()
		_, _ = e.Write([]byte("\x1b[?u"))

		select {
		case response := <-responseChan:
			if response != "\x1b[?5u" {
				t.Errorf("response = %q, want %q", response, "\x1b[?5u")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for response")
		}
	})
}

// TestEncodeKeyCSIu pins the CSI u encoding of a key press for each flag set a
// pane can ask for.
//
// The associated-text field is the third CSI u field, sent once a pane sets
// the report-associated-keys flag. Without it an app that asked for it
// (terminal-browser escalates to CSI >27u on text focus, awrit pushes CSI >31u)
// inserts the base key code, so Shift+A types "a" and shifted symbols come out
// wrong. These are the exact bytes those parsers turn back into the typed
// character.
//
// The modifier weights are shift 1, alt 2, ctrl 4, super 8, plus one.
func TestEncodeKeyCSIu(t *testing.T) {
	const disambiguate = ansi.KittyDisambiguateEscapeCodes
	const all = ansi.KittyAllFlags                    // 31: disambiguate|events|alternate|all-keys|assoc
	const focus = ansi.KittyDisambiguateEscapeCodes | // 27: what terminal-browser pushes on text focus
		ansi.KittyReportEventTypes |
		ansi.KittyReportAllKeysAsEscapeCodes |
		ansi.KittyReportAssociatedKeys

	tests := []struct {
		name     string
		key      KeyPressEvent
		flags    int
		expected string
	}{
		{"regular char without flags", KeyPressEvent{Code: 'a'}, 0, ""},
		{"regular char with disambiguate, no mod", KeyPressEvent{Code: 'a'}, disambiguate, ""},
		{"regular char with report-all-keys", KeyPressEvent{Code: 'a'}, ansi.KittyReportAllKeysAsEscapeCodes, "\x1b[97u"},
		{"ctrl+a with disambiguate", KeyPressEvent{Code: 'a', Mod: ModCtrl}, disambiguate, "\x1b[97;5u"},
		{"alt+a with disambiguate", KeyPressEvent{Code: 'a', Mod: ModAlt}, disambiguate, "\x1b[97;3u"},
		{"super+a with disambiguate", KeyPressEvent{Code: 'a', Mod: ModSuper}, disambiguate, "\x1b[97;9u"},
		{"meta+a with disambiguate", KeyPressEvent{Code: 'a', Mod: ModMeta}, disambiguate, "\x1b[97;33u"},
		{"shift+alt+ctrl+a with disambiguate", KeyPressEvent{Code: 'a', Mod: ModShift | ModAlt | ModCtrl}, disambiguate, "\x1b[97;8u"},
		// kitty keeps a bare Enter as CR under disambiguate; only a
		// modifier or report-all-keys makes it CSI 13 u.
		{"enter with disambiguate stays legacy", KeyPressEvent{Code: KeyEnter}, disambiguate, ""},
		{"ctrl+enter with disambiguate", KeyPressEvent{Code: KeyEnter, Mod: ModCtrl}, disambiguate, "\x1b[13;5u"},
		{"escape with disambiguate", KeyPressEvent{Code: KeyEscape}, disambiguate, "\x1b[27u"},
		{"up arrow without modifiers", KeyPressEvent{Code: KeyUp}, disambiguate, "\x1b[A"},
		{"shift+up arrow", KeyPressEvent{Code: KeyUp, Mod: ModShift}, disambiguate, "\x1b[1;2A"},
		{"ctrl+shift+up arrow", KeyPressEvent{Code: KeyUp, Mod: ModCtrl | ModShift}, disambiguate, "\x1b[1;6A"},
		{"F5 without modifiers", KeyPressEvent{Code: KeyF5}, disambiguate, "\x1b[15~"},
		{"ctrl+F5", KeyPressEvent{Code: KeyF5, Mod: ModCtrl}, disambiguate, "\x1b[15;5~"},

		{"plain letter carries its text (flags 31)", KeyPressEvent{Code: 'a', Text: "a"}, all, "\x1b[97;1;97u"},
		{"plain letter carries its text (flags 27)", KeyPressEvent{Code: 'a', Text: "a"}, focus, "\x1b[97;1;97u"},
		// Flags 31 include alternate keys, so the shifted key rides in the key
		// field the way kitty sends it: CSI 97:65;2u for shift+a.
		{"shifted letter reports the shifted key and text", KeyPressEvent{Code: 'x', ShiftedCode: 'X', Text: "X", Mod: ModShift}, all, "\x1b[120:88;2;88u"},
		{"shifted symbol reports the shifted key and text", KeyPressEvent{Code: ';', ShiftedCode: ':', Text: ":", Mod: ModShift}, all, "\x1b[59:58;2;58u"},
		{"shifted letter without alternate keys", KeyPressEvent{Code: 'x', ShiftedCode: 'X', Text: "X", Mod: ModShift}, focus, "\x1b[120;2;88u"},
		{"space reports its text", KeyPressEvent{Code: KeySpace, Text: " "}, all, "\x1b[32;1;32u"},
		{"non-ascii text is reported by code point", KeyPressEvent{Code: 'e', Text: "é"}, all, "\x1b[101;1;233u"},
		{"enter has no associated text", KeyPressEvent{Code: KeyEnter}, all, "\x1b[13u"},
		{"backspace has no associated text", KeyPressEvent{Code: KeyBackspace}, all, "\x1b[127u"},
		{"escape has no associated text", KeyPressEvent{Code: KeyEscape}, all, "\x1b[27u"},
		{"ctrl+letter has no associated text", KeyPressEvent{Code: 'a', Mod: ModCtrl}, all, "\x1b[97;5u"},
		{"up arrow is unchanged under all flags", KeyPressEvent{Code: KeyUp}, all, "\x1b[A"},
		// A control character delivered as Text (never a real keypress, but
		// worth pinning) must not become a text field.
		{"control text is dropped", KeyPressEvent{Code: 'm', Text: "\r"}, all, "\x1b[109u"},
		// disambiguate-only: the pane never asked for associated text, so a
		// plain letter still goes as legacy text (empty CSI-u result).
		{"no associated text without the flag", KeyPressEvent{Code: 'a', Text: "a"}, disambiguate, ""},
	}
	tests = append(tests, layoutKeyCases()...)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EncodeKeyCSIu(tt.key, tt.flags); got != tt.expected {
				t.Errorf("EncodeKeyCSIu(%+v, %d) = %q, want %q", tt.key, tt.flags, got, tt.expected)
			}
		})
	}
}

// layoutKeyCases pin what a pane gets for a key on a non-Latin layout: "ш" is
// the I key on a Ukrainian layout, so tuios holds the base-layout key 'i' (105)
// for it once the host reports alternate keys. Only a pane that asked for
// alternate keys may see that base key.
func layoutKeyCases() []struct {
	name     string
	key      KeyPressEvent
	flags    int
	expected string
} {
	const disambiguate = ansi.KittyDisambiguateEscapeCodes
	const alternate = ansi.KittyDisambiguateEscapeCodes | ansi.KittyReportAlternateKeys
	const allKeys = ansi.KittyDisambiguateEscapeCodes | ansi.KittyReportAllKeysAsEscapeCodes
	sha := KeyPressEvent{Code: 'ш', BaseCode: 'i', Text: "ш"}
	ctrlSha := KeyPressEvent{Code: 'ш', BaseCode: 'i', Mod: ModCtrl}
	shiftSha := KeyPressEvent{Code: 'ш', ShiftedCode: 'Ш', BaseCode: 'i', Text: "Ш", Mod: ModShift}
	return []struct {
		name     string
		key      KeyPressEvent
		flags    int
		expected string
	}{
		// Text is sent as text under disambiguate, ASCII or not.
		{"non-ascii letter is text with disambiguate", sha, disambiguate, ""},
		{"non-ascii letter is text with alternate keys", sha, alternate, ""},
		// A pane that did not ask for alternate keys never sees the base key.
		{"ctrl chord without alternate keys has no base key", ctrlSha, disambiguate, "\x1b[1096;5u"},
		{"plain key in report-all mode has no base key", sha, allKeys, "\x1b[1096u"},
		// A pane that asked gets it, with the shifted key empty when Shift is
		// not held.
		{"ctrl chord with alternate keys carries the base key", ctrlSha, alternate, "\x1b[1096::105;5u"},
		{"plain key with all flags carries the base key", sha, ansi.KittyAllFlags, "\x1b[1096::105;1;1096u"},
		{"shifted key with all flags carries both", shiftSha, ansi.KittyAllFlags, "\x1b[1096:1064:105;2;1064u"},
		// Caps Lock and Num Lock change the text, not the chord, so the key is
		// still text.
		{"numlock letter is text", KeyPressEvent{Code: 'a', Text: "a", Mod: ModNumLock}, disambiguate, ""},
		{"capslock non-ascii letter is text", KeyPressEvent{Code: 'ш', BaseCode: 'i', Text: "Ш", Mod: ModCapsLock}, disambiguate, ""},
		{"capslock shifted letter is text", KeyPressEvent{Code: 'a', Text: "a", Mod: ModCapsLock | ModShift}, disambiguate, ""},
		// Modifier keys go out under their kitty codes.
		{"left shift under all keys", KeyPressEvent{Code: KeyLeftShift, Mod: ModShift}, allKeys, "\x1b[57441;2u"},
		{"right ctrl under all keys", KeyPressEvent{Code: KeyRightCtrl, Mod: ModCtrl}, allKeys, "\x1b[57448;5u"},
		// A base key equal to the code is not repeated.
		{"base equal to code is left out", KeyPressEvent{Code: 'a', BaseCode: 'a', Mod: ModCtrl}, alternate, "\x1b[97;5u"},
	}
}

// TestKittyFunctionalKeysKeepTheirNumbers round-trips every functional key the
// protocol numbers (CapsLock at 57358 through ISO Level 5 Shift at 57454): the
// host's report goes through the same decoder tuios reads it with, and the pane
// must be handed the same number. Before, these left as ultraviolet's private
// codes, so keypad Enter reached the pane as \x1b[1114126u. 57364-57375 are
// skipped: they decode as F1-F12, which the protocol spells in legacy form.
func TestKittyFunctionalKeysKeepTheirNumbers(t *testing.T) {
	var d uv.EventDecoder
	for num := 57358; num <= 57454; num++ {
		if num >= 57364 && num <= 57375 {
			continue
		}
		host := fmt.Sprintf("\x1b[%du", num)
		_, ev := d.Decode([]byte(host))
		key, ok := ev.(KeyPressEvent)
		if !ok {
			t.Fatalf("%q decoded as %T, want a key press", host, ev)
		}
		if got := EncodeKeyCSIu(key, ansi.KittyReportAllKeysAsEscapeCodes); got != host {
			t.Errorf("%s: host sent %q, pane got %q", key.String(), host, got)
		}
		release := fmt.Sprintf("\x1b[%d;1:3u", num)
		if got := EncodeKeyReleaseCSIu(key, ansi.KittyReportAllKeysAsEscapeCodes|ansi.KittyReportEventTypes); got != release {
			t.Errorf("%s release: got %q, want %q", key.String(), got, release)
		}
	}
}

// TestKittyModifierKeysNeedReportAllKeys pins that a modifier pressed alone is
// reported only under report-all-keys, as the protocol says, while keypad keys
// are reported under disambiguate alone.
func TestKittyModifierKeysNeedReportAllKeys(t *testing.T) {
	shift := KeyPressEvent{Code: KeyLeftShift}
	if got := EncodeKeyCSIu(shift, ansi.KittyDisambiguateEscapeCodes); got != "" {
		t.Errorf("left shift under disambiguate: got %q, want nothing", got)
	}
	if got := EncodeKeyReleaseCSIu(shift, ansi.KittyDisambiguateEscapeCodes|ansi.KittyReportEventTypes); got != "" {
		t.Errorf("left shift release without report-all-keys: got %q, want nothing", got)
	}
	if got := EncodeKeyCSIu(KeyPressEvent{Code: KeyKpEnter}, ansi.KittyDisambiguateEscapeCodes); got != "\x1b[57414u" {
		t.Errorf("keypad enter under disambiguate: got %q, want %q", got, "\x1b[57414u")
	}
}

// TestKittyLockKeysNeedReportAllKeys pins that CapsLock, ScrollLock and NumLock
// are modifiers to the protocol, as in kitty's is_modifier_key: a disambiguate
// pane is not told they were pressed. Before, every CapsLock press reached it
// as \x1b[57358u.
func TestKittyLockKeysNeedReportAllKeys(t *testing.T) {
	for _, code := range []rune{KeyCapsLock, KeyScrollLock, KeyNumLock} {
		key := KeyPressEvent{Code: code}
		if got := EncodeKeyCSIu(key, ansi.KittyDisambiguateEscapeCodes); got != "" {
			t.Errorf("%s under disambiguate: got %q, want nothing", key.String(), got)
		}
		if got := EncodeKeyReleaseCSIu(key, ansi.KittyDisambiguateEscapeCodes|ansi.KittyReportEventTypes); got != "" {
			t.Errorf("%s release without report-all-keys: got %q, want nothing", key.String(), got)
		}
	}
}

// TestKittyBeginKeepsItsLegacyForm round-trips Begin, the key the host sends as
// \x1b[E (keypad 5 with NumLock off on most layouts). The protocol spells
// KP_BEGIN as "1 E", like the arrows; before, it fell through to ultraviolet's
// private code and left as \x1b[1114117u.
func TestKittyBeginKeepsItsLegacyForm(t *testing.T) {
	var d uv.EventDecoder
	_, ev := d.Decode([]byte("\x1b[E"))
	key, ok := ev.(KeyPressEvent)
	if !ok || key.Code != KeyBegin {
		t.Fatalf("\\x1b[E decoded as %#v, want Begin", ev)
	}
	const disambiguate = ansi.KittyDisambiguateEscapeCodes
	if got := EncodeKeyCSIu(key, disambiguate); got != "\x1b[E" {
		t.Errorf("begin: got %q, want %q", got, "\x1b[E")
	}
	key.Mod = ModCtrl
	if got := EncodeKeyCSIu(key, disambiguate); got != "\x1b[1;5E" {
		t.Errorf("ctrl+begin: got %q, want %q", got, "\x1b[1;5E")
	}
	if got := EncodeKeyReleaseCSIu(key, disambiguate|ansi.KittyReportEventTypes); got != "\x1b[1;5:3E" {
		t.Errorf("ctrl+begin release: got %q, want %q", got, "\x1b[1;5:3E")
	}
}

// TestKittyUnnumberedPrivateKeysFallBack pins that a key the protocol has no
// number for is left to the legacy encoder instead of going out as
// ultraviolet's private code. F36 and up are past the protocol's F35; the
// keypad comma borrows KP_SEPARATOR's number.
func TestKittyUnnumberedPrivateKeysFallBack(t *testing.T) {
	const all = ansi.KittyReportAllKeysAsEscapeCodes | ansi.KittyReportEventTypes
	for code := KeyF36; code <= KeyF63; code++ {
		key := KeyPressEvent{Code: code, Mod: ModCtrl}
		if got := EncodeKeyCSIu(key, all); got != "" {
			t.Errorf("%s: got %q, want the legacy fallback", key.String(), got)
		}
		if got := EncodeKeyReleaseCSIu(key, all); got != "" {
			t.Errorf("%s release: got %q, want nothing", key.String(), got)
		}
	}
	comma := KeyPressEvent{Code: KeyKpComma, Mod: ModCtrl}
	if got := EncodeKeyCSIu(comma, ansi.KittyDisambiguateEscapeCodes); got != "\x1b[57416;5u" {
		t.Errorf("ctrl+keypad comma: got %q, want %q", got, "\x1b[57416;5u")
	}
}

// TestKittyTextUnderDisambiguate pins that a key producing text is sent as that
// text under disambiguate, which reports only non-text keys, and text keys with
// Ctrl, Alt or Super, with CSI u. The events come from the decoder, so they
// carry what a real host report does: kitty sends keypad 1 with NumLock on as
// \x1b[57400;129u, and the NumLock bit must not count as a modifier.
func TestKittyTextUnderDisambiguate(t *testing.T) {
	const disambiguate = ansi.KittyDisambiguateEscapeCodes
	const allKeys = ansi.KittyReportAllKeysAsEscapeCodes
	tests := []struct {
		name  string
		host  string
		flags int
		want  string
	}{
		{"keypad 1, numlock on", "\x1b[57400;129u", disambiguate, ""},
		{"keypad 1, no lock", "\x1b[57400u", disambiguate, ""},
		{"shift+keypad plus", "\x1b[57413;2u", disambiguate, ""},
		{"ctrl+keypad 1 is still CSI u", "\x1b[57400;133u", disambiguate, "\x1b[57400;5u"},
		{"keypad enter has no text", "\x1b[57414;129u", disambiguate, "\x1b[57414u"},
		// Under report-all-keys the lock bits go through, as kitty sends them:
		// a pane that treats the lock keys as keys keeps its own lock state.
		{"keypad 1 under report-all-keys", "\x1b[57400;129u", allKeys, "\x1b[57400;129u"},
		// The same rule for every text key, not just the keypad: the lock
		// bits come along from a report-all-keys host and must not count.
		{"a, numlock on", "\x1b[97;129u", disambiguate, ""},
		{"a, capslock on", "\x1b[97;65u", disambiguate, ""},
		{"ctrl+a, numlock on is still CSI u", "\x1b[97;133u", disambiguate, "\x1b[97;5u"},
		{"non-ascii text", "\x1b[233u", disambiguate, ""},
		{"alt+non-ascii is still CSI u", "\x1b[233;3u", disambiguate, "\x1b[233;3u"},
		// Enter, Tab and Backspace stay legacy bytes under disambiguate, as
		// kitty sends them; the caller sends CR.
		{"enter, numlock on, stays legacy", "\x1b[13;129u", disambiguate, ""},
		// The lock bits do not reach a pane without report-all-keys.
		{"up, numlock on, has no lock bits", "\x1b[1;129A", disambiguate, "\x1b[A"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var d uv.EventDecoder
			_, ev := d.Decode([]byte(tt.host))
			key, ok := ev.(KeyPressEvent)
			if !ok {
				t.Fatalf("%q decoded as %T, want a key press", tt.host, ev)
			}
			if got := EncodeKeyCSIu(key, tt.flags); got != tt.want {
				t.Errorf("host %q (text %q, mod %v): got %q, want %q", tt.host, key.Text, key.Mod, got, tt.want)
			}
		})
	}
}

// TestConPTYPaneIgnoresKittyKeyboard pins the behaviour of a pane whose PTY
// is a Windows ConPTY: the emulator never offers the kitty keyboard protocol,
// so pushes, pops and sets leave the flags at 0 and a CSI ? u query goes
// unanswered — per spec, silence means "not supported" and the guest falls
// back to legacy keys on its own. conhost drops input CSIs it does not
// recognise (before conhost 1.22), so any CSI u bytes a guest sent anyway
// would never reach it.
func TestConPTYPaneIgnoresKittyKeyboard(t *testing.T) {
	e := NewEmulator(80, 24)
	defer e.Close()
	e.DisableKittyKeyboardProtocol()

	_, _ = e.Write([]byte("\x1b[>1u\x1b[=7;1u\x1b[<1u"))
	if got := e.KittyKeyboardFlags(); got != 0 {
		t.Errorf("flags = %d, want 0 after push/set/pop on a ConPTY pane", got)
	}

	responseChan := make(chan string, 1)
	go func() {
		buf := make([]byte, 256)
		n, err := e.Read(buf)
		if err != nil && err != io.EOF {
			responseChan <- "read error: " + err.Error()
			return
		}
		responseChan <- string(buf[:n])
	}()
	_, _ = e.Write([]byte("\x1b[?u"))

	select {
	case response := <-responseChan:
		t.Errorf("a ConPTY pane answered the query with %q, want silence", response)
	case <-time.After(500 * time.Millisecond):
		// Silence is the expected outcome: no reply means "not supported".
	}
}

// TestConPTYPaneRestoreKeepsStackEmpty pins that a saved kitty stack is not
// restored onto a ConPTY pane: the guest could never have negotiated it.
func TestConPTYPaneRestoreKeepsStackEmpty(t *testing.T) {
	e := NewEmulator(80, 24)
	defer e.Close()
	e.DisableKittyKeyboardProtocol()

	e.RestoreKittyKeyboardState([]int{1, 7})
	if got := e.KittyKeyboardFlags(); got != 0 {
		t.Errorf("flags = %d, want 0 after restoring a stack onto a ConPTY pane", got)
	}
}
