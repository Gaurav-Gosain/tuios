package session

import (
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// herdr's key grammar, read into send-keys calls.
//
// pane.send_keys takes a list of keys in herdr's grammar
// (src/config/keybinds.rs parse_key_combo at herdrTargetVersion): a key name
// or one character, after modifiers joined with '+', matched without regard
// to case. send-keys takes one string that it splits on spaces and commas, so
// a comma or a space typed as itself cannot be a token there. The keys are
// therefore sent in runs: named keys as one send-keys call, and plain
// characters as another with literal set, which types them as they are.

// herdrKeyRun is one send-keys call: tokens joined for send-keys, or text
// typed as it is.
type herdrKeyRun struct {
	keys    string
	literal bool
}

// herdrKeyNames maps herdr's key names, lower-cased, to send-keys names. The
// punctuation names become the character. tuios's own names for the keys
// herdr has none for (PageUp, Home, End, Insert, Delete) are taken as well.
var herdrKeyNames = map[string]string{
	"space": " ", "enter": "Enter", "return": "Enter", "esc": "Escape", "escape": "Escape",
	"tab": "Tab", "backtab": "BTab", "backspace": "Backspace", "bs": "Backspace",
	"left": "Left", "right": "Right", "up": "Up", "down": "Down",
	"minus": "-", "comma": ",", "period": ".", "slash": "/", "backslash": `\`,
	"quote": "'", "double_quote": `"`, "double-quote": `"`, "semicolon": ";", "colon": ":",
	"percent": "%", "ampersand": "&", "backtick": "`", "plus": "+",
	"pageup": "PageUp", "pagedown": "PageDown", "home": "Home", "end": "End",
	"insert": "Insert", "delete": "Delete",
}

// herdrKeyMods are herdr's modifier words and the send-keys prefix of each.
// cmd, super and hyper have no byte encoding in a terminal without a
// keyboard protocol, so a key that holds one is refused.
var herdrKeyMods = map[string]string{
	"ctrl": "ctrl", "control": "ctrl", "shift": "shift",
	"alt": "alt", "option": "alt", "meta": "alt",
	"cmd": "", "command": "", "super": "", "hyper": "",
}

// herdrKeyRuns reads herdr keys into send-keys runs. It returns the first key
// it cannot read, for herdr's invalid_key error, and sends nothing then.
func herdrKeyRuns(keys []string) ([]herdrKeyRun, string) {
	var runs []herdrKeyRun
	add := func(tok string, literal bool) {
		if n := len(runs); n > 0 && runs[n-1].literal == literal {
			if literal {
				runs[n-1].keys += tok
			} else {
				runs[n-1].keys += " " + tok
			}
			return
		}
		runs = append(runs, herdrKeyRun{keys: tok, literal: literal})
	}
	for _, key := range keys {
		tok, literal, ok := herdrKeyToken(key)
		if !ok {
			return nil, key
		}
		add(tok, literal)
	}
	return runs, ""
}

// herdrKeyToken reads one herdr key: a send-keys token, or with literal the
// text of a plain character.
func herdrKeyToken(key string) (string, bool, bool) {
	key = strings.TrimSpace(key)
	switch key {
	case "C-c", "c-c":
		key = "ctrl+c"
	case "+":
		key = "plus"
	case "":
		return "", false, false
	}
	var mods []string
	base := ""
	for part := range strings.SplitSeq(key, "+") {
		part = strings.TrimSpace(part)
		if part == "" {
			return "", false, false
		}
		if prefix, isMod := herdrKeyMods[strings.ToLower(part)]; isMod {
			if prefix == "" {
				return "", false, false
			}
			if !slices.Contains(mods, prefix) {
				mods = append(mods, prefix)
			}
			continue
		}
		if base != "" {
			return "", false, false
		}
		base = part
	}
	if base == "" {
		return "", false, false
	}
	name, named := herdrKeyNames[strings.ToLower(base)]
	switch {
	case named:
	case utf8.RuneCountInString(base) == 1:
		name = base
	case (base[0] == 'f' || base[0] == 'F') && len(base) > 1:
		n, err := strconv.Atoi(base[1:])
		if err != nil || n < 1 || n > 12 {
			return "", false, false
		}
		name = "F" + strconv.Itoa(n)
	default:
		return "", false, false
	}
	if len(mods) == 0 && utf8.RuneCountInString(name) == 1 {
		// A plain character, the space included, is typed as itself.
		return name, true, true
	}
	if name == " " {
		name = "Space"
	}
	if name == "," {
		// send-keys splits on commas; with a modifier the comma is not a
		// key tuios encodes anyway.
		return "", false, false
	}
	if len(mods) == 0 {
		return name, false, true
	}
	return strings.Join(mods, "+") + "+" + name, false, true
}
