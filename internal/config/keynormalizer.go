package config

import (
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"unicode"
	"unicode/utf8"
)

const maxFunctionKey = 63

func isFunctionKeyName(name string) bool {
	digits, ok := strings.CutPrefix(name, "f")
	if !ok || digits == "" || digits[0] == '0' {
		return false
	}
	n, err := strconv.Atoi(digits)
	return err == nil && n >= 1 && n <= maxFunctionKey
}

func isSingleRuneLetter(s string) bool {
	if utf8.RuneCountInString(s) != 1 {
		return false
	}
	r, _ := utf8.DecodeRuneInString(s)
	return unicode.IsLetter(r)
}

// Modifier names, as Bubble Tea writes them in a key event's string.
const (
	ModCtrl  = "ctrl"
	ModAlt   = "alt"
	ModShift = "shift"
	ModSuper = "super"
	ModHyper = "hyper"
)

var modifierSpellings = map[string]string{
	"ctrl": ModCtrl, "control": ModCtrl, "ctl": ModCtrl,
	"alt": ModAlt, "opt": ModAlt, "option": ModAlt, "meta": ModAlt,
	"shift": ModShift,
	"super": ModSuper, "cmd": ModSuper, "command": ModSuper, "win": ModSuper,
	"hyper": ModHyper,
}

// ParseModifier reads one spelling of a modifier, in any case, and returns
// its name: ModCtrl, ModAlt, ModShift, ModSuper or ModHyper. ok is false for a
// word that is not a modifier.
func ParseModifier(word string) (name string, ok bool) {
	name, ok = modifierSpellings[strings.ToLower(strings.TrimSpace(word))]
	return name, ok
}

// ModifierSpellings lists the spellings of the modifier name, sorted, for
// help and list-keys.
func ModifierSpellings(name string) []string {
	var out []string
	for spelling, n := range modifierSpellings {
		if n == name {
			out = append(out, spelling)
		}
	}
	slices.Sort(out)
	return out
}

var modifierOrder = []string{ModCtrl, ModAlt, ModShift, ModHyper, ModSuper}

func splitKeyChord(lower string) (mods []string, base string, ok bool) {
	if lower == "+" {
		return nil, "+", true
	}
	var rest string
	if before, cut := strings.CutSuffix(lower, "++"); cut {
		base, rest = "+", before
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
	if slices.Contains(mods, "") {
		return nil, "", false
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
		name, known := ParseModifier(m)
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

var optionToAltReplacer = strings.NewReplacer("opt+", "alt+", "option+", "alt+")

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

func detectMacOS() bool { return macOSHost }

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

var macOptionTabMap = map[string]string{
	"opt+tab": "⇥", "option+tab": "⇥",
	"opt+shift+tab": "⇤", "option+shift+tab": "⇤",
}

var macOptionLetters = map[string]string{
	"a": "å", "b": "∫", "c": "ç", "d": "∂", "e": "´", "f": "ƒ", "g": "©",
	"h": "˙", "i": "ˆ", "j": "∆", "k": "˚", "l": "¬", "m": "µ", "n": "˜",
	"o": "ø", "p": "π", "q": "œ", "r": "®", "s": "ß", "t": "†", "u": "¨",
	"v": "√", "w": "∑", "x": "≈", "y": "¥", "z": "Ω",
}

var macOptionShiftLetters = map[string]string{
	"a": "Å", "b": "ı", "c": "Ç", "d": "Î", "f": "Ï", "g": "˝",
	"h": "Ó", "j": "Ô", "k": "", "l": "Ò", "m": "Â",
	"o": "Ø", "p": "∏", "q": "Œ", "r": "‰", "s": "Í", "t": "ˇ",
	"v": "◊", "w": "„", "x": "˛", "y": "Á", "z": "¸",
}

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

var shiftedDigits = map[string]string{
	"1": "!", "2": "@", "3": "#", "4": "$", "5": "%",
	"6": "^", "7": "&", "8": "*", "9": "(", "0": ")",
}

var shiftedDigitsReverse = func() map[string]string {
	m := make(map[string]string, len(shiftedDigits))
	for digit, symbol := range shiftedDigits {
		m[symbol] = digit
	}
	return m
}()

func shiftAliases(key, keyLower string) []string {
	if out := letterShiftAliases(key, keyLower); out != nil {
		return out
	}
	return usShiftAliases(key, keyLower)
}

func letterShiftAliases(key, keyLower string) []string {
	if base, ok := strings.CutPrefix(keyLower, "shift+"); ok {
		if isSingleRuneLetter(base) {
			return []string{strings.ToUpper(base)}
		}
		return nil
	}
	if isSingleRuneLetter(key) && key != keyLower {
		return []string{"shift+" + keyLower}
	}
	return nil
}

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

const (
	usLayoutTier    = "\x00us:"
	optionGlyphTier = "\x00glyph:"
	bareGlyphTier   = "\x00bare:"
)

// USLayoutKey is the registry key that matches key only on a US layout: a
// shifted-digit alias ("alt+&" for a binding on alt+shift+7). The input path
// asks for it after every plain spelling of a key event has missed, and only
// when the event does not contradict a US layout (see KeyFitsUSLayout).
func USLayoutKey(key string) string { return usLayoutTier + key }

// OptionGlyphKey is the registry key that matches chord when it arrives as the
// character macOS composed for it, with the Alt bit set: the terminal said
// Option was held. The registry fills that tier unless
// keybindings.option_glyphs is "type" or keybindings.keyboard_layout is
// "other".
func OptionGlyphKey(chord string) string { return optionGlyphTier + chord }

// BareOptionGlyphKey is the registry key that matches chord when it arrives as
// the character macOS composed for it with no modifier at all. Nothing in such
// an event says Option was held: £ is Option and 3 on a US layout and Shift
// and 3 on a UK one (issue #746). The registry fills this tier for "bind",
// and for "auto" answers from it only on a US or ABC Mac layout.
func BareOptionGlyphKey(chord string) string { return bareGlyphTier + chord }

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
	// OptionGlyphsAuto is "bind" on a Mac whose keyboard layout is US or
	// ABC, and "alt" on any other layout or when the layout cannot be read.
	// keybindings.keyboard_layout, when set, decides instead: "us" is "bind"
	// and "other" turns the tables off. The default.
	OptionGlyphsAuto = "auto"
	// OptionGlyphsAlt reads a character composed with Option as the Option
	// binding it stands for on a US layout only when the terminal reports the
	// Alt modifier with it. A bare character is text.
	OptionGlyphsAlt = "alt"
	// OptionGlyphsType sends a character composed with Option to the pane.
	OptionGlyphsType = "type"
	// OptionGlyphsBind reads a character composed with Option as the Option
	// binding it stands for on a US layout, with or without the Alt modifier.
	// A bare character is a guess: on a UK layout £ is Shift and 3, not
	// Option and 3 (issue #746).
	OptionGlyphsBind = "bind"
)

// OptionGlyphModes is every value keybindings.option_glyphs accepts.
var OptionGlyphModes = []string{OptionGlyphsAuto, OptionGlyphsAlt, OptionGlyphsType, OptionGlyphsBind}

// OptionGlyphRuns reports whether a character macOS composes for an Option
// chord runs that chord's binding under the config. alt is whether the
// terminal reported the Alt modifier with the character. The registry fills
// the glyph tiers by the same rule.
//
// Under "auto" a bare character asks for the Mac's layout, which answers from
// a cache and never waits (see macLayoutIsUS). Call it only for a composed
// character: a stale cache starts a detection in the background.
func OptionGlyphRuns(layout, glyphs string, alt bool) bool {
	if layout == KeyboardLayoutOther || glyphs == OptionGlyphsType {
		return false
	}
	if alt {
		return true
	}
	switch glyphs {
	case OptionGlyphsBind:
		return true
	case OptionGlyphsAlt:
		return false
	}
	return bareGlyphsAuto(layout)
}

func bareGlyphsAuto(layout string) bool {
	if layout == KeyboardLayoutUS {
		return true
	}
	return macLayoutIsUS()
}

// NormalizeKey converts a key string to its canonical form for the current platform
// For example, on macOS: "opt+1" → "¡" or "alt+1" depending on context
func (kn *KeyNormalizer) NormalizeKey(key string) []string {
	key = strings.TrimSpace(key)

	var normalized string
	if isSingleRuneLetter(key) {
		normalized = key
	} else {
		normalized = strings.ToLower(key)
	}

	keyLower := strings.ToLower(key)

	result := []string{normalized}

	canonical := CanonicalKey(key)
	result = append(result, canonical)

	result = append(result, letterShiftAliases(key, keyLower)...)
	result = append(result, letterShiftAliases(canonical, strings.ToLower(canonical))...)

	if kn.isMacOS {
		if strings.HasPrefix(keyLower, "opt+") || strings.HasPrefix(keyLower, "option+") {
			result = append(result, optionToAltReplacer.Replace(keyLower))
		}

		if strings.HasPrefix(keyLower, "alt+") {
			result = append(result, altToOptReplacer.Replace(keyLower))
			result = append(result, altToOptionReplacer.Replace(keyLower))
		}
	}

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

	if keyLower == "" {
		return false, "key cannot be empty"
	}

	if keyLower == "+" {
		return true, ""
	}
	parts := strings.Split(keyLower, "+")
	if len(parts) > 1 {
		modifiers := parts[:len(parts)-1]
		actualKey := parts[len(parts)-1]

		if actualKey == "" {
			return false, "key combination incomplete (ends with +)"
		}

		for _, mod := range modifiers {
			if !validModifier(mod) {
				return false, "invalid modifier: " + mod
			}
		}

		modSet := make(map[string]bool)
		for _, mod := range modifiers {
			name, _ := ParseModifier(mod)
			if modSet[name] {
				return false, "duplicate modifier: " + mod
			}
			modSet[name] = true
		}
	}

	parts = strings.Split(keyLower, "+")
	actualKey := parts[len(parts)-1]

	if utf8.RuneCountInString(actualKey) == 1 {
		return true, ""
	}

	if !validSpecialKeys[actualKey] && !isFunctionKeyName(actualKey) {
		return false, "unknown special key: " + actualKey
	}

	return true, ""
}

// ValidateSectionKey is ValidateKey for a key in the binding table section.
// It also accepts the middle mouse button, which Bubble Tea spells "middle",
// in terminal_mode only, and with ctrl, alt and shift only: no other table
// looks a mouse press up, and Bubble Tea's mouse events carry no other
// modifier. The left and right buttons are not accepted, because "left" and
// "right" are the arrow keys.
func (kn *KeyNormalizer) ValidateSectionKey(section, key string) (bool, string) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(key)), "+")
	if parts[len(parts)-1] != middleButton {
		return kn.ValidateKey(key)
	}
	if section != SectionTerminalMode {
		return false, "the middle mouse button is a key only in [keybindings.terminal_mode]"
	}
	seen := make(map[string]bool)
	for _, mod := range parts[:len(parts)-1] {
		name, _ := ParseModifier(mod)
		switch name {
		case "ctrl", "alt", "shift":
		default:
			return false, "the middle mouse button takes only ctrl, alt and shift, not " + mod
		}
		if seen[name] {
			return false, "duplicate modifier: " + mod
		}
		seen[name] = true
	}
	return true, ""
}

const middleButton = "middle"

var validSpecialKeys = map[string]bool{
	"leftalt": true, "rightalt": true, "leftctrl": true, "rightctrl": true,
	"leftshift": true, "rightshift": true, "leftsuper": true, "rightsuper": true,
	"leftmeta": true, "rightmeta": true, "lefthyper": true, "righthyper": true,
	"enter": true, "return": true, "esc": true, "escape": true,
	"tab": true, "space": true, "backspace": true, "delete": true,
	"up": true, "down": true, "left": true, "right": true,
	"home": true, "end": true, "pgup": true, "pageup": true,
	"pgdown": true, "pagedown": true, "insert": true,
	"capslock": true, "scrolllock": true, "numlock": true,
	"printscreen": true, "pause": true, "menu": true,
}

func validModifier(mod string) bool {
	name, ok := ParseModifier(mod)
	return ok && name != ModHyper
}

type leaderSet struct {
	leader string
	macOS  bool
	keys   map[string]bool
}

var leaderCache atomic.Pointer[leaderSet]

// KeyTypes reports whether a key from the config is one programs type: a
// printable character, with shift or without, or space, enter, tab, backspace
// or esc.
func KeyTypes(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	k = strings.TrimPrefix(k, "shift+")
	if utf8.RuneCountInString(k) == 1 {
		return unicode.IsPrint([]rune(k)[0])
	}
	switch k {
	case "space", "enter", "return", "tab", "backspace", "esc", "escape",
		"ctrl+i", "ctrl+m", "ctrl+[", "ctrl+h":
		return true
	}
	return false
}

// IsKeyPress reports whether pressed, a key event's string in the canonical
// form, is key as config.toml spells it, normalized as every binding table
// is. It normalizes key on each call, so it is for a key read only inside a
// mode of its own, as keybindings.nest_exit_key is in nest mode; the leader,
// read on every press, has IsLeaderPress and its cache.
func IsKeyPress(pressed, key string) bool {
	for _, k := range (&KeyNormalizer{isMacOS: macOSHost}).NormalizeKey(key) {
		if lookupForm(k) == pressed {
			return true
		}
	}
	return false
}

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
	return set.keys[pressed]
}
