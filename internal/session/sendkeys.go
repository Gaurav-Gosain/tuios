package session

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/charmbracelet/x/ansi"
	"tuios.dev/vt"
)

const maxSendKeysRepeat = 1000

type keyMods struct {
	ctrl, alt, shift bool
	super            bool
}

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

type namedKeyKind int

const (
	keyPlain namedKeyKind = iota
	keyCursor
	keyTilde
	keySS3
)

type namedKey struct {
	name  string
	kind  namedKeyKind
	plain string
	final byte
	num   int
	code  rune
}

var namedKeys = []namedKey{
	{name: "Enter", kind: keyPlain, plain: "\r", code: vt.KeyEnter},
	{name: "Tab", kind: keyPlain, plain: "\t", code: vt.KeyTab},
	{name: "BTab", kind: keyPlain, plain: "\x1b[Z", code: vt.KeyTab},
	{name: "Space", kind: keyPlain, plain: " ", code: vt.KeySpace},
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

type sendKey struct {
	token     string
	canonical string
	prefix    bool
	named     *namedKey
	mods      keyMods
	text      []byte
}

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

type paneKeyModes struct {
	appCursor       bool
	kittyFlags      int
	modifyOtherKeys int
}

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
	r, size := utf8.DecodeRuneInString(k.canonical[len(k.mods.prefix()):])
	if size == 0 || r == utf8.RuneError {
		return key, false
	}
	key = vt.KeyPressEvent{Code: unicode.ToLower(r), Mod: mod}
	if k.mods.shift {
		key.ShiftedCode = unicode.ToUpper(r)
		if !k.mods.ctrl && !k.mods.alt {
			key.Text = string(key.ShiftedCode)
		}
	}
	return key, true
}

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

func parseSendKeys(keys string, repeat int) ([]sendKey, error) {
	if repeat < 0 || repeat > maxSendKeysRepeat {
		return nil, errfmt.Errorf("repeat must be between 1 and %d, got %d", maxSendKeysRepeat, repeat)
	}
	if repeat == 0 {
		repeat = 1
	}
	tokens := strings.Fields(strings.ReplaceAll(keys, ",", " "))
	if len(tokens) == 0 {
		return nil, errfmt.Errorf("no valid keys in sequence: %s", keys)
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

func parseKeyToken(tok string) (sendKey, error) {
	k := sendKey{token: tok}

	if tok == "PREFIX" || tok == "$PREFIX" || strings.EqualFold(tok, "prefix") || strings.EqualFold(tok, "$prefix") {
		k.prefix = true
		k.canonical = "PREFIX"
		return k, nil
	}

	if b, ok, err := decodeEscapeToken(tok); ok || err != nil {
		if err != nil {
			return k, err
		}
		k.text = b
		return k, nil
	}

	if utf8.RuneCountInString(tok) == 1 {
		k.text = []byte(tok)
		k.canonical = tok
		return k, nil
	}

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
	if keyish || looksLikeKeyName(tok, norm) {
		return k, errUnknownKey{token: tok, didYouMean: suggestKeyName(norm)}
	}
	k.text = []byte(tok)
	k.canonical = tok
	return k, nil
}

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
		nk = namedKeyByName["Tab"]
		k.mods.shift = true
	}
	k.named = &nk
	k.canonical = k.mods.prefix() + nk.name
	return k, nil
}

func modifiedChar(r rune, mods keyMods, tok string) ([]byte, error) {
	out := string(r)
	if mods.shift {
		out = string(unicode.ToUpper(r))
	}
	if mods.ctrl {
		c, ok := controlByte(r)
		if !ok {
			return nil, errfmt.Errorf("unsupported ctrl combination %q: ctrl works on a letter or one of @ [ \\ ] ^ _ ? and space", tok)
		}
		out = string([]byte{c})
	}
	if mods.alt {
		return append([]byte{0x1b}, out...), nil
	}
	return []byte(out), nil
}

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

func splitKeyMods(tok string) (mods keyMods, rest string, hasMods bool, err error) {
	rest = tok
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
	if strings.HasSuffix(tok, "++") {
		parts = append(parts[:len(parts)-2], "+")
	}
	key := ""
	for _, p := range parts {
		name, isMod := config.ParseModifier(p)
		switch {
		case isMod && name == config.ModCtrl:
			mods.ctrl = true
		case isMod && name == config.ModAlt:
			mods.alt = true
		case isMod && name == config.ModShift:
			mods.shift = true
		case isMod && name == config.ModSuper:
			mods.super = true
		case isMod:
			return keyMods{}, tok, false, errfmt.Errorf("unsupported modifier in %q: a terminal has no encoding for %s. Use ctrl, alt or shift", tok, name)
		default:
			if key != "" {
				return keyMods{}, tok, false, errUnknownKey{token: tok}
			}
			key = p
		}
	}
	if key == "" {
		return keyMods{}, tok, false, errfmt.Errorf("unsupported ctrl combination %q: a modifier needs a key after it, as in ctrl+c", tok)
	}
	return mods, key, true, nil
}

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
				return nil, true, errfmt.Errorf("bad escape in %q: \\x needs two hex digits", tok)
			}
			v, perr := strconv.ParseUint(tok[i+1:i+3], 16, 8)
			if perr != nil {
				return nil, true, errfmt.Errorf("bad escape in %q: \\x needs two hex digits", tok)
			}
			out = append(out, byte(v))
			i += 2
		case 'u':
			if i+4 >= len(tok) {
				return nil, true, errfmt.Errorf("bad escape in %q: \\u needs four hex digits", tok)
			}
			v, perr := strconv.ParseUint(tok[i+1:i+5], 16, 32)
			if perr != nil {
				return nil, true, errfmt.Errorf("bad escape in %q: \\u needs four hex digits", tok)
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
				return nil, true, errfmt.Errorf("bad escape in %q: %v", tok, perr)
			}
			out = append(out, byte(v))
			i = j - 1
		default:
			out = append(out, '\\', tok[i])
		}
	}
	return out, true, nil
}

func sendKeysBytes(keys []sendKey, modes paneKeyModes) ([]byte, error) {
	var out []byte
	for _, k := range keys {
		if k.prefix {
			return nil, errfmt.Errorf("the prefix key only works with an attached client. Attach one and retry")
		}
		if k.mods.super {
			return nil, errfmt.Errorf("unsupported modifier in %q: a terminal has no encoding for super. Use ctrl, alt or shift", k.token)
		}
		out = append(out, k.encode(modes)...)
	}
	return out, nil
}

func sendKeysCanonical(keys []sendKey) (string, error) {
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		if k.canonical == "" {
			return "", errfmt.Errorf("the escape sequence %q is not a key the window manager reads. Pass a window to write it to that window's terminal", k.token)
		}
		parts = append(parts, k.canonical)
	}
	return strings.Join(parts, " "), nil
}

func hasPrefixKey(keys []sendKey) bool {
	for _, k := range keys {
		if k.prefix {
			return true
		}
	}
	return false
}

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

var keyModifiers = func() []keyModifier {
	tmux := map[string][]string{
		config.ModCtrl:  {"C-", "^"},
		config.ModAlt:   {"M-"},
		config.ModShift: {"S-"},
	}
	var out []keyModifier
	for _, name := range []string{config.ModCtrl, config.ModAlt, config.ModShift, config.ModSuper} {
		var spellings []string
		for _, s := range config.ModifierSpellings(name) {
			spellings = append(spellings, s+"+")
		}
		out = append(out, keyModifier{Name: name, Spellings: append(spellings, tmux[name]...)})
	}
	return out
}()

type keyModifier struct {
	Name      string   `json:"name"`
	Spellings []string `json:"spellings"`
}

var ctrlCharacters = []string{"@", "[", `\`, "]", "^", "_", "?", "space", "2", "6", "-"}

var escapePrefixes = []string{`\e`, `\E`, `\x1b`, `\033`, `\u001b`, "^[", "ESC byte"}

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
