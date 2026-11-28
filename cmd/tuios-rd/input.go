//go:build linux || darwin

package main

// Terminal input: one parser for everything stdin carries. It splits kitty
// graphics replies and other terminal answers from the person's keys, mouse
// and paste, and turns keys into screen-relay input records.

import (
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Modifier bits as the kitty keyboard protocol numbers them (value - 1).
const (
	modShift = 1
	modAlt   = 2
	modCtrl  = 4
	modSuper = 8
)

// keyEv is one key press. name is a lower-case key name such as "a", "]",
// "enter" or "f12". text is set for a printable character typed with no
// modifier but shift.
type keyEv struct {
	name string
	mods int
	text rune
}

type mouseEv struct {
	cb      int
	x, y    int // pixels (1016) from 1
	release bool
}

// Everything the parser reports.
type termEv struct {
	key     *keyEv
	mouse   *mouseEv
	paste   string
	reply   *gfxReply // kitty graphics answer
	da1     bool      // primary device attributes answer
	kbd     int       // kitty keyboard flags answer, -1 when none
	cellPx  [2]int    // CSI 6 ; h ; w t answer
	pixMode int       // DECRQM answer for mode 1016, 0 when none
}

type gfxReply struct {
	id  int
	msg string
}

type parser struct {
	buf     []byte
	inPaste bool
	paste   strings.Builder
}

// feed parses as much of p (appended to what was left) as it can. When
// flush is true, a lone ESC at the end counts as the Escape key.
func (ps *parser) feed(p []byte, flush bool, emit func(termEv)) {
	ps.buf = append(ps.buf, p...)
	b := ps.buf
	i := 0
	for i < len(b) {
		if ps.inPaste {
			j := strings.Index(string(b[i:]), "\x1b[201~")
			if j < 0 {
				// keep a possible partial end marker
				keep := max(0, len(b)-5)
				if keep > i {
					ps.paste.Write(b[i:keep])
					i = keep
				}
				break
			}
			ps.paste.Write(b[i : i+j])
			emit(termEv{paste: ps.paste.String(), kbd: -1})
			ps.paste.Reset()
			ps.inPaste = false
			i += j + 6
			continue
		}
		n, ev, ok := ps.one(b[i:], flush)
		if !ok {
			break // incomplete; wait for more bytes
		}
		i += n
		if ev != nil {
			emit(*ev)
		}
	}
	ps.buf = append(ps.buf[:0], b[i:]...)
}

func kev(name string, mods int) *termEv { return &termEv{key: &keyEv{name: name, mods: mods}, kbd: -1} }

// one parses one event at the start of b. It returns ok=false when b holds
// only the start of a sequence.
func (ps *parser) one(b []byte, flush bool) (int, *termEv, bool) {
	c := b[0]
	if c != 0x1b {
		switch {
		case c == '\r' || c == '\n':
			return 1, kev("enter", 0), true
		case c == '\t':
			return 1, kev("tab", 0), true
		case c == 0x7f:
			return 1, kev("backspace", 0), true
		case c == 0x08:
			return 1, kev("backspace", modCtrl), true
		case c == 0:
			return 1, kev("space", modCtrl), true
		case c >= 1 && c <= 26:
			return 1, kev(string(rune('a'+c-1)), modCtrl), true
		case c >= 0x1c && c <= 0x1f:
			return 1, kev(string("\\]^_"[c-0x1c]), modCtrl), true
		}
		if !utf8.FullRune(b) {
			return 0, nil, false
		}
		r, n := utf8.DecodeRune(b)
		return n, textEv(r, 0), true
	}
	if len(b) == 1 {
		if flush {
			return 1, kev("escape", 0), true
		}
		return 0, nil, false
	}
	switch b[1] {
	case '[':
		return ps.csi(b)
	case '_': // APC: kitty graphics reply
		j := strings.Index(string(b), "\x1b\\")
		if j < 0 {
			return 0, nil, false
		}
		body := string(b[2:j])
		ev := &termEv{kbd: -1}
		if strings.HasPrefix(body, "G") {
			ev.reply = parseGfx(body[1:])
		}
		return j + 2, ev, true
	case 'O': // SS3
		if len(b) < 3 {
			return 0, nil, false
		}
		names := map[byte]string{'P': "f1", 'Q': "f2", 'R': "f3", 'S': "f4", 'A': "up", 'B': "down", 'C': "right", 'D': "left", 'H': "home", 'F': "end"}
		if nm, ok := names[b[2]]; ok {
			return 3, kev(nm, 0), true
		}
		return 3, nil, true
	case 0x1b:
		return 1, kev("escape", 0), true
	}
	// ESC + key: Alt + key
	n, ev, ok := ps.one(b[1:], flush)
	if !ok {
		return 0, nil, false
	}
	if ev != nil && ev.key != nil {
		ev.key.mods |= modAlt
		ev.key.text = 0
	}
	return n + 1, ev, true
}

func textEv(r rune, mods int) *termEv {
	if r == ' ' {
		return &termEv{key: &keyEv{name: "space", mods: mods, text: ' '}, kbd: -1}
	}
	name := string(r)
	if r >= 'A' && r <= 'Z' {
		name = string(r + 32)
		mods |= modShift
	}
	return &termEv{key: &keyEv{name: name, mods: mods, text: r}, kbd: -1}
}

func parseGfx(body string) *gfxReply {
	keys, msg, _ := strings.Cut(body, ";")
	r := &gfxReply{msg: msg}
	for _, kv := range strings.Split(keys, ",") {
		if v, ok := strings.CutPrefix(kv, "i="); ok {
			r.id, _ = strconv.Atoi(v)
		}
	}
	return r
}

var csiTilde = map[int]string{1: "home", 2: "insert", 3: "delete", 4: "end", 5: "pageup", 6: "pagedown", 7: "home", 8: "end",
	11: "f1", 12: "f2", 13: "f3", 14: "f4", 15: "f5", 17: "f6", 18: "f7", 19: "f8", 20: "f9", 21: "f10", 23: "f11", 24: "f12"}

var csiLetter = map[byte]string{'A': "up", 'B': "down", 'C': "right", 'D': "left", 'H': "home", 'F': "end", 'P': "f1", 'Q': "f2", 'R': "f3", 'S': "f4", 'E': "kp5"}

// kitty CSI u codes for keys that have no character.
var csiU = map[int]string{27: "escape", 13: "enter", 9: "tab", 127: "backspace", 32: "space",
	57358: "capslock", 57359: "scrolllock", 57361: "printscreen", 57362: "pause", 57363: "menu",
	57399: "0", 57400: "1", 57401: "2", 57402: "3", 57403: "4", 57404: "5", 57405: "6", 57406: "7", 57407: "8", 57408: "9",
	57409: ".", 57410: "/", 57411: "*", 57412: "-", 57413: "+", 57414: "enter", 57415: "=",
	57417: "left", 57418: "right", 57419: "up", 57420: "down", 57421: "pageup", 57422: "pagedown", 57423: "home", 57424: "end", 57425: "insert", 57426: "delete"}

func (ps *parser) csi(b []byte) (int, *termEv, bool) {
	j := 2
	for j < len(b) && (b[j] < 0x40 || b[j] > 0x7e) {
		j++
	}
	if j >= len(b) {
		if len(b) > 64 { // not a sequence we know; drop it
			return len(b), nil, true
		}
		return 0, nil, false
	}
	final := b[j]
	params := string(b[2:j])
	n := j + 1
	ev := &termEv{kbd: -1}
	switch {
	case strings.HasPrefix(params, "<") && (final == 'M' || final == 'm'):
		f := strings.Split(params[1:], ";")
		if len(f) == 3 {
			cb, _ := strconv.Atoi(f[0])
			x, _ := strconv.Atoi(f[1])
			y, _ := strconv.Atoi(f[2])
			ev.mouse = &mouseEv{cb: cb, x: x, y: y, release: final == 'm'}
		}
		return n, ev, true
	case strings.HasPrefix(params, "?") && final == 'c':
		ev.da1 = true
		return n, ev, true
	case strings.HasPrefix(params, "?") && final == 'u':
		ev.kbd, _ = strconv.Atoi(params[1:])
		return n, ev, true
	case strings.HasPrefix(params, "?1016;") && final == 'y':
		v := strings.TrimSuffix(strings.TrimPrefix(params, "?1016;"), "$")
		ev.pixMode, _ = strconv.Atoi(v)
		ev.pixMode += 10 // keep 0 for "no answer"
		return n, ev, true
	case final == 't':
		f := strings.Split(params, ";")
		if len(f) == 3 && f[0] == "6" {
			ev.cellPx[0], _ = strconv.Atoi(f[1])
			ev.cellPx[1], _ = strconv.Atoi(f[2])
		}
		return n, ev, true
	case params == "200" && final == '~':
		ps.inPaste = true
		return n, nil, true
	case final == 'I' || final == 'O':
		return n, nil, true
	}
	// Keys: CSI code[:alt...] ; mods[:event] ; text  u|~|letter
	f := strings.Split(params, ";")
	code := 1
	if f[0] != "" {
		code, _ = strconv.Atoi(strings.SplitN(f[0], ":", 2)[0])
	}
	mods, event := 0, 1
	if len(f) > 1 {
		m := strings.SplitN(f[1], ":", 2)
		mv, _ := strconv.Atoi(m[0])
		if mv > 0 {
			mods = mv - 1
		}
		if len(m) > 1 {
			event, _ = strconv.Atoi(m[1])
		}
	}
	if event == 3 { // release
		return n, nil, true
	}
	mods &= modShift | modAlt | modCtrl | modSuper
	switch final {
	case 'u':
		if nm, ok := csiU[code]; ok {
			ev.key = &keyEv{name: nm, mods: mods}
			return n, ev, true
		}
		if code >= 57441 && code <= 57452 { // a modifier key alone
			return n, nil, true
		}
		if code >= 32 && code < 57344 {
			r := rune(code)
			k := textEv(r, mods).key
			if mods&^modShift != 0 {
				k.text = 0
			} else if mods&modShift != 0 && len(f) > 2 {
				// associated text, when the terminal sends it
				if t, err := strconv.Atoi(strings.SplitN(f[2], ":", 2)[0]); err == nil {
					k.text = rune(t)
				}
			}
			ev.key = k
			return n, ev, true
		}
		return n, nil, true
	case '~':
		if nm, ok := csiTilde[code]; ok {
			ev.key = &keyEv{name: nm, mods: mods}
			return n, ev, true
		}
		return n, nil, true
	default:
		if nm, ok := csiLetter[final]; ok {
			ev.key = &keyEv{name: nm, mods: mods}
			return n, ev, true
		}
	}
	return n, nil, true
}

// ---- keys to HID usages ----

var hidByName = map[string]uint16{
	"enter": 0x28, "escape": 0x29, "backspace": 0x2A, "tab": 0x2B, "space": 0x2C,
	"-": 0x2D, "=": 0x2E, "[": 0x2F, "]": 0x30, "\\": 0x31, ";": 0x33, "'": 0x34, "`": 0x35, ",": 0x36, ".": 0x37, "/": 0x38,
	"capslock": 0x39, "printscreen": 0x46, "scrolllock": 0x47, "pause": 0x48, "insert": 0x49, "home": 0x4A, "pageup": 0x4B,
	"delete": 0x4C, "end": 0x4D, "pagedown": 0x4E, "right": 0x4F, "left": 0x50, "down": 0x51, "up": 0x52, "menu": 0x65,
}

// Shifted US-layout characters: the key and shift.
var shifted = map[rune]string{'!': "1", '@': "2", '#': "3", '$': "4", '%': "5", '^': "6", '&': "7", '*': "8", '(': "9", ')': "0",
	'_': "-", '+': "=", '{': "[", '}': "]", '|': "\\", ':': ";", '"': "'", '~': "`", '<': ",", '>': ".", '?': "/"}

func init() {
	for c := 'a'; c <= 'z'; c++ {
		hidByName[string(c)] = uint16(0x04 + c - 'a')
	}
	for c := '1'; c <= '9'; c++ {
		hidByName[string(c)] = uint16(0x1E + c - '1')
	}
	hidByName["0"] = 0x27
	for i := 1; i <= 12; i++ {
		hidByName[fmt.Sprintf("f%d", i)] = uint16(0x3A + i - 1)
	}
}

const (
	hidLCtrl  = 0xE0
	hidLShift = 0xE1
	hidLAlt   = 0xE2
	hidLGUI   = 0xE3
)

// keySpec is a key the person names in a flag, such as "ctrl+]" or "f12".
type keySpec struct {
	name string
	mods int
}

func parseKeySpec(s string) (keySpec, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	var k keySpec
	parts := strings.Split(s, "+")
	if strings.HasSuffix(s, "++") { // "ctrl++"
		parts = append(parts[:len(parts)-2], "+")
	}
	for i, p := range parts {
		if i == len(parts)-1 {
			k.name = p
			break
		}
		switch p {
		case "ctrl", "control", "c":
			k.mods |= modCtrl
		case "alt", "opt", "option", "meta", "m":
			k.mods |= modAlt
		case "shift", "s":
			k.mods |= modShift
		case "super", "cmd", "command", "win":
			k.mods |= modSuper
		default:
			return k, fmt.Errorf("unknown modifier %q in key %q", p, s)
		}
	}
	if k.name == "" {
		return k, fmt.Errorf("no key in %q", s)
	}
	return k, nil
}

func (k keySpec) matches(e *keyEv) bool { return e.name == k.name && e.mods == k.mods }

func (k keySpec) String() string {
	var b strings.Builder
	if k.mods&modCtrl != 0 {
		b.WriteString("Ctrl+")
	}
	if k.mods&modAlt != 0 {
		b.WriteString("Alt+")
	}
	if k.mods&modShift != 0 {
		b.WriteString("Shift+")
	}
	if k.mods&modSuper != 0 {
		b.WriteString("Super+")
	}
	if len(k.name) == 1 {
		b.WriteString(strings.ToUpper(k.name))
	} else {
		b.WriteString(strings.ToUpper(k.name[:1]))
		b.WriteString(k.name[1:])
	}
	return b.String()
}

// superFrom is the modifier that --super-key turns into Super, or 0.
var superFrom int

// superPrefix is the key that --super-key makes a one-shot Super prefix,
// or 0: press it, then a key, and the key goes out with Super.
var superPrefix rune

func parseSuperKey(v string) (int, error) {
	switch strings.ToLower(v) {
	case "", "none":
		return 0, nil
	case "alt", "opt", "option", "meta":
		return modAlt, nil
	case "ctrl", "control":
		return modCtrl, nil
	}
	if r := []rune(v); len(r) == 1 && r[0] > ' ' {
		superPrefix = r[0]
		return 0, nil
	}
	return 0, fmt.Errorf("--super-key %q: use alt, ctrl, none or one key such as §", v)
}

// isSuperPrefix reports whether k is the --super-key prefix key, pressed
// with no modifier.
func isSuperPrefix(k *keyEv) bool {
	if superPrefix == 0 || k.mods&^modShift != 0 {
		return false
	}
	return k.text == superPrefix || k.name == string(superPrefix)
}

// ---- input records ----

type record [16]byte

func rec(typ byte, code uint16, val int32, x, y uint16, buttons, mods uint16) record {
	var b record
	b[0] = typ
	binary.BigEndian.PutUint16(b[2:], code)
	binary.BigEndian.PutUint32(b[4:], uint32(val))
	binary.BigEndian.PutUint16(b[8:], x)
	binary.BigEndian.PutUint16(b[10:], y)
	binary.BigEndian.PutUint16(b[12:], buttons&0x1f)
	binary.BigEndian.PutUint16(b[14:], mods&0xff)
	return b
}

const (
	recKeyDown = 1
	recKeyUp   = 2
	recMove    = 3
	recBtnDown = 4
	recBtnUp   = 5
	recWheel   = 6
	recRelease = 7
	recText    = 8
)

// modKeys lists the HID modifier keys for kitty modifier bits, with the HID
// modifier mask bit of each (bit = usage - 0xE0).
func modKeys(mods int) []uint16 {
	var k []uint16
	if mods&modCtrl != 0 {
		k = append(k, hidLCtrl)
	}
	if mods&modShift != 0 {
		k = append(k, hidLShift)
	}
	if mods&modAlt != 0 {
		k = append(k, hidLAlt)
	}
	if mods&modSuper != 0 {
		k = append(k, hidLGUI)
	}
	return k
}

// keyRecords turns a key press into records: a text record for a plain
// character (the desktop layout cannot change it), else a chord of
// modifier downs, the key, and modifier ups.
func keyRecords(e *keyEv) []record {
	if superFrom != 0 && e.mods&superFrom != 0 {
		r := *e
		r.mods = r.mods&^superFrom | modSuper
		r.text = 0
		e = &r
	}
	if e.text != 0 && e.mods&^modShift == 0 {
		if e.text == ' ' {
			return chord(0x2C, 0)
		}
		return []record{rec(recText, 0, int32(e.text), 0, 0, 0, 0)}
	}
	name, mods := e.name, e.mods
	if len(name) == 1 {
		if base, ok := shifted[rune(name[0])]; ok {
			name, mods = base, mods|modShift
		}
	}
	code, ok := hidByName[name]
	if !ok {
		if r, _ := utf8.DecodeRuneInString(name); r >= 0x80 && mods&^modShift == 0 {
			return []record{rec(recText, 0, int32(r), 0, 0, 0, 0)}
		}
		return nil
	}
	return chord(code, mods)
}

func chord(code uint16, mods int) []record {
	var out []record
	var mask uint16
	mk := modKeys(mods)
	for _, m := range mk {
		mask |= 1 << (m - 0xE0)
		out = append(out, rec(recKeyDown, m, 0, 0, 0, 0, mask))
	}
	out = append(out, rec(recKeyDown, code, 0, 0, 0, 0, mask), rec(recKeyUp, code, 0, 0, 0, 0, mask))
	for i := len(mk) - 1; i >= 0; i-- {
		mask &^= 1 << (mk[i] - 0xE0)
		out = append(out, rec(recKeyUp, mk[i], 0, 0, 0, 0, mask))
	}
	return out
}

// textRecords types a pasted string.
func textRecords(s string) []record {
	var out []record
	for _, r := range strings.ReplaceAll(s, "\r\n", "\n") {
		if r == '\r' {
			r = '\n'
		}
		if r == 0 || (r < 0x20 && r != '\n' && r != '\t') || (r >= 0xD800 && r <= 0xDFFF) {
			continue
		}
		out = append(out, rec(recText, 0, r, 0, 0, 0, 0))
	}
	return out
}
