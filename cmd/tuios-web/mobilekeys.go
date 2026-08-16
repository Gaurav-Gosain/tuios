package main

import (
	"fmt"
	"strings"

	"github.com/Gaurav-Gosain/sip"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/tape"
)

// mobileCommands are the commands the chord row offers, left to right.
//
// Each is named by its registry action rather than by its key, so a user who
// rebinds one gets a button that sends what they bound. The set is the work a
// phone actually does: make a window, get rid of it, move between them, change
// how they are laid out, and reach the two overlays that hold everything else.
//
// The order is the design. Eleven buttons and the latch are 620px of strip
// against the 331px a 390px phone has, so the row pans and the first seven are
// what a thumb sees on arrival. They are the seven a thumb wants: make and
// close a pane, move between them, zoom, which is the only way a second pane is
// usable on a screen this narrow, and the command palette, which is the way to
// everything that did not fit. The layout commands and the two overlays are
// behind a pan, which is the right place for what a phone reaches for once a
// session rather than once a minute.
var mobileCommands = []struct{ label, action string }{
	{"new", "prefix_new_window"},
	{"close", "prefix_close_window"},
	{"next", "prefix_next_window"},
	{"prev", "prefix_prev_window"},
	{"zoom", "prefix_fullscreen"},
	{"cmds", "prefix_command_palette"},
	{"tile", "prefix_toggle_tiling"},
	{"vsplit", "prefix_split_vertical"},
	{"hsplit", "prefix_split_horizontal"},
	{"config", "prefix_settings"},
	{"help", "prefix_help"},
}

// mobileBar builds the touch key bar: the leader the chords hang off, and the
// rows drawn top to bottom.
//
// The chord row comes first because it is what makes TUIOS usable without a
// keyboard, and it folds away for a user who is only typing. sip's own typing
// row goes last, nearest the thumb already resting on the software keyboard.
//
// Every chord is read out of the keybind registry. A button whose key the
// browser cannot be asked to send is left out, and with an unsendable leader
// the whole chord row goes, since a prefixed button with no prefix sends its
// key bare into whatever pane is focused.
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

// commandKeys turns mobileCommands into buttons, behind the prefix latch.
//
// The latch leads so a chord with no button of its own can still be finished
// on the software keyboard, which is the only way to reach the commands that
// did not fit here.
func commandKeys(reg *config.KeybindRegistry, leader string) []sip.MobileKey {
	keys := []sip.MobileKey{{
		Label:  "pfx",
		Title:  fmt.Sprintf("Prefix (%s), then the command key", leader),
		Prefix: true,
	}}
	for _, cmd := range mobileCommands {
		if key, ok := commandKey(reg, cmd.label, cmd.action, leader); ok {
			keys = append(keys, key)
		}
	}
	if len(keys) == 1 {
		return nil
	}
	return keys
}

// commandKey resolves one action to its button.
//
// An action can be bound to several keys and only the first the browser can
// send is wanted: prefix_next_window is n and Tab, and Tab after the leader is
// the same command through a key the bar already has.
//
// The binding's own Ctrl and Alt ride along, because sip v0.7.0 sends a
// prefixed key with the modifiers the button declares. A command bound to
// ctrl+p behind the leader used to arrive as a bare p, which is why every such
// binding was dropped here instead.
func commandKey(reg *config.KeybindRegistry, label, action, leader string) (sip.MobileKey, bool) {
	for _, bound := range reg.GetKeys(action) {
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

// leaderPrefix turns the configured leader into sip's leader chord.
//
// The leader is rebindable, so it is read rather than assumed: a bar with
// ctrl+b baked into it would lie to everyone who changed it. A leader the
// browser cannot be asked to send yields no prefix at all, since a chord that
// sends the wrong thing is worse than one that is missing.
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

// keySpec is one TUIOS key binding said the way a browser says it.
type keySpec struct {
	key              string
	code             string
	ctrl, alt, shift bool
}

// resolveKey translates a TUIOS key combo ("ctrl+b", "shift+tab", "|") into
// the KeyboardEvent fields sip's client encodes from. It reports false for
// anything sip has no encoding for, so the caller drops the button rather than
// shipping one that sends nothing.
func resolveKey(combo string) (keySpec, bool) {
	parsed, err := tape.ParseKeyCombo(combo)
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

// browserKeyNames maps the key names TUIOS config uses onto the
// KeyboardEvent name and code sip's client sees. Only the names sip can turn
// into bytes are here; anything else reports false.
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

// browserKey resolves one TUIOS key name to what the browser calls it.
func browserKey(key string) (keySpec, bool) {
	if key == "" {
		return keySpec{}, false
	}
	if named, ok := browserKeyNames[strings.ToLower(key)]; ok {
		return named, true
	}
	// A single character is itself. Longer names that got this far are keys
	// sip has no encoding for, the function keys among them.
	runes := []rune(key)
	if len(runes) != 1 {
		return keySpec{}, false
	}
	code, shift := charCode(runes[0])
	return keySpec{key: key, code: code, shift: shift}, true
}

// shiftedPunctuation is the US keyboard's punctuation, both halves of each
// key. It answers which physical key a character comes from and whether Shift
// is held to get it.
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

// charCode names the physical key a printable character comes from, and says
// whether Shift is held to produce it.
//
// US layout, as sip's own key table is. sip's built-in encoder sends the
// character and ignores both answers, so a user on another layout loses
// nothing; they are carried for a page that swaps in a keymap encoder, which
// works from the code rather than from the character.
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
