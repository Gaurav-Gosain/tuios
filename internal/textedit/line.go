package textedit

import "unicode/utf8"

// EditLine applies one key press to a one-line text field s, by the rules
// every short field of tuios shares:
//   - backspace and ctrl+h remove the last rune, never a single byte of one.
//   - alt+backspace, ctrl+w and ctrl+backspace remove the last word, by the
//     rule of DropLastWord.
//   - ctrl+u clears the field when allowClear is set. A field whose panel
//     gives ctrl+u another meaning passes false.
//   - space types a space.
//   - text, the text the key carries, is typed as it is, so multi-byte input
//     works. A field that filters what may be typed passes the filtered text.
//   - a single printable ASCII key name types itself, for a key built without
//     text, such as a synthesized key.
//
// key is the key press's name, as tea.KeyPressMsg.String gives it. consumed
// reports whether the key was an edit key at all. changed reports whether s
// changed, except that ctrl+u always reports a change, because the filtering
// fields refilter on it even when they are empty.
func EditLine(s *string, key, text string, allowClear bool) (changed, consumed bool) {
	switch {
	case key == "backspace" || key == "ctrl+h":
		if *s == "" {
			return false, true
		}
		_, size := utf8.DecodeLastRuneInString(*s)
		*s = (*s)[:len(*s)-size]
		return true, true
	case IsWordDeleteKey(key):
		if *s == "" {
			return false, true
		}
		*s = DropLastWord(*s)
		return true, true
	case key == "ctrl+u" && allowClear:
		*s = ""
		return true, true
	case key == "space":
		*s += " "
	case text != "":
		*s += text
	case len(key) == 1 && key[0] >= 32 && key[0] <= 126:
		*s += key
	default:
		return false, false
	}
	return true, true
}
