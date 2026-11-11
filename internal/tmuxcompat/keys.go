package tmuxcompat

import (
	"slices"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

// send-keys: tmux key names, read into the daemon's send-keys calls.
//
// Without -l, tmux looks each argument up as a key name first and sends it as
// literal text only when it is not one. The shim does the same lookup, then
// hands each key to the daemon's send-keys verb as a token in its own syntax
// ("ctrl+c", "alt+Up"), and each run of text to send-text. send-keys encodes
// a key for the pane's modes, as real tmux does: Up is ESC O A once the program
// has turned on application cursor keys, and a modified character follows the
// pane's kitty keyboard or modifyOtherKeys mode. The shim knows only which
// words tmux reads as keys; the bytes are send-keys' alone.

// tmuxKeyNames are the words tmux reads as key names, lower-cased, each with
// the send-keys name it is.
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

// keyToken reads one send-keys argument as a tmux key and returns it as a
// send-keys token, and false when it is not a key (it is then sent as text).
// A key is a name, or a name or one character after C- (or ^x), M- and S-.
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
	// send-keys splits its keys on spaces and commas, so those two go by name.
	switch rest {
	case " ":
		rest = "Space"
	case ",":
		rest = "Comma"
	}
	return mods.String() + rest, true
}

// keyRun is one call the shim makes for send-keys: keys for the daemon's
// send-keys verb, or text for send-text.
type keyRun struct {
	keys []string
	text string
}

// sendKeysRuns reads send-keys arguments into the calls that send them, in
// order. literal is -l; hex is -H, where every argument is a hex byte. Both
// send text only.
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
	// An empty argument adds no text.
	return slices.DeleteFunc(runs, func(r keyRun) bool { return r.keys == nil && r.text == "" }), nil
}

// repeatRuns is runs sent count times, with the runs of one kind that meet
// joined into one call.
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
