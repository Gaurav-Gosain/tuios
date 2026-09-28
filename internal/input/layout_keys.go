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
	if !usesBaseLayout(msg) {
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

// usesBaseLayout reports whether a binding may match msg through its
// base-layout key. It needs a base-layout key that differs from the key
// produced, and a produced key outside ASCII and outside the Latin script.
//
// A Latin layout keeps its own keys, with or without Ctrl. On AZERTY the key
// that types "a" sits where US has q, German ß sits on "-", French é on "2",
// and Dvorak's Ctrl+X is where US has Ctrl+B. Reading those by position ran
// quit, split a pane, jumped to window 2, and took an editor's C-x for the
// leader. The labels on a Latin keyboard are what its user binds, so only a
// script no binding is spelled in (Cyrillic, Greek, Hebrew, Arabic and the
// rest) goes by position.
func usesBaseLayout(msg tea.KeyPressMsg) bool {
	if msg.BaseCode == 0 || msg.BaseCode == msg.Code {
		return false
	}
	return msg.Code >= 0x80 && unicode.IsPrint(msg.Code) && !unicode.Is(unicode.Latin, msg.Code)
}

// producedKey is msg without its base-layout key, so String() and Keystroke()
// spell the key it produced rather than the US key at its position.
func producedKey(msg tea.KeyPressMsg) tea.KeyPressMsg {
	msg.BaseCode = 0
	return msg
}

// readKey is msg as tuios reads it: spelled by the key the layout produced.
// Bubble Tea spells a chord from its base-layout key when the terminal sent
// one, so on any Latin layout other than US a Ctrl or Alt chord read as the
// US key at its position wherever msg.String() was compared. Taking that key
// off here, once, makes every reader agree with the bindings. It stays where
// usesBaseLayout lets it stand in for a non-Latin key, and on a key reported
// as its own base. The pane still gets the key the host sent; see paneMsg.
func readKey(msg tea.KeyPressMsg) tea.KeyPressMsg {
	if msg.BaseCode == 0 || msg.BaseCode == msg.Code || usesBaseLayout(msg) {
		return msg
	}
	return producedKey(msg)
}

// isLayoutTextWithoutBase reports whether msg is a non-ASCII character typed
// with no modifier but Shift, and carries no base-layout key.
func isLayoutTextWithoutBase(msg tea.KeyPressMsg) bool {
	if msg.BaseCode != 0 || msg.Mod&^(lockMods|tea.ModShift) != 0 {
		return false
	}
	r := chordRune(msg)
	return r >= 0x80 && unicode.IsPrint(r) && !unicode.Is(unicode.Latin, r)
}

// commandKey is the key a panel with fixed ASCII keys (the quit menu, copy
// mode, the help panel) should switch on. It is msg.String(), unless that is
// not ASCII and the terminal reported a base-layout key: no fixed key can match
// a non-ASCII character, so the base-layout key is the only one that can mean
// anything. Do not use it where the key is typed as text.
func commandKey(msg tea.KeyPressMsg) string {
	key := producedKey(msg).String()
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
