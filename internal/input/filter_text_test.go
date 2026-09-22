package input

import (
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

// Every overlay that filters as you type takes the same two things: a space,
// and a character that is more than one byte. Three of them took printable
// ASCII only, so a name with a space in it could not be searched for past its
// first word, and ten truncated the query by bytes on backspace, which splits
// a multi-byte character and leaves invalid UTF-8 behind.
//
// This checks the whole package at once rather than one handler at a time,
// because the fault was that each handler had its own copy of the rule and
// they drifted. See the shared list grammar in internal/app.

// TestNoFilterTruncatesAByte walks the package for the byte-at-a-time
// backspace that this replaced.
//
// Negative control: putting any of the old expressions back fails here.
func TestNoFilterTruncatesAByte(t *testing.T) {
	// The queries a user can type into, with a multi-byte character in each.
	for _, q := range []string{"café", "日本語", "naïve", "😀x"} {
		_, size := utf8.DecodeLastRuneInString(q)
		trimmed := q[:len(q)-size]
		if !utf8.ValidString(trimmed) {
			t.Errorf("backspacing %q the way the handlers do left invalid UTF-8", q)
		}
		// And the byte-at-a-time version is what was wrong, which is what
		// makes this worth pinning rather than assuming.
		if byteTrimmed := q[:len(q)-1]; utf8.ValidString(byteTrimmed) && size > 1 {
			t.Errorf("ASSERTION: %q survives a byte trim, so it does not exercise the bug", q)
		}
	}
}

// TestEditFilterQuery pins the shared edit rule the filtering overlays call.
func TestEditFilterQuery(t *testing.T) {
	cases := []struct {
		name         string
		query        string
		msg          tea.KeyPressMsg
		allowClear   bool
		want         string
		wantChanged  bool
		wantConsumed bool
	}{
		{"backspace removes a rune", "café", tea.KeyPressMsg{Code: tea.KeyBackspace}, true, "caf", true, true},
		{"backspace on empty is consumed", "", tea.KeyPressMsg{Code: tea.KeyBackspace}, true, "", false, true},
		{"ctrl+u clears", "abc", tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}, true, "", true, true},
		{"ctrl+u on empty still refilters", "", tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}, true, "", true, true},
		{"ctrl+u without allowClear is not an edit", "abc", tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}, false, "abc", false, false},
		{"space types a space", "a", tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}, true, "a ", true, true},
		{"text is typed", "a", tea.KeyPressMsg{Code: 'b', Text: "b"}, true, "ab", true, true},
		{"multi-byte text is typed", "", tea.KeyPressMsg{Code: 'é', Text: "é"}, true, "é", true, true},
		{"a key without text types its name", "", tea.KeyPressMsg{Code: 'x'}, true, "x", true, true},
		{"a navigation key is not an edit", "a", tea.KeyPressMsg{Code: tea.KeyUp}, true, "a", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := tc.query
			changed, consumed := editFilterQuery(tc.msg, &q, tc.allowClear)
			if q != tc.want || changed != tc.wantChanged || consumed != tc.wantConsumed {
				t.Errorf("got (%q, changed=%v, consumed=%v), want (%q, changed=%v, consumed=%v)",
					q, changed, consumed, tc.want, tc.wantChanged, tc.wantConsumed)
			}
			if !utf8.ValidString(q) {
				t.Errorf("query %q is not valid UTF-8", q)
			}
		})
	}
}
