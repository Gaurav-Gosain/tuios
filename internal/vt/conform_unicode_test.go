package vt_test

import "testing"

// Unicode cases small enough to state as a screen. The sweeps over the UAX data
// files live in unicode_grapheme_test.go and unicode_width_test.go; these are
// the individual shapes worth naming, plus the one divergence left open.

func TestConform_UnicodeOnScreen(t *testing.T) {
	runConform(t, []conformCase{
		{
			name:   "a wide character takes two columns",
			in:     "世X",
			want:   "世X",
			cursor: "3,0",
			cells: []cellWant{
				{x: 0, y: 0, content: "世", width: 2},
				{x: 2, y: 0, content: "X", width: 1},
			},
		},
		{
			name:   "a combining mark stays with its base",
			in:     "éX",
			want:   "éX",
			cursor: "2,0",
			cells: []cellWant{
				{x: 0, y: 0, content: "é", width: 1},
				{x: 1, y: 0, content: "X", width: 1},
			},
		},
		{
			name:   "a zero width joiner family is one cluster",
			in:     "\U0001f469‍\U0001f469‍\U0001f467",
			want:   "\U0001f469‍\U0001f469‍\U0001f467",
			cursor: "2,0",
			cells: []cellWant{
				{x: 0, y: 0, content: "\U0001f469‍\U0001f469‍\U0001f467", width: 2},
			},
		},
		{
			name:   "a skin tone modifier joins its base",
			in:     "\U0001f44d\U0001f3fd",
			cursor: "2,0",
			want:   "\U0001f44d\U0001f3fd",
			cells: []cellWant{
				{x: 0, y: 0, content: "\U0001f44d\U0001f3fd", width: 2},
			},
		},
		{
			name:   "a regional indicator pair is one flag",
			in:     "\U0001f1fa\U0001f1f8",
			cursor: "2,0",
			want:   "\U0001f1fa\U0001f1f8",
			cells: []cellWant{
				{x: 0, y: 0, content: "\U0001f1fa\U0001f1f8", width: 2},
			},
		},
		{
			// Three in a row is two clusters, not one and a half: the pair
			// rule takes them two at a time.
			name:   "a third regional indicator starts a new flag",
			in:     "\U0001f1fa\U0001f1f8\U0001f1fa",
			cursor: "4,0",
			want:   "\U0001f1fa\U0001f1f8\U0001f1fa",
			cells: []cellWant{
				{x: 0, y: 0, content: "\U0001f1fa\U0001f1f8", width: 2},
				{x: 2, y: 0, content: "\U0001f1fa", width: 2},
			},
		},
		{
			name:   "a text presentation selector keeps the base narrow",
			in:     "❤︎",
			cursor: "1,0",
			want:   "❤︎",
			cells: []cellWant{
				{x: 0, y: 0, content: "❤︎", width: 1},
			},
		},
		{
			name:   "an emoji presentation selector makes the base wide",
			in:     "❤️",
			cursor: "2,0",
			want:   "❤️",
			cells: []cellWant{
				{x: 0, y: 0, content: "❤️", width: 2},
			},
		},
		{
			name:   "a keycap sequence is one cluster",
			in:     "1️⃣",
			cursor: "2,0",
			want:   "1️⃣",
			cells: []cellWant{
				{x: 0, y: 0, content: "1️⃣", width: 2},
			},
		},
		{
			name:   "a tag sequence flag is one cluster",
			in:     "\U0001f3f4\U000e0067\U000e0062\U000e0073\U000e0063\U000e0074\U000e007f",
			cursor: "2,0",
			want:   "\U0001f3f4\U000e0067\U000e0062\U000e0073\U000e0063\U000e0074\U000e007f",
			cells: []cellWant{
				{x: 0, y: 0, content: "\U0001f3f4\U000e0067\U000e0062\U000e0073\U000e0063\U000e0074\U000e007f", width: 2},
			},
		},
		{
			// The bytes that can never start a character are dropped rather
			// than drawn. What matters is that they never become a character
			// the guest did not send.
			name:   "bytes that cannot start a character are dropped",
			in:     "a\xff\xfeb",
			cursor: "2,0",
			want:   "ab",
		},
		{
			// An overlong encoding of U+0080 must not decode to U+0080: that
			// is how a filter checking for raw C1 bytes gets walked past. It
			// does not reach the screen here, but it does reach the control
			// code dispatcher, which is why this case expects a rejection
			// rather than silence.
			name:      "an overlong encoding does not become the character it encodes",
			in:        "a\xc0\x80b",
			cursor:    "2,0",
			want:      "ab",
			unhandled: true,
		},
		{
			name:   "a code point past the last plane becomes a replacement character",
			in:     "a\xf4\x90\x80\x80b",
			cursor: "3,0",
			want:   "a\ufffdb",
		},
	})
}

// TestConform_InvalidUTF8IsInconsistent records a divergence that is not fixed.
//
// Invalid UTF-8 is handled two different ways depending on which way it is
// invalid. A byte that cannot start a character is dropped without trace, while
// a well-formed-looking sequence that decodes out of range becomes one U+FFFD.
// Unicode's own recommendation is one U+FFFD per maximal invalid subpart, which
// would give both of these a visible replacement character.
//
// It is left open because terminals disagree with each other here more than any
// of them disagrees with Unicode, and because the property that matters,
// invalid input never becoming a valid character, already holds. Fixing it
// means changing the decoder in x/ansi rather than anything in this package.
func TestConform_InvalidUTF8IsInconsistent(t *testing.T) {
	runConform(t, []conformCase{
		{
			name:     "a stray byte gets a replacement character too",
			in:       "a\xffb",
			want:     "a\ufffdb",
			knownBug: "bytes that cannot start a character are dropped instead of replaced",
		},
	})
}

// TestConform_PrependBeforeASCII records a divergence that is not fixed.
//
// A Prepend character binds forwards, so U+0600 followed by `b` is a single
// cluster. The printable-ASCII path draws its character the moment it sees it
// and cannot be talked out of it by something that arrived earlier, so the two
// land in separate cells.
//
// It stays open on purpose. Fixing it means routing ASCII through the buffered
// path whenever anything is buffered, which makes every character after the
// first non-ASCII one on a line a buffered write. That is the hottest path in
// the emulator, and the cost is not worth it for the thirteen code points with
// this property, none of which changes what the line looks like: the Prepend
// characters are all invisible formatting marks.
func TestConform_PrependBeforeASCII(t *testing.T) {
	runConform(t, []conformCase{
		{
			name:     "a prepend character joins the ASCII base after it",
			in:       "؀b",
			want:     "؀b",
			knownBug: "the printable-ASCII path draws before it can know a Prepend preceded it",
			cells: []cellWant{
				{x: 0, y: 0, content: "؀b"},
			},
		},
	})
}
