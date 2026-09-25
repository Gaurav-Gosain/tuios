package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	uv "github.com/charmbracelet/ultraviolet"
)

// decodeKey decodes a single key event from raw terminal bytes exactly as
// bubbletea does, so these cases exercise the real decoder rather than a
// hand-built KeyPressMsg.
func decodeKey(t *testing.T, raw []byte) tea.KeyPressMsg {
	t.Helper()
	var dec uv.EventDecoder
	_, ev := dec.Decode(raw)
	kp, ok := ev.(uv.KeyPressEvent)
	if !ok {
		t.Fatalf("decode %q: got %T, want KeyPressEvent", raw, ev)
	}
	return tea.KeyPressMsg(kp)
}

// TestGlobalBindsAcrossEncodings pins that a registered global binding is
// recognised no matter how the terminal encoded the key, and that near misses
// are not. The lookup path is bindingKeys (String, then Keystroke) against
// lookupKey (which lowercases compound keys), and between them they cover
// every spelling below. The stringified key differs across the Ctrl+P
// encodings: associated-text reporting gives "p" and alternate-key reporting
// gives "ctrl+P", which the old msg.String() == "ctrl+p" comparison missed.
func TestGlobalBindsAcrossEncodings(t *testing.T) {
	const palette, launcher = "command_palette", "launcher"
	cfg := config.DefaultConfig()
	cfg.Keybindings.Global = map[string][]string{palette: {"ctrl+p"}, launcher: {"alt+space"}}
	o := &app.OS{Settings: config.Global, KeybindRegistry: config.NewKeybindRegistry(cfg)}

	for _, tc := range []struct {
		name string
		raw  []byte
		want string
	}{
		{"ctrl+p legacy control byte", []byte{0x10}, palette},
		{"ctrl+p kitty disambiguate", []byte("\x1b[112;5u"), palette},
		{"ctrl+p kitty associated text", []byte("\x1b[112;5;112u"), palette},
		{"ctrl+p kitty alternate/base keys", []byte("\x1b[112::80;5u"), palette},
		{"ctrl+p modifyOtherKeys level 2", []byte("\x1b[27;5;112~"), palette},
		// Lock modifiers ride along in the Kitty modifier field and stay on the
		// decoded event. Num Lock (mod 133) is the boot default on most desktop
		// keyboards, so this is the owner's real-world failure: an exact
		// Mod == ModCtrl check missed it and the palette never opened.
		{"ctrl+p kitty capslock", []byte("\x1b[112;69u"), palette},
		{"ctrl+p kitty numlock", []byte("\x1b[112;133u"), palette},

		{"alt+space legacy esc-prefixed", []byte("\x1b "), launcher},
		{"alt+space kitty disambiguate", []byte("\x1b[32;3u"), launcher},
		{"alt+space kitty associated text", []byte("\x1b[32;3;32u"), launcher},
		{"alt+space kitty numlock", []byte("\x1b[32;131u"), launcher},

		// Ordinary typing into the shell must not be swallowed.
		{"bare p", []byte("p"), ""},
		{"alt+p", []byte("\x1bp"), ""},
		{"bare P", []byte("P"), ""},
		{"ctrl+o", []byte{0x0f}, ""},
		{"kitty ctrl+shift+p", []byte("\x1b[112;6u"), ""},
		{"kitty ctrl+alt+p", []byte("\x1b[112;7u"), ""},
	} {
		msg := decodeKey(t, tc.raw)
		if got := sectionAction(msg, o, (*config.KeybindRegistry).GetGlobalAction); got != tc.want {
			t.Errorf("%s: action = %q (String()=%q, Keystroke()=%q, Mod=%v), want %q",
				tc.name, got, msg.String(), msg.Keystroke(), msg.Mod, tc.want)
		}
	}
}
