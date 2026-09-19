package input

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// lockMods are the modifier bits a terminal reports for the lock keys. They say
// nothing about which chord was struck and have to come off before a key event
// is compared against a binding.
const lockMods = tea.ModCapsLock | tea.ModNumLock | tea.ModScrollLock

// bindingKeys returns every spelling a binding for msg may be written as, most
// literal first. One physical chord reaches tuios under several names depending
// on what the host terminal negotiated, and a binding has to answer to all of
// them:
//
//   - Key.String() is the text the key produced when there is one, so a binding
//     on "!" matches whether the terminal sent the character or the chord.
//   - Key.Keystroke() is the chord spelling. It prefers the PC-101 base code
//     that the Kitty protocol's alternate-key reporting supplies, which is what
//     turns a composed or non-US-layout key back into the key the user pressed.
//   - macOptionChord covers macOS Option chords that arrive as the character the
//     OS composed, with the Alt bit set or (without the Kitty protocol) missing
//     entirely.
func bindingKeys(msg tea.KeyPressMsg) []string {
	key := msg.String()
	keys := []string{key}
	if stroke := msg.Keystroke(); stroke != key {
		keys = append(keys, stroke)
	}
	if chord, ok := macOptionChord(msg); ok && chord != key {
		// Four of the Option+letter chords (e, i, n, u) compose the same
		// character shifted as unshifted, because unshifted they are dead keys
		// whose accent only lands once a second key ends the composition. When
		// the terminal reports the Shift bit the two are still tellable apart,
		// and the shifted reading is the more specific one: alt+shift+n walks
		// sessions while alt+n walks windows.
		if msg.Mod&tea.ModShift != 0 {
			if shifted := strings.Replace(chord, "alt+", "alt+shift+", 1); shifted != chord &&
				!strings.Contains(chord, "shift+") {
				keys = append(keys, shifted)
			}
		}
		keys = append(keys, chord)
	}
	return keys
}

// lookupAction resolves msg against a registry lookup, trying each spelling in
// turn. Returns "" when nothing is bound.
func lookupAction(msg tea.KeyPressMsg, get func(string) string) string {
	for _, key := range bindingKeys(msg) {
		if action := get(key); action != "" {
			return action
		}
	}
	return ""
}

// macOptionChord returns the alt+ chord a macOS Option press stands for when the
// OS composed a character out of it, and whether msg is such a press.
//
// macOS treats Option as a compose key unless the terminal is told otherwise, so
// Option+n arrives as the tilde it composed. Terminals differ in what they do
// with the modifier: without the Kitty protocol the glyph arrives bare, and with
// it the Alt bit is set but the code is still the composed character. Both spell
// the same chord, so both are accepted here; anything carrying Ctrl or Super is
// not a chord macOS composes for and is left alone.
//
// Darwin only. Every one of these glyphs is an ordinary typed character on some
// other layout (£ is Shift+3 on a UK keyboard), so doing this anywhere else
// would eat real input on its way to the shell.
func macOptionChord(msg tea.KeyPressMsg) (string, bool) {
	if !runtimeIsDarwin() {
		return "", false
	}
	if mods := msg.Mod &^ lockMods; mods&^(tea.ModAlt|tea.ModShift) != 0 {
		return "", false
	}
	r := chordRune(msg)
	if r == 0 {
		return "", false
	}
	return config.MacOSOptionChord(r)
}

// chordRune is the single character a key event carries, from the key code when
// it has one and from the text otherwise.
func chordRune(msg tea.KeyPressMsg) rune {
	if msg.Code != 0 && msg.Code != tea.KeyExtended {
		return msg.Code
	}
	runes := []rune(msg.Text)
	if len(runes) != 1 {
		return 0
	}
	return runes[0]
}

// macRewrittenAltArrowKeys are the readline word motions a macOS terminal
// sends in place of Option+Left and Option+Right, mapped to the chord the user
// actually pressed.
//
// ESC b and ESC f are word-back and word-forward, and sending them for
// Option+arrow is the macOS convention rather than a fault. It costs tuios the
// two chords all the same, because what arrives says nothing about an arrow
// key having been pressed.
var macRewrittenAltArrowKeys = map[string]string{
	"alt+b": "alt+left",
	"alt+f": "alt+right",
}

// macRewrittenAltArrow reports whether a key press is one of those, and which
// arrow chord it stands for.
//
// Only on darwin, and only for the plain chord: alt+b typed on a Linux box is
// a user's own binding, and ctrl+alt+b is not something any terminal sends for
// an arrow key.
//
// This does not rebind anything. The pair is genuinely ambiguous, since a
// shell wants ESC b and ESC f for word movement, so tuios keeps its hands off
// them and says what happened instead.
func macRewrittenAltArrow(msg tea.KeyPressMsg) (got, arrow string, ok bool) {
	if !runtimeIsDarwin() {
		return "", "", false
	}
	if mods := msg.Mod &^ lockMods; mods != tea.ModAlt {
		return "", "", false
	}
	got = msg.Keystroke()
	arrow, ok = macRewrittenAltArrowKeys[got]
	return got, arrow, ok
}
