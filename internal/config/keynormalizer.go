package config

import (
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"unicode"
	"unicode/utf8"
)

// maxFunctionKey is the highest function key Bubble Tea names (KeyF63). A
// terminal sends F13 to F24 as ordinary escape sequences, and keys above that
// under the Kitty keyboard protocol.
const maxFunctionKey = 63

// isFunctionKeyName reports whether name is f1 to f63, the names Bubble Tea
// gives the function keys.
func isFunctionKeyName(name string) bool {
	digits, ok := strings.CutPrefix(name, "f")
	if !ok || digits == "" || digits[0] == '0' {
		return false
	}
	n, err := strconv.Atoi(digits)
	return err == nil && n >= 1 && n <= maxFunctionKey
}

// isSingleRuneLetter reports whether s is exactly one rune and that rune is a
// letter. Keys that are a single letter must preserve case (m and M, é and É
// are distinct keys in Bubbletea); compound keys are lowercased. The rune-aware
// check is required so multi-byte AZERTY letters (é, è, à, ç) are not rejected
// or case-folded by a byte-length test.
func isSingleRuneLetter(s string) bool {
	if utf8.RuneCountInString(s) != 1 {
		return false
	}
	r, _ := utf8.DecodeRuneInString(s)
	return unicode.IsLetter(r)
}

// modifierAliases maps every modifier spelling tuios accepts in config.toml to
// the name Bubble Tea gives the modifier in a key event. opt and option are the
// macOS names for Alt, cmd and command the macOS names for Super.
var modifierAliases = map[string]string{
	"ctrl": "ctrl", "control": "ctrl",
	"alt": "alt", "opt": "alt", "option": "alt",
	"shift": "shift",
	"meta":  "meta",
	"hyper": "hyper",
	"super": "super", "cmd": "super", "command": "super",
}

// modifierOrder is the order Bubble Tea writes modifiers in a key event's
// string (ultraviolet's Key.Keystroke). A binding spelled in any other order
// has to be put in this one, or "shift+ctrl+x" never equals the "ctrl+shift+x"
// a key press produces.
var modifierOrder = []string{"ctrl", "alt", "shift", "meta", "hyper", "super"}

// splitKeyChord splits a lowercased chord into its modifiers and its base key.
// A trailing "++" is the plus key itself, so "ctrl++" is ctrl and "+". ok is
// false when a modifier or the base is empty ("ctrl+", "ctrl++x").
func splitKeyChord(lower string) (mods []string, base string, ok bool) {
	if lower == "+" {
		return nil, "+", true
	}
	var rest string
	if strings.HasSuffix(lower, "++") {
		base, rest = "+", strings.TrimSuffix(lower, "++")
	} else {
		i := strings.LastIndex(lower, "+")
		if i < 0 {
			return nil, lower, true
		}
		base, rest = lower[i+1:], lower[:i]
	}
	if base == "" || rest == "" {
		return nil, "", false
	}
	mods = strings.Split(rest, "+")
	for _, m := range mods {
		if m == "" {
			return nil, "", false
		}
	}
	return mods, base, true
}

// CanonicalKey is the one spelling of a key that tuios compares against a key
// event. Every key that comes from config.toml or from a command line goes
// through it before it is matched: the leader, every binding table, and the
// argument of `tuios keybinds explain`, `free` and `unbind`.
//
// A single letter keeps its case (m and M are different keys). Anything else
// is lowercased, each modifier alias becomes the name Bubble Tea uses
// (opt+f12 and option+f12 become alt+f12, cmd+v becomes super+v, control+b
// becomes ctrl+b), a repeated modifier is dropped, and the modifiers are put
// in Bubble Tea's order. A chord with a modifier tuios does not know, or with
// an empty part, comes back lowercased and otherwise untouched, so the
// validator can still name what is wrong with it.
func CanonicalKey(key string) string {
	key = strings.TrimSpace(key)
	if isSingleRuneLetter(key) {
		return key
	}
	lower := strings.ToLower(key)
	if !strings.Contains(lower, "+") || lower == "+" {
		return lower
	}
	mods, base, ok := splitKeyChord(lower)
	if !ok {
		return lower
	}
	have := make(map[string]bool, len(mods))
	for _, m := range mods {
		name, known := modifierAliases[m]
		if !known {
			return lower
		}
		have[name] = true
	}
	var sb strings.Builder
	sb.Grow(len(lower))
	for _, name := range modifierOrder {
		if have[name] {
			sb.WriteString(name)
			sb.WriteByte('+')
		}
	}
	sb.WriteString(base)
	return sb.String()
}

// SameKey reports whether two key spellings name the same key press.
func SameKey(a, b string) bool {
	return a == b || CanonicalKey(a) == CanonicalKey(b)
}

// optionToAltReplacer converts opt/option to alt for consistent key naming
var optionToAltReplacer = strings.NewReplacer("opt+", "alt+", "option+", "alt+")

// altToOptReplacer converts alt to opt/option variants
var altToOptReplacer = strings.NewReplacer("alt+", "opt+")
var altToOptionReplacer = strings.NewReplacer("alt+", "option+")

// KeyNormalizer handles platform-specific key normalization
// Converts user-friendly key strings (like "opt+1" on macOS) to their actual representations
type KeyNormalizer struct {
	isMacOS bool
}

// NewKeyNormalizer creates a new key normalizer with platform detection
func NewKeyNormalizer() *KeyNormalizer {
	return &KeyNormalizer{
		isMacOS: detectMacOS(),
	}
}

// IsMacOS returns whether the current platform is macOS
func (kn *KeyNormalizer) IsMacOS() bool {
	return kn.isMacOS
}

// detectMacOS checks if the current platform is macOS. See macOSHost.
func detectMacOS() bool { return macOSHost }

// macOS Option key mappings (opt+number produces unicode characters)
var macOptionNumberMap = map[string]string{
	"opt+1": "¡", "option+1": "¡",
	"opt+2": "™", "option+2": "™",
	"opt+3": "£", "option+3": "£",
	"opt+4": "¢", "option+4": "¢",
	"opt+5": "∞", "option+5": "∞",
	"opt+6": "§", "option+6": "§",
	"opt+7": "¶", "option+7": "¶",
	"opt+8": "•", "option+8": "•",
	"opt+9": "ª", "option+9": "ª",
}

// macOS Option+Shift key mappings
var macOptionShiftNumberMap = map[string]string{
	"opt+shift+1": "⁄", "option+shift+1": "⁄",
	"opt+shift+2": "€", "option+shift+2": "€",
	"opt+shift+3": "‹", "option+shift+3": "‹",
	"opt+shift+4": "›", "option+shift+4": "›",
	"opt+shift+5": "ﬁ", "option+shift+5": "ﬁ",
	"opt+shift+6": "ﬂ", "option+shift+6": "ﬂ",
	"opt+shift+7": "‡", "option+shift+7": "‡",
	"opt+shift+8": "°", "option+shift+8": "°",
	"opt+shift+9": "·", "option+shift+9": "·",
}

// macOS Option+Tab key mappings
var macOptionTabMap = map[string]string{
	"opt+tab": "⇥", "option+tab": "⇥",
	"opt+shift+tab": "⇤", "option+shift+tab": "⇤",
}

// macOptionLetters is the character a US macOS layout produces for Option+letter
// while the terminal composes instead of sending Alt (the default in Terminal.app,
// iTerm2, Ghostty and kitty). tuios never sees a modifier for those chords, only
// the composed glyph, so a binding written as alt+n has to answer to it as well.
//
// The four dead keys (e, i, n, u) emit their accent only once a second key ends
// the composition, so the glyph below is what arrives after that; a terminal that
// swallows the dead key entirely gives tuios nothing to match, which is what
// MacOptionAdvice exists to explain.
var macOptionLetters = map[string]string{
	"a": "å", "b": "∫", "c": "ç", "d": "∂", "e": "´", "f": "ƒ", "g": "©",
	"h": "˙", "i": "ˆ", "j": "∆", "k": "˚", "l": "¬", "m": "µ", "n": "˜",
	"o": "ø", "p": "π", "q": "œ", "r": "®", "s": "ß", "t": "†", "u": "¨",
	"v": "√", "w": "∑", "x": "≈", "y": "¥", "z": "Ω",
}

// macOptionShiftLetters is macOptionLetters for Option+Shift. The e, i, n and u
// entries are deliberately absent: their Option+Shift glyphs are the same
// codepoints the dead keys spill (´ ˆ ˜ ¨), and those are far more likely to be
// meant as the unshifted chord, which is the one this bug was reported against.
var macOptionShiftLetters = map[string]string{
	"a": "Å", "b": "ı", "c": "Ç", "d": "Î", "f": "Ï", "g": "˝",
	"h": "Ó", "j": "Ô", "k": "", "l": "Ò", "m": "Â",
	"o": "Ø", "p": "∏", "q": "Œ", "r": "‰", "s": "Í", "t": "ˇ",
	"v": "◊", "w": "„", "x": "˛", "y": "Á", "z": "¸",
}

// macOptionChords reverses every Option table: composed glyph → the chord it
// stands for. Built once, so the input path can ask what an unmodified glyph
// meant without walking four maps.
var macOptionChords = func() map[rune]string {
	chords := make(map[rune]string, 64)
	add := func(glyph, chord string) {
		r, size := utf8.DecodeRuneInString(glyph)
		if size == 0 || len(glyph) != size {
			return
		}
		if _, taken := chords[r]; !taken {
			chords[r] = chord
		}
	}
	for chord, glyph := range macOptionNumberMap {
		if strings.HasPrefix(chord, "opt+") {
			add(glyph, optionToAltReplacer.Replace(chord))
		}
	}
	for chord, glyph := range macOptionShiftNumberMap {
		if strings.HasPrefix(chord, "opt+") {
			add(glyph, optionToAltReplacer.Replace(chord))
		}
	}
	for chord, glyph := range macOptionTabMap {
		if strings.HasPrefix(chord, "opt+") {
			add(glyph, optionToAltReplacer.Replace(chord))
		}
	}
	for letter, glyph := range macOptionLetters {
		add(glyph, "alt+"+letter)
	}
	for letter, glyph := range macOptionShiftLetters {
		add(glyph, "alt+shift+"+letter)
	}
	return chords
}()

// MacOSOptionChord returns the alt+ chord a composed macOS Option glyph stands
// for, and whether r is such a glyph. Only meaningful on darwin: these glyphs are
// ordinary typed characters elsewhere.
func MacOSOptionChord(r rune) (string, bool) {
	chord, ok := macOptionChords[r]
	return chord, ok
}

// shiftedDigits maps a digit to the character a US layout produces when it is
// typed with Shift. Terminals disagree about which of the two spellings they
// report for the same physical chord: some send the shifted character ("!"),
// others report the chord ("shift+1"). Binding one spelling has to match both,
// otherwise a binding works on one terminal and silently does nothing on
// another.
var shiftedDigits = map[string]string{
	"1": "!", "2": "@", "3": "#", "4": "$", "5": "%",
	"6": "^", "7": "&", "8": "*", "9": "(", "0": ")",
}

// shiftedDigitsReverse is shiftedDigits inverted, for the "!" → "shift+1"
// direction.
var shiftedDigitsReverse = func() map[string]string {
	m := make(map[string]string, len(shiftedDigits))
	for digit, symbol := range shiftedDigits {
		m[symbol] = digit
	}
	return m
}()

// shiftAliases returns the alternate spellings of a shifted key: the shifted
// character for a "shift+x" chord and the "shift+x" chord for a shifted
// character. Returns nil when key is not a shifted key in either spelling.
//
// It is the union of letterShiftAliases, which hold on every layout, and
// usShiftAliases, which hold only on a US layout.
func shiftAliases(key, keyLower string) []string {
	if out := letterShiftAliases(key, keyLower); out != nil {
		return out
	}
	return usShiftAliases(key, keyLower)
}

// letterShiftAliases returns the other spelling of a shifted letter: "A" for
// "shift+a" and "shift+a" for "A". A letter and its capital are one key on
// every layout, so these are read as the key itself.
func letterShiftAliases(key, keyLower string) []string {
	if base, ok := strings.CutPrefix(keyLower, "shift+"); ok {
		if isSingleRuneLetter(base) {
			return []string{strings.ToUpper(base)}
		}
		return nil
	}
	// An uppercase letter is the shifted spelling of its lowercase self.
	if isSingleRuneLetter(key) && key != keyLower {
		return []string{"shift+" + keyLower}
	}
	return nil
}

// usShiftAliases returns the spellings a shifted digit has on a US layout:
// "!" for "shift+1", "shift+1" for "!", and the same with other modifiers.
//
// These hold only where the key sits as it does on a US keyboard. On French
// AZERTY "&" is an unshifted key and the digits are shifted, so reading "&" as
// shift+7 ran move_and_follow_7 for Option and the 1 key (issue #575). The
// registry keeps them in a tier of their own (see USLayoutKey), and the input
// path asks that tier only when the key event does not contradict a US layout.
func usShiftAliases(key, keyLower string) []string {
	if base, ok := strings.CutPrefix(keyLower, "shift+"); ok {
		if symbol, ok := shiftedDigits[base]; ok {
			return []string{symbol}
		}
		return nil
	}
	if digit, ok := shiftedDigitsReverse[key]; ok {
		return []string{"shift+" + digit}
	}
	return modifiedShiftAliases(keyLower)
}

// modifiedShiftAliases does for a chord carrying other modifiers what
// usShiftAliases does for a bare one: "alt+shift+1" also answers to "alt+!"
// (what a terminal without the Kitty protocol sends) and to "alt+shift+!"
// (what xterm's modifyOtherKeys sends).
//
// Only digits are aliased. A shifted letter needs no help, since lookup
// case-folds compound keys, and aliasing "alt+shift+n" to "alt+n" would
// silently steal an unrelated binding. An unshifted "alt+1" is a different
// physical chord and gets no aliases, so it keeps its own action.
func modifiedShiftAliases(keyLower string) []string {
	i := strings.LastIndex(keyLower, "+")
	if i <= 0 {
		return nil
	}
	mods, base := keyLower[:i+1], keyLower[i+1:]
	shifted := strings.HasSuffix(mods, "shift+")
	mods = strings.TrimSuffix(mods, "shift+")
	if mods == "" {
		return nil
	}
	if digit, ok := shiftedDigitsReverse[base]; ok {
		return []string{mods + base, mods + "shift+" + base, mods + "shift+" + digit}
	}
	if symbol, ok := shiftedDigits[base]; ok && shifted {
		return []string{mods + symbol, mods + "shift+" + symbol}
	}
	return nil
}

// usLayoutTier and optionGlyphTier mark the registry keys that only match
// under an assumption about the keyboard. A key event never spells either, so
// a key in a tier can only be reached by asking for it with USLayoutKey or
// OptionGlyphKey, and a binding written for the key itself always wins.
const (
	usLayoutTier    = "\x00us:"
	optionGlyphTier = "\x00glyph:"
)

// USLayoutKey is the registry key that matches key only on a US layout: a
// shifted-digit alias ("alt+&" for a binding on alt+shift+7). The input path
// asks for it after every plain spelling of a key event has missed, and only
// when the event does not contradict a US layout (see KeyFitsUSLayout).
func USLayoutKey(key string) string { return usLayoutTier + key }

// OptionGlyphKey is the registry key that matches chord when it arrives as the
// character macOS composed for it, with or without the Alt bit. The registry
// fills that tier unless keybindings.option_glyphs is "type" or
// keybindings.keyboard_layout is "other".
func OptionGlyphKey(chord string) string { return optionGlyphTier + chord }

// KeyFitsUSLayout reports whether a key event is consistent with a US layout,
// from what the terminal reported about it. code is the key's code, shifted
// the character it gives with Shift and base the key at the same position on
// a US layout (both from the Kitty protocol, zero when not reported), and
// shift whether Shift was held.
//
// It answers true when nothing contradicts a US layout, which includes every
// terminal that reports none of this. Then the US tables are the best guess
// there is, and keybindings.keyboard_layout = "other" turns them off.
func KeyFitsUSLayout(code, shifted, base rune, shift bool) bool {
	if base != 0 && base != code && base != unicode.ToLower(code) {
		return false
	}
	if shifted == 0 || !shift {
		return true
	}
	// A symbol a US layout types only with Shift is never the unshifted code
	// of a US key, and the protocol reports the unshifted code.
	if _, ok := shiftedDigitsReverse[string(code)]; ok {
		return false
	}
	if symbol, ok := shiftedDigits[string(code)]; ok && symbol != string(shifted) {
		return false
	}
	return true
}

// Keyboard layout values for keybindings.keyboard_layout.
const (
	// KeyboardLayoutUS reads a key the terminal does not describe as if it
	// came from a US layout. The default.
	KeyboardLayoutUS = "us"
	// KeyboardLayoutOther never applies the US tables.
	KeyboardLayoutOther = "other"
)

// KeyboardLayouts is every value keybindings.keyboard_layout accepts.
var KeyboardLayouts = []string{KeyboardLayoutUS, KeyboardLayoutOther}

// Option glyph values for keybindings.option_glyphs.
const (
	// OptionGlyphsType sends a character composed with Option to the pane.
	OptionGlyphsType = "type"
	// OptionGlyphsBind reads a character composed with Option as the Option
	// binding it stands for on a US layout. The default.
	OptionGlyphsBind = "bind"
)

// OptionGlyphModes is every value keybindings.option_glyphs accepts.
var OptionGlyphModes = []string{OptionGlyphsType, OptionGlyphsBind}

// NormalizeKey converts a key string to its canonical form for the current platform
// For example, on macOS: "opt+1" → "¡" or "alt+1" depending on context
func (kn *KeyNormalizer) NormalizeKey(key string) []string {
	key = strings.TrimSpace(key)

	// For single letters, preserve case (M and m are different keys in Bubbletea)
	// For everything else, normalize to lowercase
	var normalized string
	if isSingleRuneLetter(key) {
		normalized = key // Preserve case for single letters
	} else {
		normalized = strings.ToLower(key) // Lowercase for compound keys (ctrl+m, shift+tab, etc.)
	}

	keyLower := strings.ToLower(key)

	// Always include the normalized version
	result := []string{normalized}

	// The canonical spelling is the one a key event produces: opt+f12 arrives
	// as alt+f12, cmd+v as super+v, shift+ctrl+x as ctrl+shift+x. On macOS the
	// Option branch below also adds the alt+ form, but only for a chord that
	// starts with opt+, and never for the other aliases or another order.
	canonical := CanonicalKey(key)
	result = append(result, canonical)

	// Accept both spellings of a shifted letter, on every platform. A shifted
	// digit's US spellings are not here: see USAliasKeys.
	result = append(result, letterShiftAliases(key, keyLower)...)
	result = append(result, letterShiftAliases(canonical, strings.ToLower(canonical))...)

	// The character macOS composes for an Option chord is not a spelling of
	// the binding: a user who composes on purpose (issue #566) types it. The
	// input path reads it through MacOSOptionChord instead, into the tiers
	// USLayoutKey and OptionGlyphKey name, which the config can turn off.
	if kn.isMacOS {
		// Option reaches a terminal as the Alt modifier whatever key it is
		// held with, so the alt+ spelling is always one of the ways an opt+
		// binding actually arrives.
		//
		// It is added here rather than only inside the branches above, because
		// each of those needs a glyph table to match. A key that composes no
		// glyph on macOS would otherwise keep only the opt+ spelling, which no
		// key event produces, and the binding would be dead: opt+esc, the
		// default for terminal_exit_mode, and any opt+<named key> a user writes.
		if strings.HasPrefix(keyLower, "opt+") || strings.HasPrefix(keyLower, "option+") {
			result = append(result, optionToAltReplacer.Replace(keyLower))
		}

		// If the key starts with "alt+", also accept "opt+" and "option+" variants
		if strings.HasPrefix(keyLower, "alt+") {
			result = append(result, altToOptReplacer.Replace(keyLower))
			result = append(result, altToOptionReplacer.Replace(keyLower))
		}
	}

	// Remove duplicates
	seen := make(map[string]bool)
	unique := []string{}
	for _, k := range result {
		if !seen[k] {
			seen[k] = true
			unique = append(unique, k)
		}
	}

	return unique
}

// USAliasKeys returns the spellings key has only on a US layout: the US
// spellings of a shifted digit, for the key as written and for its canonical
// form. NormalizeKey leaves these out, and the registry puts them in the tier
// USLayoutKey names.
func (kn *KeyNormalizer) USAliasKeys(key string) []string {
	key = strings.TrimSpace(key)
	canonical := CanonicalKey(key)
	plain := kn.NormalizeKey(key)
	var out []string
	for _, k := range append(usShiftAliases(key, strings.ToLower(key)),
		usShiftAliases(canonical, strings.ToLower(canonical))...) {
		k = strings.ToLower(k)
		if !slices.Contains(out, k) && !slices.Contains(plain, k) {
			out = append(out, k)
		}
	}
	return out
}

// ExpandKeys takes a slice of user-provided keys and expands them to all platform-specific variants
func (kn *KeyNormalizer) ExpandKeys(keys []string) []string {
	var expanded []string
	for _, key := range keys {
		normalized := kn.NormalizeKey(key)
		expanded = append(expanded, normalized...)
	}

	// Remove duplicates from final list
	seen := make(map[string]bool)
	unique := []string{}
	for _, k := range expanded {
		if !seen[k] {
			seen[k] = true
			unique = append(unique, k)
		}
	}

	return unique
}

// ValidateKey checks if a key string is valid for the current platform
func (kn *KeyNormalizer) ValidateKey(key string) (bool, string) {
	key = strings.TrimSpace(key)
	keyLower := strings.ToLower(key)

	// Empty key
	if keyLower == "" {
		return false, "key cannot be empty"
	}

	// opt+ and option+ are valid on every platform. Off macOS they can only
	// mean Alt, and CanonicalKey reads them as alt+, so a config.toml shared
	// with a Mac works unchanged (issue #556).

	// On macOS, suggest opt+ instead of alt+ for better UX
	// Note: We return true (valid) but will add a warning in validation
	// This is handled separately in validation.go as a warning, not an error

	// Check for invalid modifier combinations
	parts := strings.Split(keyLower, "+")
	if len(parts) > 1 {
		// Extract modifiers (all but last part)
		modifiers := parts[:len(parts)-1]
		actualKey := parts[len(parts)-1]

		// Check for empty actualKey
		if actualKey == "" {
			return false, "key combination incomplete (ends with +)"
		}

		// Check each modifier
		for _, mod := range modifiers {
			if !validModifier(mod) {
				return false, "invalid modifier: " + mod
			}
		}

		// Check for duplicate modifiers. Aliases count as the modifier they
		// stand for, so alt+opt+x names Alt twice.
		modSet := make(map[string]bool)
		for _, mod := range modifiers {
			name := modifierAliases[mod]
			if modSet[name] {
				return false, "duplicate modifier: " + mod
			}
			modSet[name] = true
		}
	}

	// If there are modifiers, check if the actual key is valid
	parts = strings.Split(keyLower, "+")
	actualKey := parts[len(parts)-1]

	// Single-rune keys are always valid (a-z, 0-9, symbols, and multi-byte
	// AZERTY accented letters such as é/è/à/ç).
	if utf8.RuneCountInString(actualKey) == 1 {
		return true, ""
	}

	// Check if it's a valid special key
	if !validSpecialKeys[actualKey] && !isFunctionKeyName(actualKey) {
		return false, "unknown special key: " + actualKey
	}

	return true, ""
}

// validSpecialKeys are the key names ValidateKey accepts beyond single runes
// and function keys. It is only read. The left/right modifier names are keys in
// their own right only under the Kitty keyboard protocol; they are accepted so
// a held-key binding (hold_window_mode) can name one.
var validSpecialKeys = map[string]bool{
	"leftalt": true, "rightalt": true, "leftctrl": true, "rightctrl": true,
	"leftshift": true, "rightshift": true, "leftsuper": true, "rightsuper": true,
	"leftmeta": true, "rightmeta": true, "lefthyper": true, "righthyper": true,
	"enter": true, "return": true, "esc": true, "escape": true,
	"tab": true, "space": true, "backspace": true, "delete": true,
	"up": true, "down": true, "left": true, "right": true,
	"home": true, "end": true, "pgup": true, "pageup": true,
	"pgdown": true, "pagedown": true, "insert": true,
	// Keys Bubble Tea names that most keyboards have but that a terminal
	// reports only under the Kitty keyboard protocol. They suit a held-key
	// binding, which needs that protocol anyway.
	"capslock": true, "scrolllock": true, "numlock": true,
	"printscreen": true, "pause": true, "menu": true,
}

// validModifier reports whether mod is a modifier that works reliably in
// terminals. Super only reaches a terminal that has negotiated the Kitty
// keyboard protocol, but the input path has always acted on super+v and
// shift+super+v for the host paste, so rejecting it would make the working
// default unwritable the moment it became a binding. control is an alias for
// ctrl, cmd and command are aliases for super, and opt and option are
// aliases for alt on every platform.
func validModifier(mod string) bool {
	switch mod {
	case "ctrl", "control", "alt", "opt", "option", "shift", "super", "cmd", "command":
		return true
	}
	return false
}

// leaderSet is the expanded form of one leader spelling, cached so the input
// path does not run the normalizer on every key press.
type leaderSet struct {
	leader string
	macOS  bool
	keys   map[string]bool
}

var leaderCache atomic.Pointer[leaderSet]

// IsLeaderPress reports whether pressed, a key event's string, is the leader
// key spelled as leader in config.toml. pressed must be in the canonical form a
// key event has (see CanonicalKey); it is not normalized here, because this
// runs on every key press. The leader goes through the same
// normalizer as every binding table, so opt+f12 and option+f12 match the
// alt+f12 a terminal sends. An empty leader means the default.
func IsLeaderPress(pressed, leader string) bool {
	if leader == "" {
		leader = DefaultLeaderKey
	}
	set := leaderCache.Load()
	if set == nil || set.leader != leader || set.macOS != macOSHost {
		set = &leaderSet{leader: leader, macOS: macOSHost, keys: map[string]bool{}}
		for _, k := range (&KeyNormalizer{isMacOS: macOSHost}).NormalizeKey(leader) {
			set.keys[lookupForm(k)] = true
		}
		leaderCache.Store(set)
	}
	// A key event is already spelled the canonical way, so it is compared as
	// it is and a miss costs one map read with no allocation. A caller that
	// builds the string itself passes it through CanonicalKey first.
	return set.keys[pressed]
}
