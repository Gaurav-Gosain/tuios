package input

import (
	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/textedit"
)

// editFilterQuery applies the type-to-filter edit rule to an overlay's query.
// Every overlay that filters as you type shares this one rule, so none of them
// takes printable ASCII only or cuts the query a byte at a time. See
// filter_text_test.go.
//
// The rule:
//   - backspace removes the last rune, never a single byte of one.
//   - alt+backspace, ctrl+w and ctrl+backspace remove the last word, by the
//     rule of textedit.DropLastWord. They are not gated by allowClear: that flag
//     is for overlays that give ctrl+u another meaning, and none gives these
//     keys one.
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
	return textedit.EditLine(q, msg.String(), msg.Text, allowClear)
}
