package config

import "strings"

// Ambiguity is a pair of key names that a terminal sends as the same byte, so
// binding one of them binds both.
//
// This is not a tuios quirk to be worked around. It is what a VT100 keyboard
// was: Ctrl+letter is the letter's code with the top three bits cleared, and
// Tab, Return and Escape happen to sit exactly where Ctrl+I, Ctrl+M and Ctrl+[
// land. Nothing downstream of the terminal can undo that, which is why the only
// thing that changes the answer is the terminal agreeing to send something else.
type Ambiguity struct {
	// Keys are the names that collapse onto one another.
	Keys []string `json:"keys"`
	// Byte is the control code they all arrive as in the legacy encoding.
	Byte byte `json:"byte"`
	// Why is the one sentence explaining the pair.
	Why string `json:"why"`
	// Separable is whether the Kitty keyboard protocol's disambiguation can
	// tell them apart when the host grants it. All the control-code pairs can
	// be; the pair kept here that cannot is noted in its Why.
	Separable bool `json:"separable"`
}

// Ambiguities is every pair worth warning about. Kept short on purpose: these
// are the ones a user reaches for and is then surprised by, not every control
// code in the table.
var Ambiguities = []Ambiguity{
	{
		Keys: []string{"ctrl+i", "tab"}, Byte: 0x09, Separable: true,
		Why: "Ctrl+I is byte 0x09, which is what Tab sends. Without disambiguation the two are one key.",
	},
	{
		Keys: []string{"ctrl+m", "enter", "return"}, Byte: 0x0D, Separable: true,
		Why: "Ctrl+M is byte 0x0D, carriage return, which is what Enter sends.",
	},
	{
		Keys: []string{"ctrl+[", "esc", "escape"}, Byte: 0x1B, Separable: true,
		Why: "Ctrl+[ is byte 0x1B, escape. Binding it also binds Esc, and Esc starts every escape sequence the terminal sends.",
	},
	{
		Keys: []string{"ctrl+h", "backspace"}, Byte: 0x08, Separable: true,
		Why: "Ctrl+H is byte 0x08. Terminals disagree about whether Backspace sends 0x08 or 0x7F, so this pair collides on some hosts and not others.",
	},
	{
		Keys: []string{"ctrl+j", "ctrl+enter"}, Byte: 0x0A, Separable: true,
		Why: "Ctrl+J is byte 0x0A, line feed. Some terminals send it for Ctrl+Enter and a few send it for Enter.",
	},
	{
		Keys: []string{"ctrl+space", "ctrl+@"}, Byte: 0x00, Separable: true,
		Why: "Ctrl+Space is the null byte, which is also Ctrl+@. Some terminals send nothing at all for it.",
	},
}

// AmbiguityFor returns the pair a key belongs to, and whether it is in one.
func AmbiguityFor(key string) (Ambiguity, bool) {
	key = strings.ToLower(strings.TrimSpace(key))
	for _, a := range Ambiguities {
		for _, k := range a.Keys {
			if k == key {
				return a, true
			}
		}
	}
	return Ambiguity{}, false
}

// AmbiguityPartners returns the other names in key's pair, excluding key
// itself, or nil when it is in no pair.
func AmbiguityPartners(key string) []string {
	a, ok := AmbiguityFor(key)
	if !ok {
		return nil
	}
	key = strings.ToLower(strings.TrimSpace(key))
	var out []string
	for _, k := range a.Keys {
		if k != key {
			out = append(out, k)
		}
	}
	return out
}

// AmbiguityVerdict is what to tell a user about a key they just pressed, given
// whether the host terminal agreed to disambiguate.
//
// The host's answer is the whole of it. tuios asks for disambiguation on every
// view; a terminal that granted it sends Ctrl+I as an escape sequence carrying
// the modifier, and the pair genuinely separates. A terminal that did not sends
// 0x09 and there is nothing to separate.
func AmbiguityVerdict(key string, hostDisambiguates bool) string {
	a, ok := AmbiguityFor(key)
	if !ok {
		return ""
	}
	partners := strings.Join(AmbiguityPartners(key), " and ")
	if !a.Separable {
		return a.Why + " This host cannot separate them, so " + key + " and " + partners + " are the same binding."
	}
	if hostDisambiguates {
		return a.Why + " This terminal reports disambiguated keys, so " + key + " and " + partners + " are separate here. On a terminal that does not, they are one key."
	}
	return a.Why + " This terminal has not granted key disambiguation, so binding " + key + " also binds " + partners + "."
}
