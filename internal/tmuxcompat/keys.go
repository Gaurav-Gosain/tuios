package tmuxcompat

import (
	"slices"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

var tmuxKeyNames = map[string]string{
	"enter":    "Enter",
	"tab":      "Tab",
	"btab":     "BTab",
	"space":    "Space",
	"escape":   "Escape",
	"bspace":   "Backspace",
	"up":       "Up",
	"down":     "Down",
	"right":    "Right",
	"left":     "Left",
	"home":     "Home",
	"end":      "End",
	"ic":       "Insert",
	"insert":   "Insert",
	"dc":       "Delete",
	"delete":   "Delete",
	"ppage":    "PageUp",
	"pageup":   "PageUp",
	"pgup":     "PageUp",
	"npage":    "PageDown",
	"pagedown": "PageDown",
	"pgdn":     "PageDown",
	"f1":       "F1",
	"f2":       "F2",
	"f3":       "F3",
	"f4":       "F4",
	"f5":       "F5",
	"f6":       "F6",
	"f7":       "F7",
	"f8":       "F8",
	"f9":       "F9",
	"f10":      "F10",
	"f11":      "F11",
	"f12":      "F12",
}

func keyToken(arg string) (string, bool) {
	if arg == "" {
		return "", false
	}
	if name, ok := tmuxKeyNames[strings.ToLower(arg)]; ok {
		return name, true
	}
	var mods strings.Builder
	rest := arg
	for {
		switch {
		case len(rest) > 2 && (strings.HasPrefix(rest, "C-") || strings.HasPrefix(rest, "c-")):
			mods.WriteString("ctrl+")
			rest = rest[2:]
			continue
		case len(rest) > 2 && (strings.HasPrefix(rest, "M-") || strings.HasPrefix(rest, "m-")):
			mods.WriteString("alt+")
			rest = rest[2:]
			continue
		case len(rest) > 2 && (strings.HasPrefix(rest, "S-") || strings.HasPrefix(rest, "s-")):
			mods.WriteString("shift+")
			rest = rest[2:]
			continue
		case len(rest) == 2 && rest[0] == '^':
			mods.WriteString("ctrl+")
			rest = rest[1:]
			continue
		}
		break
	}
	if mods.Len() == 0 {
		return "", false
	}
	if name, ok := tmuxKeyNames[strings.ToLower(rest)]; ok {
		return mods.String() + name, true
	}
	if len(rest) != 1 {
		return "", false
	}
	switch rest {
	case " ":
		rest = "Space"
	case ",":
		rest = "Comma"
	}
	return mods.String() + rest, true
}

type keyRun struct {
	keys []string
	text string
}

func sendKeysRuns(args []string, literal, hex bool) ([]keyRun, error) {
	var runs []keyRun
	addText := func(t string) {
		if n := len(runs); n > 0 && runs[n-1].keys == nil {
			runs[n-1].text += t
			return
		}
		runs = append(runs, keyRun{text: t})
	}
	for _, a := range args {
		switch {
		case hex:
			n, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(a), "0x"), 16, 8)
			if err != nil {
				return nil, logAs{err: errfmt.Errorf("send-keys: invalid hex key %q", a), log: "send-keys: invalid hex key"}
			}
			addText(string([]byte{byte(n)}))
		case literal:
			addText(a)
		default:
			tok, ok := keyToken(a)
			if !ok {
				addText(a)
				continue
			}
			if n := len(runs); n > 0 && runs[n-1].keys != nil {
				runs[n-1].keys = append(runs[n-1].keys, tok)
				continue
			}
			runs = append(runs, keyRun{keys: []string{tok}})
		}
	}
	return slices.DeleteFunc(runs, func(r keyRun) bool { return r.keys == nil && r.text == "" }), nil
}

func repeatRuns(runs []keyRun, count int) []keyRun {
	if count <= 1 || len(runs) == 0 {
		return runs
	}
	if len(runs) == 1 {
		r := runs[0]
		if r.keys != nil {
			keys := make([]string, 0, len(r.keys)*count)
			for range count {
				keys = append(keys, r.keys...)
			}
			return []keyRun{{keys: keys}}
		}
		return []keyRun{{text: strings.Repeat(r.text, count)}}
	}
	out := make([]keyRun, 0, len(runs)*count)
	for range count {
		for _, r := range runs {
			if n := len(out); n > 0 && (out[n-1].keys == nil) == (r.keys == nil) {
				if r.keys != nil {
					out[n-1].keys = append(slices.Clip(out[n-1].keys), r.keys...)
				} else {
					out[n-1].text += r.text
				}
				continue
			}
			out = append(out, keyRun{keys: slices.Clone(r.keys), text: r.text})
		}
	}
	return out
}
