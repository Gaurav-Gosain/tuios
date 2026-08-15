package main

import (
	"fmt"
	"strings"

	"github.com/Gaurav-Gosain/sip"
	"github.com/Gaurav-Gosain/tuios/internal/tape"
)

// mobileKeys is the touch key bar sip draws on a phone.
//
// The bulk of it is the keys a phone keyboard does not have: escape, tab,
// sticky ctrl and alt, the arrows, and the punctuation a shell needs. Those
// are as useful under TUIOS as under anything else, so sip's own set is kept
// and the leader goes in front of it, where a narrow screen shows it without
// scrolling.
//
// The leader is as far as this can go today. A sip.MobileKey is one
// keystroke with modifiers, while every TUIOS command is a chord: the
// leader, and then the key bound to the command. A button for each command
// has to wait for sip to be able to say that, so the bar offers the half it
// can send honestly and leaves the command key to the software keyboard.
func mobileKeys(leader string) []sip.MobileKey {
	keys := []sip.MobileKey{
		{Label: "esc", Title: "Escape", Key: "Escape"},
		{Label: "tab", Title: "Tab", Key: "Tab"},
		{Label: "ctrl", Title: "Ctrl (tap to arm, tap again to lock)", Mod: "ctrl"},
		{Label: "alt", Title: "Alt (tap to arm, tap again to lock)", Mod: "alt"},
		{Label: "←", Title: "Left", Key: "ArrowLeft", Narrow: true},
		{Label: "↓", Title: "Down", Key: "ArrowDown", Narrow: true},
		{Label: "↑", Title: "Up", Key: "ArrowUp", Narrow: true},
		{Label: "→", Title: "Right", Key: "ArrowRight", Narrow: true},
		{Label: "/", Title: "Slash", Key: "/", Narrow: true},
		{Label: "-", Title: "Minus", Key: "-", Narrow: true},
		{Label: "|", Title: "Pipe", Key: "|", Narrow: true},
		{Label: ":", Title: "Colon", Key: ":", Narrow: true},
	}

	if pfx, ok := leaderMobileKey(leader); ok {
		keys = append([]sip.MobileKey{pfx}, keys...)
	}
	return keys
}

// leaderMobileKey turns the configured leader into its button.
//
// The leader is rebindable, so it is read rather than assumed: a bar with
// ctrl+b baked into it would lie to everyone who changed it. A leader the
// browser cannot be asked to send gets no button at all, since a button that
// sends the wrong thing is worse than one that is missing.
func leaderMobileKey(leader string) (sip.MobileKey, bool) {
	combo, err := tape.ParseKeyCombo(leader)
	if err != nil {
		return sip.MobileKey{}, false
	}
	key, ok := browserKeyName(combo.Key)
	if !ok {
		return sip.MobileKey{}, false
	}
	return sip.MobileKey{
		Label: "pfx",
		Title: fmt.Sprintf("Prefix (%s), then the command key", leader),
		Key:   key,
		Ctrl:  combo.Ctrl,
		Alt:   combo.Alt,
		Shift: combo.Shift,
	}, true
}

// browserKeyNames maps the key names TUIOS config uses onto the
// KeyboardEvent names sip's client encodes. Only the names sip can turn into
// bytes are here; anything else reports false so the caller can drop the key
// rather than send something the terminal never receives.
var browserKeyNames = map[string]string{
	"esc":       "Escape",
	"escape":    "Escape",
	"tab":       "Tab",
	"enter":     "Enter",
	"return":    "Enter",
	"space":     " ",
	"backspace": "Backspace",
	"delete":    "Delete",
	"insert":    "Insert",
	"up":        "ArrowUp",
	"down":      "ArrowDown",
	"left":      "ArrowLeft",
	"right":     "ArrowRight",
	"home":      "Home",
	"end":       "End",
	"pgup":      "PageUp",
	"pageup":    "PageUp",
	"pgdown":    "PageDown",
	"pagedown":  "PageDown",
}

// browserKeyName resolves one TUIOS key name to what the browser calls it.
func browserKeyName(key string) (string, bool) {
	if key == "" {
		return "", false
	}
	if named, ok := browserKeyNames[strings.ToLower(key)]; ok {
		return named, true
	}
	// A single character is itself. Longer names that got this far are keys
	// sip has no encoding for, the function keys among them.
	if len([]rune(key)) == 1 {
		return key, true
	}
	return "", false
}
