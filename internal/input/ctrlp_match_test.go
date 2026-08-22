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

// osWithGlobalBinds returns an OS whose registry has the given global section.
func osWithGlobalBinds(t *testing.T, binds map[string][]string) *app.OS {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Keybindings.Global = binds
	return &app.OS{KeybindRegistry: config.NewKeybindRegistry(cfg)}
}

// ctrlPEncodings is every way a terminal might send Ctrl+P. The stringified key
// differs across them: associated-text reporting gives "p" and alternate-key
// reporting gives "ctrl+P", which the old msg.String() == "ctrl+p" comparison
// missed.
var ctrlPEncodings = map[string][]byte{
	"legacy control byte":       {0x10},
	"kitty disambiguate":        []byte("\x1b[112;5u"),
	"kitty associated text":     []byte("\x1b[112;5;112u"),
	"kitty alternate/base keys": []byte("\x1b[112::80;5u"),
	"modifyOtherKeys level 2":   []byte("\x1b[27;5;112~"),
	// Lock modifiers ride along in the Kitty modifier field and stay on the
	// decoded event. Num Lock (mod 133) is the boot default on most desktop
	// keyboards, so this case is the owner's real-world failure: an exact
	// Mod == ModCtrl check missed it and the palette never opened.
	"kitty ctrl+capslock": []byte("\x1b[112;69u"),
	"kitty ctrl+numlock":  []byte("\x1b[112;133u"),
}

// TestGlobalBindsAcrossEncodings pins that a registered binding is recognised no
// matter how the terminal encoded the key. This is the property the hand-rolled
// isCtrlP matcher existed for, and moving the binding into the registry must not
// cost it: the lookup path is bindingKeys (String, then Keystroke) against
// lookupKey (which lowercases compound keys), and between them they cover every
// spelling below.
func TestGlobalBindsAcrossEncodings(t *testing.T) {
	o := osWithGlobalBinds(t, map[string][]string{"command_palette": {"ctrl+p"}})
	for name, raw := range ctrlPEncodings {
		msg := decodeKey(t, raw)
		got := sectionAction(msg, o, (*config.KeybindRegistry).GetGlobalAction)
		if got != "command_palette" {
			t.Errorf("%s: action = %q (String()=%q, Keystroke()=%q, Mod=%v), want command_palette",
				name, got, msg.String(), msg.Keystroke(), msg.Mod)
		}
	}
}

// TestGlobalBindsRejectBareP guards that a plain 'p' never resolves to the
// palette, so ordinary typing into the shell is not swallowed.
func TestGlobalBindsRejectBareP(t *testing.T) {
	o := osWithGlobalBinds(t, map[string][]string{"command_palette": {"ctrl+p"}})
	for name, raw := range map[string][]byte{
		"bare p":             []byte("p"),
		"alt+p":              []byte("\x1bp"),
		"bare P":             []byte("P"),
		"ctrl+o":             {0x0f},
		"kitty ctrl+shift+p": []byte("\x1b[112;6u"),
		"kitty ctrl+alt+p":   []byte("\x1b[112;7u"),
	} {
		msg := decodeKey(t, raw)
		if got := sectionAction(msg, o, (*config.KeybindRegistry).GetGlobalAction); got != "" {
			t.Errorf("%s: action = %q (String()=%q), want none", name, got, msg.String())
		}
	}
}

// TestLauncherKeyAcrossEncodings does for alt+space what the Ctrl+P cases do for
// the palette.
func TestLauncherKeyAcrossEncodings(t *testing.T) {
	o := osWithGlobalBinds(t, map[string][]string{"launcher": {"alt+space"}})
	for name, raw := range map[string][]byte{
		"legacy esc-prefixed":     []byte("\x1b "),
		"kitty disambiguate":      []byte("\x1b[32;3u"),
		"kitty associated text":   []byte("\x1b[32;3;32u"),
		"kitty alt+space+numlock": []byte("\x1b[32;131u"),
	} {
		msg := decodeKey(t, raw)
		if got := sectionAction(msg, o, (*config.KeybindRegistry).GetGlobalAction); got != "launcher" {
			t.Errorf("%s: action = %q (String()=%q), want launcher", name, got, msg.String())
		}
	}
}

// TestGlobalBindRebound is the whole point of the move: the key the palette
// answers to is whatever the config says, and the default stops working once it
// is replaced.
func TestGlobalBindRebound(t *testing.T) {
	o := osWithGlobalBinds(t, map[string][]string{"command_palette": {"ctrl+g"}})
	for name, raw := range ctrlPEncodings {
		msg := decodeKey(t, raw)
		if got := sectionAction(msg, o, (*config.KeybindRegistry).GetGlobalAction); got != "" {
			t.Errorf("%s: ctrl+p still resolves to %q after rebinding to ctrl+g", name, got)
		}
	}
	ctrlG := decodeKey(t, []byte{0x07})
	if got := sectionAction(ctrlG, o, (*config.KeybindRegistry).GetGlobalAction); got != "command_palette" {
		t.Errorf("ctrl+g resolves to %q, want command_palette", got)
	}
}

// TestGlobalBindUnbound pins that an action set to [] is off. "Hackable"
// includes turning something off, and a user who wants fish's history-back has
// no other way to get it.
func TestGlobalBindUnbound(t *testing.T) {
	o := osWithGlobalBinds(t, map[string][]string{"command_palette": {}})
	for name, raw := range ctrlPEncodings {
		msg := decodeKey(t, raw)
		if got := sectionAction(msg, o, (*config.KeybindRegistry).GetGlobalAction); got != "" {
			t.Errorf("%s: unbound command_palette still resolves to %q", name, got)
		}
	}
}
