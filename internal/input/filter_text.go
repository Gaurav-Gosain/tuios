package input

import (
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

// editFilterQuery applies the type-to-filter edit rule to an overlay's query.
// Every overlay that filters as you type used to carry its own copy of this
// rule, and the copies drifted: some took printable ASCII only and some cut
// the query a byte at a time. See filter_text_test.go.
//
// The rule:
//   - backspace removes the last rune, never a single byte of one.
//   - ctrl+u clears the query when allowClear is set.
//   - space types a space.
//   - any key that carries text types that text, so multi-byte input works.
//   - a single printable ASCII key name types itself, for keys built without
//     text, such as a synthesized key.
//
// consumed reports whether the key was an edit key at all. changed reports
// whether the caller should refilter. Backspace on an empty query is consumed
// without a change. ctrl+u always reports a change, even on an empty query,
// because every caller refiltered on it unconditionally.
func editFilterQuery(msg tea.KeyPressMsg, q *string, allowClear bool) (changed, consumed bool) {
	key := msg.String()
	switch {
	case key == "backspace":
		if *q == "" {
			return false, true
		}
		_, size := utf8.DecodeLastRuneInString(*q)
		*q = (*q)[:len(*q)-size]
		return true, true
	case key == "ctrl+u" && allowClear:
		*q = ""
		return true, true
	case key == "space":
		*q += " "
	case msg.Text != "":
		*q += msg.Text
	case len(key) == 1 && key[0] >= 32 && key[0] <= 126:
		*q += key
	default:
		return false, false
	}
	return true, true
}
