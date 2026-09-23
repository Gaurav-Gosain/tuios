package tmuxcompat

import (
	"fmt"
	"strconv"
	"strings"
)

// send-keys: tmux key names to the bytes a terminal sends for them.
//
// Without -l, tmux looks each argument up as a key name first and sends it as
// literal text only when it is not one. The shim does the same lookup and
// writes the bytes itself, rather than passing names to tuios send-keys, whose
// key syntax is its own ("ctrl+c", comma separated) and would read tmux text
// differently.

var namedKeys = map[string]string{
	"enter":    "\r",
	"tab":      "\t",
	"btab":     "\x1b[Z",
	"space":    " ",
	"escape":   "\x1b",
	"bspace":   "\x7f",
	"up":       "\x1b[A",
	"down":     "\x1b[B",
	"right":    "\x1b[C",
	"left":     "\x1b[D",
	"home":     "\x1b[H",
	"end":      "\x1b[F",
	"ic":       "\x1b[2~",
	"insert":   "\x1b[2~",
	"dc":       "\x1b[3~",
	"delete":   "\x1b[3~",
	"ppage":    "\x1b[5~",
	"pageup":   "\x1b[5~",
	"pgup":     "\x1b[5~",
	"npage":    "\x1b[6~",
	"pagedown": "\x1b[6~",
	"pgdn":     "\x1b[6~",
	"f1":       "\x1bOP",
	"f2":       "\x1bOQ",
	"f3":       "\x1bOR",
	"f4":       "\x1bOS",
	"f5":       "\x1b[15~",
	"f6":       "\x1b[17~",
	"f7":       "\x1b[18~",
	"f8":       "\x1b[19~",
	"f9":       "\x1b[20~",
	"f10":      "\x1b[21~",
	"f11":      "\x1b[23~",
	"f12":      "\x1b[24~",
}

// keyBytes returns the bytes for one send-keys argument read as a key name,
// and false when it is not a key name (it is then sent as text).
func keyBytes(arg string) (string, bool) {
	if arg == "" {
		return "", false
	}
	if b, ok := namedKeys[strings.ToLower(arg)]; ok {
		return b, true
	}
	// Modifier prefixes: C- (or ^x), M-, S-. S- is accepted only where it
	// changes nothing the shim can express (S-Tab is BTab).
	meta := false
	rest := arg
	ctrl := false
	for {
		switch {
		case len(rest) > 2 && (strings.HasPrefix(rest, "C-") || strings.HasPrefix(rest, "c-")):
			ctrl = true
			rest = rest[2:]
			continue
		case len(rest) > 2 && (strings.HasPrefix(rest, "M-") || strings.HasPrefix(rest, "m-")):
			meta = true
			rest = rest[2:]
			continue
		case len(rest) == 2 && rest[0] == '^':
			ctrl = true
			rest = rest[1:]
			continue
		}
		break
	}
	if !ctrl && !meta {
		return "", false
	}
	var base string
	switch {
	case len(rest) == 1:
		base = rest
	default:
		b, ok := namedKeys[strings.ToLower(rest)]
		if !ok {
			return "", false
		}
		base = b
	}
	if ctrl {
		if len(base) != 1 {
			return "", false
		}
		c := base[0]
		switch {
		case c >= 'a' && c <= 'z':
			base = string(c - 'a' + 1)
		case c >= '@' && c <= '_':
			base = string(c - '@')
		case c == ' ' || c == '2':
			base = "\x00"
		case c == '?':
			base = "\x7f"
		default:
			return "", false
		}
	}
	if meta {
		base = "\x1b" + base
	}
	return base, true
}

// sendKeysText builds the text send-keys writes for its arguments. literal is
// -l; hex is -H, where every argument is a hex byte.
func sendKeysText(args []string, literal, hex bool) (string, error) {
	var b strings.Builder
	for _, a := range args {
		switch {
		case hex:
			n, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(a), "0x"), 16, 8)
			if err != nil {
				return "", logAs{err: fmt.Errorf("send-keys: invalid hex key %q", a), log: "send-keys: invalid hex key"}
			}
			b.WriteByte(byte(n))
		case literal:
			b.WriteString(a)
		default:
			if k, ok := keyBytes(a); ok {
				b.WriteString(k)
			} else {
				b.WriteString(a)
			}
		}
	}
	return b.String(), nil
}
