package config

import (
	"os"
	"runtime"
	"strings"
	"unicode"
	"unicode/utf8"
)

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

// detectMacOS checks if the current platform is macOS
func detectMacOS() bool {
	// Check GOOS first (most reliable)
	if runtime.GOOS == "darwin" {
		return true
	}
	// Fallback to environment variables
	goos := strings.ToLower(os.Getenv("GOOS"))
	ostype := strings.ToLower(os.Getenv("OSTYPE"))
	return strings.Contains(goos, "darwin") || strings.Contains(ostype, "darwin")
}

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
// macOptionAdvice exists to explain.
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

// macOptionLetterGlyph returns the composed glyph for an "opt+x"/"alt+x" chord
// spelled in lower case, or "" when the chord is not an Option+letter one.
func macOptionLetterGlyph(keyLower string) string {
	base, ok := cutOptionPrefix(keyLower)
	if !ok {
		return ""
	}
	if shifted, ok := strings.CutPrefix(base, "shift+"); ok {
		return macOptionShiftLetters[shifted]
	}
	return macOptionLetters[base]
}

// cutOptionPrefix strips a leading alt+/opt+/option+ and reports whether one was
// there. Only that single modifier counts: ctrl+alt+n is not a chord macOS
// composes a character for.
func cutOptionPrefix(keyLower string) (string, bool) {
	for _, prefix := range []string{"alt+", "opt+", "option+"} {
		if base, ok := strings.CutPrefix(keyLower, prefix); ok {
			return base, true
		}
	}
	return "", false
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
func shiftAliases(key, keyLower string) []string {
	if after, ok := strings.CutPrefix(keyLower, "shift+"); ok {
		base := after
		if symbol, ok := shiftedDigits[base]; ok {
			return []string{symbol}
		}
		if isSingleRuneLetter(base) {
			return []string{strings.ToUpper(base)}
		}
		return nil
	}
	if digit, ok := shiftedDigitsReverse[key]; ok {
		return []string{"shift+" + digit}
	}
	// An uppercase letter is the shifted spelling of its lowercase self.
	if isSingleRuneLetter(key) && key != keyLower {
		return []string{"shift+" + keyLower}
	}
	return modifiedShiftAliases(keyLower)
}

// modifiedShiftAliases does for a chord carrying other modifiers what
// shiftAliases does for a bare one: "alt+shift+1" also answers to "alt+!"
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

	// Accept both spellings of a shifted key, on every platform.
	result = append(result, shiftAliases(key, keyLower)...)

	// On macOS, expand opt+N and option+N to unicode and alt+N
	if kn.isMacOS {
		// Check for opt+shift+number combinations first
		if unicode, ok := macOptionShiftNumberMap[keyLower]; ok {
			// Add the unicode character
			result = append(result, strings.ToLower(unicode))
			// Also map to alt+shift+N (use replacer for efficiency)
			result = append(result, optionToAltReplacer.Replace(keyLower))
		} else if unicode, ok := macOptionNumberMap[keyLower]; ok {
			// Add the unicode character
			result = append(result, strings.ToLower(unicode))
			// Also map to alt+N
			result = append(result, optionToAltReplacer.Replace(keyLower))
		} else if unicode, ok := macOptionTabMap[keyLower]; ok {
			// Add the unicode character for opt+tab variants
			result = append(result, unicode)
			// Also map to alt+tab variant
			result = append(result, optionToAltReplacer.Replace(keyLower))
		} else if glyph := macOptionLetterGlyph(keyLower); glyph != "" {
			// Case is preserved: å (opt+a) and Å (opt+shift+a) are different keys.
			result = append(result, glyph)
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

	// On non-macOS systems, error on opt/option keys
	if !kn.isMacOS {
		if strings.Contains(keyLower, "opt+") || strings.Contains(keyLower, "option+") {
			return false, "opt/option keys are only valid on macOS, use alt+ instead"
		}
	}

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

		// Valid modifiers (only those that work reliably in terminals)
		validModifiers := map[string]bool{
			"ctrl":  true,
			"alt":   true,
			"shift": true,
			// Super only reaches a terminal that has negotiated the Kitty
			// keyboard protocol, but the input path has always acted on super+v
			// and super+shift+v for the host paste. Rejecting it here would make
			// the working default unwritable the moment it became a binding.
			"super":  true,
			"opt":    kn.isMacOS, // opt only valid on macOS
			"option": kn.isMacOS, // option only valid on macOS
		}

		// Check each modifier
		for _, mod := range modifiers {
			if !validModifiers[mod] {
				if mod == "opt" || mod == "option" {
					return false, "opt/option modifiers are only valid on macOS"
				}
				return false, "invalid modifier: " + mod
			}
		}

		// Check for duplicate modifiers
		modSet := make(map[string]bool)
		for _, mod := range modifiers {
			if modSet[mod] {
				return false, "duplicate modifier: " + mod
			}
			modSet[mod] = true
		}
	}

	// Valid special keys. The left/right modifier names are keys in their own
	// right only under the Kitty keyboard protocol; they are accepted so a
	// held-key binding (hold_window_mode) can name one.
	validSpecialKeys := map[string]bool{
		"leftalt": true, "rightalt": true, "leftctrl": true, "rightctrl": true,
		"leftshift": true, "rightshift": true, "leftsuper": true, "rightsuper": true,
		"leftmeta": true, "rightmeta": true, "lefthyper": true, "righthyper": true,
		"enter": true, "return": true, "esc": true, "escape": true,
		"tab": true, "space": true, "backspace": true, "delete": true,
		"up": true, "down": true, "left": true, "right": true,
		"home": true, "end": true, "pgup": true, "pageup": true,
		"pgdown": true, "pagedown": true,
		"f1": true, "f2": true, "f3": true, "f4": true,
		"f5": true, "f6": true, "f7": true, "f8": true,
		"f9": true, "f10": true, "f11": true, "f12": true,
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
	if !validSpecialKeys[actualKey] {
		return false, "unknown special key: " + actualKey
	}

	return true, ""
}
