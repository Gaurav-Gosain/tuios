package session

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
	"github.com/charmbracelet/x/ansi"
)

// This file turns a send-keys argument into keys. One parser serves both
// routes a send-keys call can take: the bytes written to a window's terminal,
// and the canonical key names handed to an attached client when the keys are
// meant for the window manager. Keeping one parser means a spelling that works
// on one route works on the other, and a spelling that works on neither fails
// before anything is sent, on both.

// maxSendKeysRepeat bounds the repeat param. It is a guard against a typo such
// as 10000 for 100, not a limit anyone driving a pager should meet.
const maxSendKeysRepeat = 1000

// keyMods is the set of modifiers on one key token.
type keyMods struct {
	ctrl, alt, shift bool
	// super has no terminal encoding. It is kept so the window manager, which
	// can bind it, still gets it; writing it to a pane is an error.
	super bool
}

// xterm is the modifier parameter xterm puts in a modified cursor or function
// key sequence: 1 plus shift 1, alt 2 and ctrl 4.
func (m keyMods) xterm() int {
	n := 1
	if m.shift {
		n++
	}
	if m.alt {
		n += 2
	}
	if m.ctrl {
		n += 4
	}
	return n
}

// prefix is how the modifiers read in a canonical name, in a fixed order.
func (m keyMods) prefix() string {
	var b strings.Builder
	if m.ctrl {
		b.WriteString("ctrl+")
	}
	if m.alt {
		b.WriteString("alt+")
	}
	if m.shift {
		b.WriteString("shift+")
	}
	if m.super {
		b.WriteString("super+")
	}
	return b.String()
}

// namedKeyKind says how a named key is encoded.
type namedKeyKind int

const (
	// keyPlain is a key with a byte of its own: Enter, Tab, Escape.
	keyPlain namedKeyKind = iota
	// keyCursor is a key sent as CSI X, or SS3 X when the application has
	// turned on application cursor keys (DECCKM): the arrows, Home and End.
	keyCursor
	// keyTilde is a key sent as CSI n ~: Insert, Delete, the page keys, F5-F12.
	keyTilde
	// keySS3 is F1 to F4, sent as SS3 P to SS3 S.
	keySS3
)

// namedKey is one key with no character of its own.
type namedKey struct {
	name  string // canonical spelling, the one list-verbs and errors print
	kind  namedKeyKind
	plain string // keyPlain: the bytes
	final byte   // keyCursor, keySS3: the final byte
	num   int    // keyTilde: the parameter before the ~
	code  rune   // the key's code, as the pane key encoder reads it
}

// namedKeys is every key name send-keys knows, in the order errors list them.
var namedKeys = []namedKey{
	{name: "Enter", kind: keyPlain, plain: "\r", code: vt.KeyEnter},
	{name: "Tab", kind: keyPlain, plain: "\t", code: vt.KeyTab},
	{name: "BTab", kind: keyPlain, plain: "\x1b[Z", code: vt.KeyTab},
	{name: "Space", kind: keyPlain, plain: " ", code: vt.KeySpace},
	// Comma is the one character send-keys cannot take as itself, since it
	// splits keys on commas.
	{name: "Comma", kind: keyPlain, plain: ",", code: ','},
	{name: "Escape", kind: keyPlain, plain: "\x1b", code: vt.KeyEscape},
	{name: "Backspace", kind: keyPlain, plain: "\x7f", code: vt.KeyBackspace},
	{name: "Up", kind: keyCursor, final: 'A', code: vt.KeyUp},
	{name: "Down", kind: keyCursor, final: 'B', code: vt.KeyDown},
	{name: "Right", kind: keyCursor, final: 'C', code: vt.KeyRight},
	{name: "Left", kind: keyCursor, final: 'D', code: vt.KeyLeft},
	{name: "Home", kind: keyCursor, final: 'H', code: vt.KeyHome},
	{name: "End", kind: keyCursor, final: 'F', code: vt.KeyEnd},
	{name: "PageUp", kind: keyTilde, num: 5, code: vt.KeyPgUp},
	{name: "PageDown", kind: keyTilde, num: 6, code: vt.KeyPgDown},
	{name: "Insert", kind: keyTilde, num: 2, code: vt.KeyInsert},
	{name: "Delete", kind: keyTilde, num: 3, code: vt.KeyDelete},
	{name: "F1", kind: keySS3, final: 'P', code: vt.KeyF1},
	{name: "F2", kind: keySS3, final: 'Q', code: vt.KeyF2},
	{name: "F3", kind: keySS3, final: 'R', code: vt.KeyF3},
	{name: "F4", kind: keySS3, final: 'S', code: vt.KeyF4},
	{name: "F5", kind: keyTilde, num: 15, code: vt.KeyF5},
	{name: "F6", kind: keyTilde, num: 17, code: vt.KeyF6},
	{name: "F7", kind: keyTilde, num: 18, code: vt.KeyF7},
	{name: "F8", kind: keyTilde, num: 19, code: vt.KeyF8},
	{name: "F9", kind: keyTilde, num: 20, code: vt.KeyF9},
	{name: "F10", kind: keyTilde, num: 21, code: vt.KeyF10},
	{name: "F11", kind: keyTilde, num: 23, code: vt.KeyF11},
	{name: "F12", kind: keyTilde, num: 24, code: vt.KeyF12},
}

// keyAliases maps a normalized spelling (see normalizeKeyName) to the
// canonical name. It covers the names people carry over from tmux, curses,
// vim, xdotool and the DOM, so an agent's first guess works.
var keyAliases = func() map[string]string {
	m := map[string]string{
		"return": "Enter", "ret": "Enter", "cr": "Enter", "kpenter": "Enter",
		"backtab": "BTab", "stab": "BTab",
		"spc":    "Space",
		"esc":    "Escape",
		"bspace": "Backspace", "bs": "Backspace", "bksp": "Backspace",
		"del": "Delete", "dc": "Delete",
		"ins": "Insert", "ic": "Insert",
		"pgup": "PageUp", "ppage": "PageUp", "prior": "PageUp", "prevpage": "PageUp",
		"pgdn": "PageDown", "pgdown": "PageDown", "npage": "PageDown", "next": "PageDown", "nextpage": "PageDown",
		"pagedn": "PageDown",
	}
	for _, k := range namedKeys {
		m[strings.ToLower(k.name)] = k.name
	}
	return m
}()

// namedKeyByName is namedKeys by canonical name.
var namedKeyByName = func() map[string]namedKey {
	m := make(map[string]namedKey, len(namedKeys))
	for _, k := range namedKeys {
		m[k.name] = k
	}
	return m
}()

// KeyNames lists the canonical key names send-keys accepts, for errors, help
// and the skill.
func KeyNames() []string {
	names := make([]string, 0, len(namedKeys))
	for _, k := range namedKeys {
		names = append(names, k.name)
	}
	return names
}

// normalizeKeyName reduces a spelling of a key name to the form keyAliases is
// keyed by: lower case, with no separators, and with the decoration other
// tools put around a name removed: <Up> (vim), KEY_UP (curses), ArrowUp (the
// DOM), arrow-up and up-arrow. keyish reports whether any decoration was
// there, which marks the token as meant to be a key name even when the rest of
// it is not one.
func normalizeKeyName(tok string) (norm string, keyish bool) {
	s := strings.ToLower(tok)
	if len(s) > 2 && strings.HasPrefix(s, "<") && strings.HasSuffix(s, ">") {
		s = s[1 : len(s)-1]
		keyish = true
	}
	s = strings.NewReplacer("-", "", "_", "", " ", "").Replace(s)
	if len(s) > 3 && strings.HasPrefix(s, "key") {
		s = s[3:]
		keyish = true
	}
	if len(s) > 5 && strings.HasPrefix(s, "arrow") {
		s = s[5:]
		keyish = true
	} else if len(s) > 5 && strings.HasSuffix(s, "arrow") {
		s = s[:len(s)-5]
		keyish = true
	}
	return s, keyish
}

// sendKey is one parsed send-keys token.
type sendKey struct {
	// token is what the caller wrote, for errors.
	token string
	// canonical is the key as the attached client's parser reads it, or empty
	// for a token the client cannot take (an escape sequence).
	canonical string
	// prefix is the PREFIX token: the configured leader key, which only the
	// attached client knows.
	prefix bool
	// named is set for a named key; bytes depend on the pane's modes.
	named *namedKey
	mods  keyMods
	// text is the bytes of a character, a word typed as text, or an escape
	// sequence.
	text []byte
}

// bytes is the key as the terminal of a pane receives it. appCursor is the
// pane's application cursor keys mode (DECCKM), which changes the arrows,
// Home and End from CSI to SS3, the way a real terminal does.
func (k sendKey) bytes(appCursor bool) []byte {
	if k.named == nil {
		return k.text
	}
	nk := k.named
	mod := k.mods.xterm()
	switch nk.kind {
	case keyCursor:
		if mod > 1 {
			return fmt.Appendf(nil, "\x1b[1;%d%c", mod, nk.final)
		}
		if appCursor {
			return []byte{0x1b, 'O', nk.final}
		}
		return []byte{0x1b, '[', nk.final}
	case keySS3:
		if mod > 1 {
			return fmt.Appendf(nil, "\x1b[1;%d%c", mod, nk.final)
		}
		return []byte{0x1b, 'O', nk.final}
	case keyTilde:
		if mod > 1 {
			return fmt.Appendf(nil, "\x1b[%d;%d~", nk.num, mod)
		}
		return fmt.Appendf(nil, "\x1b[%d~", nk.num)
	}
	// A plain key. shift+Tab is the back tab; ctrl turns Space into NUL and
	// Backspace into BS; alt puts ESC in front, as xterm does with metaSendsEscape.
	out := nk.plain
	switch {
	case nk.name == "Tab" && k.mods.shift:
		out = "\x1b[Z"
	case nk.name == "Space" && k.mods.ctrl:
		out = "\x00"
	case nk.name == "Backspace" && k.mods.ctrl:
		out = "\x08"
	}
	if k.mods.alt {
		return append([]byte{0x1b}, out...)
	}
	return []byte(out)
}

// paneKeyModes is the keyboard mode a pane asked for, as the daemon's emulator
// last read it. A named key or a modified character is encoded for it the way
// the client encodes a key a person types.
type paneKeyModes struct {
	// appCursor is application cursor keys (DECSET 1, DECCKM).
	appCursor bool
	// kittyFlags is the pane's kitty keyboard flags.
	kittyFlags int
	// modifyOtherKeys is the pane's XTMODKEYS level: 0 off, 1 or 2.
	modifyOtherKeys int
}

// vtKey is the key as a key press event for the pane key encoder. ok is false
// for a token that is typed as text: a character with no modifier, a word or
// an escape sequence. Those reach the pane as their bytes in every mode.
func (k sendKey) vtKey() (key vt.KeyPressEvent, ok bool) {
	var mod vt.KeyMod
	if k.mods.ctrl {
		mod |= vt.ModCtrl
	}
	if k.mods.alt {
		mod |= vt.ModAlt
	}
	if k.mods.shift {
		mod |= vt.ModShift
	}
	if k.named != nil {
		key = vt.KeyPressEvent{Code: k.named.code, Mod: mod}
		if k.named.name == "BTab" {
			key.Mod |= vt.ModShift
		}
		if key.Mod == 0 && (k.named.code == vt.KeySpace || k.named.code == ',') {
			key.Text = string(k.named.code)
		}
		return key, true
	}
	if mod == 0 {
		return key, false
	}
	// A character with modifiers: withMods kept the token's key after them.
	r, size := utf8.DecodeRuneInString(k.canonical[len(k.mods.prefix()):])
	if size == 0 || r == utf8.RuneError {
		return key, false
	}
	key = vt.KeyPressEvent{Code: unicode.ToLower(r), Mod: mod}
	if k.mods.shift {
		key.ShiftedCode = unicode.ToUpper(r)
		if !k.mods.ctrl && !k.mods.alt {
			// Shift alone types the shifted character, as a host reports it.
			key.Text = string(key.ShiftedCode)
		}
	}
	return key, true
}

// encode is the key as the terminal of a pane in modes receives it. A pane that
// asked for the kitty keyboard protocol or modifyOtherKeys gets the encoding it
// asked for, from the encoder the client uses for a key a person types. Without
// one, Vim in kitty mode read ctrl+h sent as 0x08 as Backspace. A pane that
// asked for event types also gets the release, since no release follows a sent
// key.
func (k sendKey) encode(modes paneKeyModes) []byte {
	key, ok := k.vtKey()
	if !ok {
		return k.bytes(modes.appCursor)
	}
	out := []byte(vt.EncodePaneKey(key, modes.kittyFlags, modes.modifyOtherKeys))
	if len(out) == 0 {
		out = k.bytes(modes.appCursor)
	}
	if modes.kittyFlags&ansi.KittyReportEventTypes != 0 {
		out = append(out, vt.EncodeKeyReleaseCSIu(key, modes.kittyFlags)...)
	}
	return out
}

// errUnknownKey is a token that looks like a key name and is not one.
type errUnknownKey struct {
	token      string
	didYouMean string
}

func (e errUnknownKey) Error() string {
	msg := fmt.Sprintf("unknown key %q", e.token)
	if e.didYouMean != "" {
		msg += fmt.Sprintf(" (did you mean %s?)", e.didYouMean)
	}
	return msg + ". Nothing was sent"
}

// parseSendKeys splits a send-keys argument on spaces and commas and parses
// each token. repeat sends the whole sequence that many times; 0 means once.
func parseSendKeys(keys string, repeat int) ([]sendKey, error) {
	if repeat < 0 || repeat > maxSendKeysRepeat {
		return nil, fmt.Errorf("repeat must be between 1 and %d, got %d", maxSendKeysRepeat, repeat)
	}
	if repeat == 0 {
		repeat = 1
	}
	tokens := strings.Fields(strings.ReplaceAll(keys, ",", " "))
	if len(tokens) == 0 {
		return nil, fmt.Errorf("no valid keys in sequence: %s", keys)
	}
	one := make([]sendKey, 0, len(tokens))
	for _, tok := range tokens {
		k, err := parseKeyToken(tok)
		if err != nil {
			return nil, err
		}
		one = append(one, k)
	}
	out := make([]sendKey, 0, len(one)*repeat)
	for range repeat {
		out = append(out, one...)
	}
	return out, nil
}

// parseKeyToken parses one send-keys token.
func parseKeyToken(tok string) (sendKey, error) {
	k := sendKey{token: tok}

	if tok == "PREFIX" || tok == "$PREFIX" || strings.EqualFold(tok, "prefix") || strings.EqualFold(tok, "$prefix") {
		k.prefix = true
		k.canonical = "PREFIX"
		return k, nil
	}

	// An escape sequence, written with its ESC byte, as \e, \x1b or \033, or
	// as ^[. It goes to the pane as it is.
	if b, ok, err := decodeEscapeToken(tok); ok || err != nil {
		if err != nil {
			return k, err
		}
		k.text = b
		return k, nil
	}

	// One character is that character.
	if utf8.RuneCountInString(tok) == 1 {
		k.text = []byte(tok)
		k.canonical = tok
		return k, nil
	}

	// ^C is ctrl+c, the way terminals print it.
	if len(tok) == 2 && tok[0] == '^' {
		return withMods(k, keyMods{ctrl: true}, tok[1:])
	}

	mods, rest, hasMods, err := splitKeyMods(tok)
	if err != nil {
		return k, err
	}
	if hasMods {
		return withMods(k, mods, rest)
	}

	norm, keyish := normalizeKeyName(tok)
	if name, ok := keyAliases[norm]; ok {
		nk := namedKeyByName[name]
		k.named = &nk
		k.canonical = name
		if name == "BTab" {
			k.canonical = "shift+Tab"
		}
		return k, nil
	}
	// A function key past F12 (F13 to F20) is not in the table; say so rather
	// than typing "F13".
	if keyish || looksLikeKeyName(tok, norm) {
		return k, errUnknownKey{token: tok, didYouMean: suggestKeyName(norm)}
	}
	// Any other word is typed as its characters, which is what send-keys has
	// always done with "ls,Enter".
	k.text = []byte(tok)
	k.canonical = tok
	return k, nil
}

// withMods finishes a token that carries modifiers on key, which is a single
// character or a key name.
func withMods(k sendKey, mods keyMods, key string) (sendKey, error) {
	k.mods = mods
	if utf8.RuneCountInString(key) == 1 {
		r, _ := utf8.DecodeRuneInString(key)
		b, err := modifiedChar(r, mods, k.token)
		if err != nil {
			return k, err
		}
		k.text = b
		k.canonical = mods.prefix() + strings.ToLower(key)
		if mods.shift && !mods.ctrl && !mods.alt {
			k.canonical = mods.prefix() + key
		}
		return k, nil
	}
	norm, _ := normalizeKeyName(key)
	name, ok := keyAliases[norm]
	if !ok {
		return k, errUnknownKey{token: k.token, didYouMean: suggestKeyName(norm)}
	}
	nk := namedKeyByName[name]
	if name == "BTab" {
		// BTab is shift+Tab already.
		nk = namedKeyByName["Tab"]
		k.mods.shift = true
	}
	k.named = &nk
	k.canonical = k.mods.prefix() + nk.name
	return k, nil
}

// modifiedChar encodes a character with modifiers the way xterm does for a
// terminal with no keyboard protocol: ctrl makes a control byte, shift an
// upper-case letter, alt a leading ESC.
func modifiedChar(r rune, mods keyMods, tok string) ([]byte, error) {
	out := string(r)
	if mods.shift {
		out = string(unicode.ToUpper(r))
	}
	if mods.ctrl {
		c, ok := controlByte(r)
		if !ok {
			return nil, fmt.Errorf("unsupported ctrl combination %q: ctrl works on a letter or one of @ [ \\ ] ^ _ ? and space", tok)
		}
		out = string([]byte{c})
	}
	if mods.alt {
		return append([]byte{0x1b}, out...), nil
	}
	return []byte(out), nil
}

// controlByte is the byte ctrl plus a character sends.
func controlByte(r rune) (byte, bool) {
	switch {
	case r >= 'a' && r <= 'z':
		return byte(r) & 0x1f, true
	case r >= 'A' && r <= 'Z':
		return byte(r) & 0x1f, true
	case r == '@' || r == ' ' || r == '2':
		return 0x00, true
	case r == '[':
		return 0x1b, true
	case r == '\\':
		return 0x1c, true
	case r == ']':
		return 0x1d, true
	case r == '^' || r == '6':
		return 0x1e, true
	case r == '_' || r == '-':
		return 0x1f, true
	case r == '?':
		return 0x7f, true
	}
	return 0, false
}

// splitKeyMods takes the modifiers off a token: ctrl+alt+x, and tmux's C-x,
// M-x and S-x. hasMods is false for a token with no modifier, whose rest is
// the whole token.
func splitKeyMods(tok string) (mods keyMods, rest string, hasMods bool, err error) {
	rest = tok
	// tmux style: C-, M-, S- in front, as many as there are.
	for len(rest) > 2 && rest[1] == '-' {
		switch rest[0] {
		case 'C':
			mods.ctrl = true
		case 'M':
			mods.alt = true
		case 'S':
			mods.shift = true
		default:
			return keyMods{}, tok, false, nil
		}
		rest = rest[2:]
		hasMods = true
	}
	if hasMods {
		return mods, rest, true, nil
	}
	if !strings.Contains(tok, "+") || len(tok) < 2 {
		return keyMods{}, tok, false, nil
	}
	parts := strings.Split(tok, "+")
	// "ctrl++" is ctrl and the plus key.
	if strings.HasSuffix(tok, "++") {
		parts = append(parts[:len(parts)-2], "+")
	}
	key := ""
	for _, p := range parts {
		switch strings.ToLower(strings.TrimSpace(p)) {
		case "ctrl", "control", "ctl":
			mods.ctrl = true
		case "alt", "opt", "option", "meta":
			mods.alt = true
		case "shift":
			mods.shift = true
		case "super", "cmd", "win":
			mods.super = true
		default:
			if key != "" {
				return keyMods{}, tok, false, errUnknownKey{token: tok}
			}
			key = p
		}
	}
	if key == "" {
		return keyMods{}, tok, false, fmt.Errorf("unsupported ctrl combination %q: a modifier needs a key after it, as in ctrl+c", tok)
	}
	return mods, key, true, nil
}

// looksLikeKeyName reports whether a word that is not a key name was probably
// meant as one: it starts with a capital letter and is a short edit away from
// a key name ("Dwon", "Pagedown" is already an alias), or it is a function key
// out of range. A lower-case word such as "ls" is typed as text, as it always
// was.
func looksLikeKeyName(tok, norm string) bool {
	if len(norm) >= 2 && norm[0] == 'f' {
		if _, err := strconv.Atoi(norm[1:]); err == nil {
			return true
		}
	}
	r, _ := utf8.DecodeRuneInString(tok)
	if !unicode.IsUpper(r) || len(norm) < 3 {
		return false
	}
	return suggestKeyName(norm) != ""
}

// suggestKeyName is the key name closest to a normalized spelling, or empty.
func suggestKeyName(norm string) string {
	if norm == "" {
		return ""
	}
	names := make([]string, 0, len(keyAliases))
	for alias := range keyAliases {
		names = append(names, alias)
	}
	best := closestMatch(norm, names)
	if best == "" {
		// A swap of two letters (Dwon) is two edits for Levenshtein.
		for alias := range keyAliases {
			if isTransposition(norm, alias) {
				best = alias
				break
			}
		}
	}
	if best == "" {
		return ""
	}
	return keyAliases[best]
}

// isTransposition reports whether a and b differ by one swap of neighbours.
func isTransposition(a, b string) bool {
	if len(a) != len(b) || a == b {
		return false
	}
	i := 0
	for i < len(a) && a[i] == b[i] {
		i++
	}
	return i+1 < len(a) && a[i] == b[i+1] && a[i+1] == b[i] && a[i+2:] == b[i+2:]
}

// decodeEscapeToken decodes a token written as an escape sequence: one that
// starts with a real ESC byte, with \e, \E, \x1b, \033, \u001b, or with ^[.
// ok is false for a token that is not one. The rest of the token may use \e,
// \xHH, \NNN octal, \n, \r, \t and \\.
func decodeEscapeToken(tok string) (b []byte, ok bool, err error) {
	switch {
	case strings.HasPrefix(tok, "\x1b"):
		return []byte(tok), true, nil
	case strings.HasPrefix(tok, "^["):
		tok = "\\e" + tok[2:]
	case strings.HasPrefix(tok, `\e`), strings.HasPrefix(tok, `\E`),
		strings.HasPrefix(tok, `\x1b`), strings.HasPrefix(tok, `\x1B`),
		strings.HasPrefix(tok, `\033`), strings.HasPrefix(tok, `\u001b`), strings.HasPrefix(tok, `\u001B`):
	default:
		return nil, false, nil
	}
	var out []byte
	for i := 0; i < len(tok); i++ {
		c := tok[i]
		if c != '\\' || i+1 >= len(tok) {
			out = append(out, c)
			continue
		}
		i++
		switch tok[i] {
		case 'e', 'E':
			out = append(out, 0x1b)
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'a':
			out = append(out, 0x07)
		case '\\':
			out = append(out, '\\')
		case 'x':
			if i+2 >= len(tok) {
				return nil, true, fmt.Errorf("bad escape in %q: \\x needs two hex digits", tok)
			}
			v, perr := strconv.ParseUint(tok[i+1:i+3], 16, 8)
			if perr != nil {
				return nil, true, fmt.Errorf("bad escape in %q: \\x needs two hex digits", tok)
			}
			out = append(out, byte(v))
			i += 2
		case 'u':
			if i+4 >= len(tok) {
				return nil, true, fmt.Errorf("bad escape in %q: \\u needs four hex digits", tok)
			}
			v, perr := strconv.ParseUint(tok[i+1:i+5], 16, 32)
			if perr != nil {
				return nil, true, fmt.Errorf("bad escape in %q: \\u needs four hex digits", tok)
			}
			out = utf8.AppendRune(out, rune(v))
			i += 4
		case '0', '1', '2', '3':
			j := i
			for j < len(tok) && j < i+3 && tok[j] >= '0' && tok[j] <= '7' {
				j++
			}
			v, perr := strconv.ParseUint(tok[i:j], 8, 8)
			if perr != nil {
				return nil, true, fmt.Errorf("bad escape in %q: %v", tok, perr)
			}
			out = append(out, byte(v))
			i = j - 1
		default:
			out = append(out, '\\', tok[i])
		}
	}
	return out, true, nil
}

// sendKeysBytes is the bytes a parsed sequence writes to a pane in modes. The
// prefix has no bytes: only an attached client knows the leader key.
func sendKeysBytes(keys []sendKey, modes paneKeyModes) ([]byte, error) {
	var out []byte
	for _, k := range keys {
		if k.prefix {
			return nil, fmt.Errorf("the prefix key only works with an attached client. Attach one and retry")
		}
		if k.mods.super {
			return nil, fmt.Errorf("unsupported modifier in %q: a terminal has no encoding for super. Use ctrl, alt or shift", k.token)
		}
		out = append(out, k.encode(modes)...)
	}
	return out, nil
}

// sendKeysCanonical is a parsed sequence as the attached client's key parser
// reads it. An escape sequence has no key there, so it is an error naming the
// route that takes it.
func sendKeysCanonical(keys []sendKey) (string, error) {
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		if k.canonical == "" {
			return "", fmt.Errorf("the escape sequence %q is not a key the window manager reads. Pass a window to write it to that window's terminal", k.token)
		}
		parts = append(parts, k.canonical)
	}
	return strings.Join(parts, " "), nil
}

// hasPrefixKey reports whether a sequence holds the PREFIX token.
func hasPrefixKey(keys []sendKey) bool {
	for _, k := range keys {
		if k.prefix {
			return true
		}
	}
	return false
}

// keyModes is the keyboard mode the application in the pane asked for, as the
// daemon's emulator last read it: application cursor keys (less and vim turn
// it on), the kitty keyboard flags and modifyOtherKeys. A key sent to the pane
// has to match it.
func (p *PTY) keyModes() paneKeyModes {
	p.terminalMu.RLock()
	defer p.terminalMu.RUnlock()
	if p.terminal == nil {
		return paneKeyModes{}
	}
	return paneKeyModes{
		appCursor:       p.terminal.ApplicationCursorKeys(),
		kittyFlags:      p.terminal.KittyKeyboardFlags(),
		modifyOtherKeys: p.terminal.ModifyOtherKeys(),
	}
}

// keyModifiers are the modifiers a send-keys token can carry, each with every
// spelling the parser takes (splitKeyMods and parseKeyToken). list-keys
// reports them.
var keyModifiers = []struct {
	Name      string   `json:"name"`
	Spellings []string `json:"spellings"`
}{
	{"ctrl", []string{"ctrl+", "control+", "ctl+", "C-", "^"}},
	{"alt", []string{"alt+", "opt+", "option+", "meta+", "M-"}},
	{"shift", []string{"shift+", "S-"}},
	{"super", []string{"super+", "cmd+", "win+"}},
}

// ctrlCharacters are the characters other than letters that ctrl combines
// with (controlByte).
var ctrlCharacters = []string{"@", "[", `\`, "]", "^", "_", "?", "space", "2", "6", "-"}

// escapePrefixes start a token written as an escape sequence
// (decodeEscapeToken).
var escapePrefixes = []string{`\e`, `\E`, `\x1b`, `\033`, `\u001b`, "^[", "ESC byte"}

// keyList is the whole send-keys grammar as list-keys reports it: every
// named key with the spellings that reach it, the modifiers, and the other
// kinds of token. It is a closed list. A named key or modifier missing here
// is refused.
func keyList() map[string]any {
	aliases := map[string][]string{}
	for alias, name := range keyAliases {
		if alias != strings.ToLower(name) {
			aliases[name] = append(aliases[name], alias)
		}
	}
	keys := make([]map[string]any, 0, len(namedKeys))
	for _, k := range namedKeys {
		a := aliases[k.name]
		slices.Sort(a)
		if a == nil {
			a = []string{}
		}
		keys = append(keys, map[string]any{"name": k.name, "aliases": a})
	}
	return map[string]any{
		"type":            "key_list",
		"keys":            keys,
		"modifiers":       keyModifiers,
		"ctrl_characters": ctrlCharacters,
		"escape_prefixes": escapePrefixes,
		"prefix_token":    "PREFIX",
		"separators":      []string{" ", ","},
		"max_repeat":      maxSendKeysRepeat,
	}
}
