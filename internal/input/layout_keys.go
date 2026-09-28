package input

import (
	"unicode"

	tea "charm.land/bubbletea/v2"
)

// Layout independence.
//
// A key reaches tuios as the character the active layout produced, so with a
// Ukrainian layout the I key arrives as "ш" and nothing is bound to that. The
// Kitty keyboard protocol's alternate-key reporting also names the key at the
// same position on a US layout (the base-layout key), which Bubble Tea puts in
// Key.BaseCode. The helpers here fall back to that key when the produced one
// means nothing, so every binding answers to the physical key under any layout.
//
// The produced key always wins when it matches: a user who bound "ш" gets that
// binding. And nothing here applies to text being typed (a rename, a search, a
// palette query, a pane): those take msg.Text, which is still the character the
// user typed.

// baseLayoutKey returns the binding spelling of the US-layout key at the
// position of msg, and whether msg carries one that differs from the key it
// produced. A shifted letter is spelled as the capital letter, the way
// bindings write it; any other chord is spelled with its modifiers.
func baseLayoutKey(msg tea.KeyPressMsg) (string, bool) {
	if msg.BaseCode == 0 || msg.BaseCode == msg.Code {
		return "", false
	}
	mods := msg.Mod &^ lockMods
	if mods == tea.ModShift && msg.BaseCode >= 'a' && msg.BaseCode <= 'z' {
		return string(unicode.ToUpper(msg.BaseCode)), true
	}
	stroke := msg.Keystroke()
	if stroke == "" {
		return "", false
	}
	return stroke, true
}

// commandKey is the key a panel with fixed ASCII keys (the quit menu, copy
// mode, the help panel) should switch on. It is msg.String(), unless that is
// not ASCII and the terminal reported a base-layout key: no fixed key can match
// a non-ASCII character, so the base-layout key is the only one that can mean
// anything. Do not use it where the key is typed as text.
func commandKey(msg tea.KeyPressMsg) string {
	key := msg.String()
	if isASCII(key) {
		return key
	}
	if base, ok := baseLayoutKey(msg); ok {
		return base
	}
	return key
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// isModifierKeyPress reports whether msg is a modifier or lock key pressed on
// its own. A terminal only sends these in report-all-keys mode, which tuios
// asks for while it reads keys itself. Such a press is not a command, so it
// must not end a pending prefix or reach a binding.
func isModifierKeyPress(msg tea.KeyPressMsg) bool {
	switch {
	case msg.Code >= tea.KeyLeftShift && msg.Code <= tea.KeyIsoLevel5Shift:
		return true
	case msg.Code == tea.KeyCapsLock, msg.Code == tea.KeyScrollLock, msg.Code == tea.KeyNumLock:
		return true
	}
	return false
}
