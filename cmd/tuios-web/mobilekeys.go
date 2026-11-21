package main

import (
	"fmt"
	"strings"

	"github.com/Gaurav-Gosain/sip"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/tape"
)

var mobileCommands = config.MobileCommands()

func mobileBar(reg *config.KeybindRegistry, leader string) (sip.MobilePrefix, []sip.MobileRow) {
	prefix, ok := leaderPrefix(leader)
	rows := make([]sip.MobileRow, 0, 2)
	if ok {
		if keys := commandKeys(reg, leader); len(keys) > 0 {
			rows = append(rows, sip.MobileRow{
				Label:       "TUIOS commands",
				Keys:        keys,
				Collapsible: true,
			})
		}
	}
	return prefix, append(rows, sip.MobileRow{Label: "Keys", Keys: sip.DefaultMobileKeys()})
}

func commandKeys(reg *config.KeybindRegistry, leader string) []sip.MobileKey {
	keys := []sip.MobileKey{{
		Label:  "pfx",
		Title:  fmt.Sprintf("Prefix (%s), then the command key", leader),
		Prefix: true,
	}}
	for _, cmd := range mobileCommands {
		if key, ok := commandKey(reg, cmd.Label, cmd.Action, leader); ok {
			keys = append(keys, key)
		}
	}
	if len(keys) == 1 {
		return nil
	}
	return keys
}

func commandKey(reg *config.KeybindRegistry, label, action, leader string) (sip.MobileKey, bool) {
	for _, bound := range reg.Keys(config.TablePrefixMode, action) {
		spec, ok := resolveKey(bound)
		if !ok {
			continue
		}
		title := config.ActionDescriptions[action]
		if title == "" {
			title = label
		}
		return sip.MobileKey{
			Label:    label,
			Title:    fmt.Sprintf("%s (%s %s)", title, leader, bound),
			Key:      spec.key,
			Code:     spec.code,
			Shift:    spec.shift,
			Ctrl:     spec.ctrl,
			Alt:      spec.alt,
			Prefixed: true,
		}, true
	}
	return sip.MobileKey{}, false
}

func leaderPrefix(leader string) (sip.MobilePrefix, bool) {
	spec, ok := resolveKey(leader)
	if !ok {
		return sip.MobilePrefix{}, false
	}
	return sip.MobilePrefix{
		Key:   spec.key,
		Code:  spec.code,
		Ctrl:  spec.ctrl,
		Alt:   spec.alt,
		Shift: spec.shift,
	}, true
}

type keySpec struct {
	key              string
	code             string
	ctrl, alt, shift bool
}

func resolveKey(combo string) (keySpec, bool) {
	parsed, err := tape.ParseKeyCombo(config.CanonicalKey(combo))
	if err != nil {
		return keySpec{}, false
	}
	spec, ok := browserKey(parsed.Key)
	if !ok {
		return keySpec{}, false
	}
	spec.ctrl = parsed.Ctrl
	spec.alt = parsed.Alt
	spec.shift = spec.shift || parsed.Shift
	return spec, true
}

var browserKeyNames = map[string]keySpec{
	"esc":       {key: "Escape", code: "Escape"},
	"escape":    {key: "Escape", code: "Escape"},
	"tab":       {key: "Tab", code: "Tab"},
	"enter":     {key: "Enter", code: "Enter"},
	"return":    {key: "Enter", code: "Enter"},
	"space":     {key: " ", code: "Space"},
	"backspace": {key: "Backspace", code: "Backspace"},
	"delete":    {key: "Delete", code: "Delete"},
	"insert":    {key: "Insert", code: "Insert"},
	"up":        {key: "ArrowUp", code: "ArrowUp"},
	"down":      {key: "ArrowDown", code: "ArrowDown"},
	"left":      {key: "ArrowLeft", code: "ArrowLeft"},
	"right":     {key: "ArrowRight", code: "ArrowRight"},
	"home":      {key: "Home", code: "Home"},
	"end":       {key: "End", code: "End"},
	"pgup":      {key: "PageUp", code: "PageUp"},
	"pageup":    {key: "PageUp", code: "PageUp"},
	"pgdown":    {key: "PageDown", code: "PageDown"},
	"pagedown":  {key: "PageDown", code: "PageDown"},
}

func browserKey(key string) (keySpec, bool) {
	if key == "" {
		return keySpec{}, false
	}
	if named, ok := browserKeyNames[strings.ToLower(key)]; ok {
		return named, true
	}
	runes := []rune(key)
	if len(runes) != 1 {
		return keySpec{}, false
	}
	code, shift := charCode(runes[0])
	return keySpec{key: key, code: code, shift: shift}, true
}

var shiftedPunctuation = map[rune]keySpec{
	'`': {code: "Backquote"}, '~': {code: "Backquote", shift: true},
	'-': {code: "Minus"}, '_': {code: "Minus", shift: true},
	'=': {code: "Equal"}, '+': {code: "Equal", shift: true},
	'[': {code: "BracketLeft"}, '{': {code: "BracketLeft", shift: true},
	']': {code: "BracketRight"}, '}': {code: "BracketRight", shift: true},
	'\\': {code: "Backslash"}, '|': {code: "Backslash", shift: true},
	';': {code: "Semicolon"}, ':': {code: "Semicolon", shift: true},
	'\'': {code: "Quote"}, '"': {code: "Quote", shift: true},
	',': {code: "Comma"}, '<': {code: "Comma", shift: true},
	'.': {code: "Period"}, '>': {code: "Period", shift: true},
	'/': {code: "Slash"}, '?': {code: "Slash", shift: true},
	'!': {code: "Digit1", shift: true}, '@': {code: "Digit2", shift: true},
	'#': {code: "Digit3", shift: true}, '$': {code: "Digit4", shift: true},
	'%': {code: "Digit5", shift: true}, '^': {code: "Digit6", shift: true},
	'&': {code: "Digit7", shift: true}, '*': {code: "Digit8", shift: true},
	'(': {code: "Digit9", shift: true}, ')': {code: "Digit0", shift: true},
	' ': {code: "Space"},
}

func charCode(r rune) (string, bool) {
	switch {
	case r >= 'a' && r <= 'z':
		return "Key" + strings.ToUpper(string(r)), false
	case r >= 'A' && r <= 'Z':
		return "Key" + string(r), true
	case r >= '0' && r <= '9':
		return "Digit" + string(r), false
	}
	if p, ok := shiftedPunctuation[r]; ok {
		return p.code, p.shift
	}
	return "", false
}
