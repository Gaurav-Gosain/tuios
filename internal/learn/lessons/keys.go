package lessons

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// ChordParts splits a chord: "ctrl+b" is ["ctrl", "b"]. "+" alone stays.
func ChordParts(chord string) []string {
	if chord == "+" || !strings.Contains(chord, "+") {
		return []string{chord}
	}
	return strings.Split(chord, "+")
}

var namedBytes = map[string]string{
	"enter": "\r", "esc": "\x1b", "escape": "\x1b", "tab": "\t", "space": " ",
	"backspace": "\x7f", "up": "\x1b[A", "down": "\x1b[B", "right": "\x1b[C", "left": "\x1b[D",
}

var arrowFinal = map[string]string{"up": "A", "down": "B", "right": "C", "left": "D"}

// KeyBytes is what "show me" types for a key: the bytes a terminal sends for
// a chord, or the text itself.
func KeyBytes(k Key) string {
	if k.Text != "" {
		return k.Text
	}
	parts := ChordParts(k.Chord)
	base := parts[len(parts)-1]
	mods := map[string]bool{}
	for _, m := range parts[:len(parts)-1] {
		mods[m] = true
	}
	if mods["shift"] && base == "tab" {
		return "\x1b[Z"
	}
	// Arrows with a modifier, the way xterm sends them: alt+left is CSI 1;3D.
	if final, ok := arrowFinal[base]; ok && len(mods) > 0 {
		code := 1
		if mods["shift"] {
			code++
		}
		if mods["alt"] {
			code += 2
		}
		if mods["ctrl"] {
			code += 4
		}
		return fmt.Sprintf("\x1b[1;%d%s", code, final)
	}
	out, ok := namedBytes[base]
	if !ok {
		out = base
	}
	if mods["ctrl"] && len(base) == 1 {
		c := strings.ToLower(base)[0]
		if c < 'a' || c > 'z' {
			// ctrl and a digit or a symbol has no control byte of its
			// own: ctrl+2 is not ctrl+r. The kitty keyboard protocol
			// spells it out, and tuios reads that.
			code := 5
			if mods["alt"] {
				code += 2
			}
			if mods["shift"] {
				code++
			}
			return fmt.Sprintf("\x1b[%d;%du", c, code)
		}
		out = string(rune(c & 0x1f))
	}
	if mods["alt"] {
		out = "\x1b" + out
	}
	return out
}

// macOption maps what Option and a key types on a US Mac to the key, for a
// chord that reaches tuios as the composed glyph.
var macOption = map[string]string{
	"å": "a", "∫": "b", "ç": "c", "∂": "d", "´": "e", "ƒ": "f", "©": "g", "˙": "h",
	"ˆ": "i", "∆": "j", "˚": "k", "¬": "l", "µ": "m", "˜": "n", "ø": "o", "π": "p",
	"œ": "q", "®": "r", "ß": "s", "†": "t", "¨": "u", "√": "v", "∑": "w", "≈": "x",
	"¥": "y", "Ω": "z", "¡": "1", "™": "2", "£": "3", "¢": "4", "∞": "5", "§": "6",
	"¶": "7", "•": "8", "ª": "9", "º": "0",
}

// fromMacOption: "alt+∆" and a bare "∆" are the Mac spellings of alt+j.
func fromMacOption(key string) string {
	glyph := strings.TrimPrefix(key, "alt+")
	if base, ok := macOption[glyph]; ok {
		return "alt+" + base
	}
	return key
}

// IsAltChord reports whether a chord uses alt, in tuios's notation or as a
// Mac Option glyph.
func IsAltChord(chord string) bool {
	parts := ChordParts(fromMacOption(chord))
	return slices.Contains(parts[:len(parts)-1], "alt")
}

var modifierKey = regexp.MustCompile(`(^|\+)((left|right)(shift|alt|ctrl|super|hyper|meta)|isolevel[35]shift)$`)

// IsModifierKey reports whether tuios reported a modifier on its own, which
// comes before the chord it starts.
func IsModifierKey(key string) bool { return modifierKey.MatchString(key) }

var shiftOne = regexp.MustCompile(`^shift\+(.)$`)

// SameChord reports whether the key tuios reported is the chord a step asks
// for. tuios reports "|" for shift and backslash, case does not matter, and
// a Mac Option glyph is the alt chord.
func SameChord(reported, wanted string) bool {
	if reported == wanted {
		return true
	}
	norm := func(k string) string {
		k = fromMacOption(k)
		if m := shiftOne.FindStringSubmatch(k); m != nil {
			k = m[1]
		}
		return strings.ToLower(k)
	}
	return norm(reported) == norm(wanted)
}

var (
	chordMods  = map[string]bool{"ctrl": true, "alt": true, "shift": true, "super": true}
	namedChord = map[string]bool{
		"enter": true, "esc": true, "tab": true, "space": true, "backspace": true,
		"up": true, "down": true, "left": true, "right": true, "home": true, "end": true,
		"pgup": true, "pgdown": true, "delete": true, "insert": true,
	}
)

// CheckChord reports whether a step's chord is one the engine can match and
// "show me" can type: modifiers from ctrl, alt, shift and super, each once,
// then one character or a named key.
func CheckChord(chord string) error {
	if chord == "" {
		return fmt.Errorf("an empty key")
	}
	parts := ChordParts(chord)
	base := parts[len(parts)-1]
	seen := map[string]bool{}
	for _, m := range parts[:len(parts)-1] {
		if !chordMods[m] || seen[m] {
			return fmt.Errorf("key %q: %q is not a modifier, or it comes twice", chord, m)
		}
		seen[m] = true
	}
	if utf8.RuneCountInString(base) == 1 || namedChord[base] {
		return nil
	}
	if f := strings.TrimPrefix(base, "f"); f != base && len(f) <= 2 && strings.Trim(f, "0123456789") == "" {
		return nil
	}
	return fmt.Errorf("key %q: %q is not a character or a named key", chord, base)
}
