package input

import (
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
)

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

func usesBaseLayout(msg tea.KeyPressMsg) bool {
	if msg.BaseCode == 0 || msg.BaseCode == msg.Code {
		return false
	}
	if msg.Mod&(tea.ModCtrl|tea.ModAlt) == tea.ModCtrl|tea.ModAlt && msg.Text != "" {
		return false
	}
	return msg.Code >= 0x80 && unicode.IsPrint(msg.Code) && !unicode.Is(unicode.Latin, msg.Code)
}

func producedKey(msg tea.KeyPressMsg) tea.KeyPressMsg {
	msg.BaseCode = 0
	return msg
}

func withoutChordText(msg tea.KeyPressMsg) tea.KeyPressMsg {
	if msg.Text == "" {
		return msg
	}
	mods := msg.Mod &^ (lockMods | tea.ModShift)
	if mods == 0 || mods&(tea.ModCtrl|tea.ModAlt) == tea.ModCtrl|tea.ModAlt {
		return msg
	}
	msg.Text = ""
	return msg
}

func readKey(msg tea.KeyPressMsg) tea.KeyPressMsg {
	if msg.BaseCode == 0 || msg.BaseCode == msg.Code || usesBaseLayout(msg) {
		return msg
	}
	return producedKey(msg)
}

func isLayoutTextWithoutBase(msg tea.KeyPressMsg) bool {
	if msg.BaseCode != 0 || msg.Mod&^(lockMods|tea.ModShift) != 0 {
		return false
	}
	r := chordRune(msg)
	return r >= 0x80 && unicode.IsPrint(r) && !unicode.Is(unicode.Latin, r)
}

func commandKey(msg tea.KeyPressMsg) string {
	msg = withoutChordText(msg)
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

func isModifierKeyPress(msg tea.KeyPressMsg) bool {
	switch {
	case msg.Code >= tea.KeyLeftShift && msg.Code <= tea.KeyIsoLevel5Shift:
		return true
	case msg.Code == tea.KeyCapsLock, msg.Code == tea.KeyScrollLock, msg.Code == tea.KeyNumLock:
		return true
	}
	return false
}

func optionTypedKey(msg tea.KeyPressMsg, base rune) (string, bool) {
	mods := msg.Mod &^ lockMods
	if mods&tea.ModAlt == 0 || mods&^(tea.ModAlt|tea.ModShift) != 0 {
		return "", false
	}
	text := []rune(msg.Text)
	if len(text) != 1 || text[0] <= ' ' || text[0] > '~' {
		return "", false
	}
	c := text[0]
	for _, own := range []rune{msg.Code, msg.ShiftedCode, base} {
		if own == 0 {
			continue
		}
		if c == own || c == unicode.ToUpper(own) || (mods&tea.ModShift != 0 && c == usShifted(own)) {
			return "", false
		}
	}
	return string(c), true
}

func usShifted(r rune) rune {
	const plain, shifted = "1234567890-=[]\\;',./`", "!@#$%^&*()_+{}|:\"<>?~"
	if i := strings.IndexRune(plain, r); i >= 0 {
		return rune(shifted[i])
	}
	return 0
}
