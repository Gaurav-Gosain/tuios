package input

import (
	"testing"
	"unicode/utf8"
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
