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
		{"enter with disambiguate", KeyPressEvent{Code: KeyEnter}, disambiguate, "\x1b[13u"},
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
