package session

import (
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

type herdrKeyRun struct {
	keys    string
	literal bool
}

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

func herdrKeyMod(word string) (prefix string, isMod, refused bool) {
	name, ok := config.ParseModifier(word)
	if !ok {
		return "", false, false
	}
	switch name {
	case config.ModCtrl, config.ModAlt, config.ModShift:
		return name, true, false
	}
	return "", true, true
}

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
		if prefix, isMod, refused := herdrKeyMod(part); isMod {
			if refused {
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
		return name, true, true
	}
	if name == " " {
		name = "Space"
	}
	if name == "," {
		return "", false, false
	}
	if len(mods) == 0 {
		return name, false, true
	}
	return strings.Join(mods, "+") + "+" + name, false, true
}
