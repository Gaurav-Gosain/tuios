package input

import (
	"slices"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
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
//   - Key.Keystroke() is the chord spelling.
//   - shiftedKey is the chord spelled with the character Shift gave, when the
//     Kitty protocol reports it: on AZERTY Option+Shift+& is alt+1.
//   - baseLayoutKey is the US key at the same position, for a key whose
//     produced character no binding can be spelled in (see usesBaseLayout).
//
// After those come the keys that only match under an assumption about the
// keyboard, which the registry keeps in tiers of their own, so that a binding
// written for the key itself always wins:
//
//   - The US-layout tier (config.USLayoutKey): the US spellings of a shifted
//     digit, and the chord a macOS Option character with the Alt bit stands
//     for. Asked only when the event does not contradict a US layout.
//   - The Option-glyph tier (config.OptionGlyphKey): the chord a macOS Option
//     character with no Alt modifier stands for. The registry fills it only
//     when keybindings.option_glyphs is "bind". Without that, the character is
//     text and goes to the pane (issue #566).
//
// base is the base-layout key the host sent with msg. The input path reads a
// key without it (see readKey), so the caller hands it back here.
func bindingKeys(msg tea.KeyPressMsg, base rune) []string {
	// Keystroke() and String() spell a key from its base-layout code when it
	// has one. The key produced is tried first, spelled without it, and the
	// base-layout key last and only where usesBaseLayout allows it. Otherwise
	// German Ctrl+Z matched only ctrl+y, and an unbound AZERTY "a" ran quit.
	orig := msg
	msg = producedKey(msg)
	key := msg.String()
	keys := []string{key}
	if stroke := msg.Keystroke(); stroke != key {
		keys = append(keys, stroke)
	}
	if shifted, ok := shiftedKey(msg); ok && !slices.Contains(keys, shifted) {
		keys = append(keys, shifted)
	}
	if pos, ok := baseLayoutKey(orig); ok && !slices.Contains(keys, pos) {
		keys = append(keys, pos)
	}
	plain := len(keys)

	mods := msg.Mod &^ lockMods
	if config.KeyFitsUSLayout(msg.Code, msg.ShiftedCode, base, mods&tea.ModShift != 0) {
		for _, k := range keys[:plain] {
			keys = append(keys, config.USLayoutKey(k))
		}
	}
	if chord, ok := macOptionChord(msg); ok && chord != key {
		tier := config.OptionGlyphKey
		if mods&tea.ModAlt != 0 {
			tier = config.USLayoutKey
		}
		// Four of the Option+letter chords (e, i, n, u) compose the same
		// character shifted as unshifted, because unshifted they are dead keys
		// whose accent only lands once a second key ends the composition. When
		// the terminal reports the Shift bit the two are still tellable apart,
		// and the shifted reading is the more specific one: alt+shift+n walks
		// sessions while alt+n walks windows.
		if mods&tea.ModShift != 0 {
			if shifted := strings.Replace(chord, "alt+", "alt+shift+", 1); shifted != chord &&
				!strings.Contains(chord, "shift+") {
				keys = append(keys, tier(shifted))
			}
		}
		keys = append(keys, tier(chord))
	}
	return keys
}

// shiftedKey spells msg with the character Shift gave in place of the key and
// the Shift modifier, when the terminal reported that character (the Kitty
// protocol's shifted key). On AZERTY the digits are the shifted characters of
// the number row, so Option+Shift and the & key is alt+1 here, the chord the
// default opt+1 binding names.
//
// A letter is left out. Its capital is the same key, which the binding tables
// already read case-blind, and alt+A would match a binding on alt+a.
func shiftedKey(msg tea.KeyPressMsg) (string, bool) {
	mods := msg.Mod &^ lockMods
	if mods&tea.ModShift == 0 || msg.ShiftedCode == 0 || msg.ShiftedCode == msg.Code {
		return "", false
	}
	if unicode.IsLetter(msg.ShiftedCode) || !unicode.IsPrint(msg.ShiftedCode) {
		return "", false
	}
	k := tea.Key{Code: msg.ShiftedCode, Mod: mods &^ tea.ModShift}
	return k.Keystroke(), true
}

// lookupAction resolves msg against a registry lookup, trying each spelling in
// turn. Returns "" when nothing is bound. o gives back the base-layout key the
// host sent with msg; it may be nil when msg still carries its own.
func lookupAction(o *app.OS, msg tea.KeyPressMsg, get func(string) string) string {
	base := msg.BaseCode
	if base == 0 && o != nil {
		base = o.HostBaseCode(msg)
	}
	for _, key := range bindingKeys(msg, base) {
		if action := get(key); action != "" {
			return action
		}
	}
	return ""
}

// macOptionChord returns the alt+ chord a macOS Option press stands for when the
// OS composed a character out of it on a US layout, and whether msg is such a
// press.
//
// macOS treats Option as a compose key unless the terminal is told otherwise, so
// Option+n arrives as the tilde it composed. Terminals differ in what they do
// with the modifier: without the Kitty protocol the glyph arrives bare, and with
// it the Alt bit is set but the code is still the composed character. Both are
// recognised here; anything carrying Ctrl or Super is not a chord macOS
// composes for and is left alone. Recognising one is not running it:
// bindingKeys asks for the chord only in the tiers that hold under an
// assumption, and a bare character is text unless keybindings.option_glyphs
// is "bind".
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
