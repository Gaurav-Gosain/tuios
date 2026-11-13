package invisible

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// StripHidden returns s without the invisible characters that can carry
// words a person cannot see, and how many it removed. It is the rule for text
// tuios types into an agent on a caller's behalf, which no person reads first.
//
// Strip is not used there because it breaks emoji: it drops every variation
// selector, so a keycap or a red heart loses its emoji form, and every tag
// character, so a subdivision flag (England, Scotland, Wales) falls apart.
// StripHidden removes:
//
//   - the tag characters, U+E0000 to U+E007F, except in a flag tag sequence:
//     a waving black flag, one to seven tag letters or digits, and the cancel
//     tag;
//   - the bidi controls: the marks, embeddings, overrides and isolates;
//   - the zero-width space, the word joiner and the zero-width no-break space;
//   - a zero-width joiner that does not sit between two emoji, and a
//     zero-width non-joiner that does not sit between two letters;
//   - each variation selector after the first in a run, since one selector
//     is all a character takes and a run can spell out a message.
//
// Everything else, line breaks and other format characters included, is kept.
func StripHidden(s string) (string, int) {
	if !strings.ContainsFunc(s, hiddenCandidate) {
		return s, 0
	}
	var b strings.Builder
	b.Grow(len(s))
	removed := 0
	base := rune(-1) // the last kept rune that is not a variation selector
	prevVS := false
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		rest := s[i+size:]
		keep := true
		switch {
		case r == flagBase:
			if n := flagTagsLen(rest); n > 0 {
				b.WriteString(s[i : i+size+n])
				i += size + n
				base, prevVS = tagCancel, false
				continue
			}
		case r >= 0xe0000 && r <= 0xe007f:
			keep = false
		case bidiControl(r):
			keep = false
		case r == 0x200b || r == 0x2060 || r == 0xfeff:
			keep = false
		case r == zwj:
			next := nextBase(rest)
			keep = pictographic(base) && pictographic(next)
		case r == 0x200c:
			next, _ := utf8.DecodeRuneInString(rest)
			keep = unicode.IsLetter(base) && unicode.IsLetter(next)
		case variationSelector(r):
			keep = !prevVS && base >= 0
		}
		if keep {
			b.WriteString(s[i : i+size])
			if variationSelector(r) {
				prevVS = true
			} else {
				base, prevVS = r, false
			}
		} else {
			removed++
		}
		i += size
	}
	return b.String(), removed
}

// flagBase is the waving black flag a subdivision flag starts with, and
// tagCancel the tag that ends its sequence.
const (
	flagBase  = 0x1f3f4
	tagCancel = 0xe007f
)

// flagTagsLen is the byte length of the flag tag sequence s starts with: one
// to seven tag letters or digits and the cancel tag. It is 0 when s does not
// start with one.
func flagTagsLen(s string) int {
	n, count := 0, 0
	for n < len(s) {
		r, size := utf8.DecodeRuneInString(s[n:])
		switch {
		case r == tagCancel && count > 0:
			return n + size
		case count < 7 && ((r >= 0xe0030 && r <= 0xe0039) || (r >= 0xe0061 && r <= 0xe007a)):
			count++
			n += size
		default:
			return 0
		}
	}
	return 0
}

// nextBase is the first rune of s, past any variation selector.
func nextBase(s string) rune {
	for _, r := range s {
		if !variationSelector(r) {
			return r
		}
	}
	return -1
}

// hiddenCandidate reports whether r is one StripHidden may remove.
func hiddenCandidate(r rune) bool {
	return (r >= 0xe0000 && r <= 0xe007f) || bidiControl(r) || r == 0x200b || r == 0x200c ||
		r == zwj || r == 0x2060 || r == 0xfeff || variationSelector(r)
}

// bidiControl reports whether r is one of the Bidi_Control characters.
func bidiControl(r rune) bool {
	return r == 0x061c || r == 0x200e || r == 0x200f || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069)
}

// variationSelector reports whether r is a variation selector.
func variationSelector(r rune) bool {
	return (r >= 0xfe00 && r <= 0xfe0f) || (r >= 0xe0100 && r <= 0xe01ef)
}
