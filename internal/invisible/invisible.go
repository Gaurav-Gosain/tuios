// Package invisible finds the characters that draw nothing but change how
// text reads: format characters, variation selectors and line separators.
// Every place tuios shows text another program wrote (agent mail, remote
// captures, notifications, titles, agent metadata) removes them with this
// package, so all of them agree on what counts as invisible.
//
// It imports nothing from tuios, so any package can use it.
package invisible

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// zwj is the zero-width joiner, U+200D.
const zwj = 0x200d

// Rune reports whether r draws nothing and is removed from untrusted text:
//
//   - every format character (Unicode category Cf): the zero-width space,
//     joiners and non-joiner, the bidi marks, embeddings, overrides and
//     isolates, the word joiner and invisible operators, the byte order mark,
//     the soft hyphen, the Mongolian vowel separator, the interlinear
//     annotation marks, the musical symbol format marks, and the tag
//     characters;
//   - the whole U+2060 to U+206F and U+E0000 to U+E007F blocks, unassigned
//     points included, so a newer Unicode version cannot slip one past an
//     older table;
//   - the variation selectors, U+FE00 to U+FE0F and U+E0100 to U+E01EF.
//     They are combining marks rather than format characters, and a run of
//     them after one visible character can carry a whole hidden message;
//   - the line and paragraph separators, U+2028 and U+2029. Strip turns them
//     into a line feed; a caller that checks single runes drops them.
//
// A bidi override reorders what follows it, and the rest let text hold words
// a person cannot see but a program reading it can.
func Rune(r rune) bool {
	switch {
	case r < 0xad:
		return false
	case r >= 0x2060 && r <= 0x206f:
		return true
	case r >= 0xe0000 && r <= 0xe007f:
		return true
	case r >= 0xfe00 && r <= 0xfe0f:
		return true
	case r >= 0xe0100 && r <= 0xe01ef:
		return true
	case r == 0x2028 || r == 0x2029:
		return true
	}
	return unicode.Is(unicode.Cf, r)
}

// Strip returns s without the characters Rune reports, with two exceptions.
// A line or paragraph separator becomes a line feed, so the line break it
// stands for stays a line break and a fence's gutter still starts the next
// line. A zero-width joiner stays when it sits between two emoji, where it
// builds one picture out of several (a family, a profession, a flag); it is
// dropped everywhere else.
func Strip(s string) string {
	if !strings.ContainsFunc(s, Rune) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	prev := rune(-1)
	for i, r := range s {
		switch {
		case r == 0x2028 || r == 0x2029:
			b.WriteByte('\n')
			prev = '\n'
		case r == zwj:
			next, _ := utf8.DecodeRuneInString(s[i+utf8.RuneLen(zwj):])
			if pictographic(prev) && pictographic(next) {
				b.WriteRune(r)
				prev = r
			}
		case Rune(r):
		default:
			b.WriteRune(r)
			prev = r
		}
	}
	return b.String()
}

// pictographic reports whether r is an emoji that a zero-width joiner may
// join to another. It is a close cover of the Extended_Pictographic property
// plus the skin tone modifiers and regional indicators, which sit in the
// U+1F000 to U+1FAFF span.
func pictographic(r rune) bool {
	switch {
	case r == 0xa9, r == 0xae, r == 0x203c, r == 0x2049, r == 0x2122, r == 0x2139:
		return true
	case r >= 0x2194 && r <= 0x21aa:
		return true
	case r >= 0x231a && r <= 0x23ff:
		return true
	case r == 0x24c2:
		return true
	case r >= 0x25aa && r <= 0x27bf:
		return true
	case r >= 0x2934 && r <= 0x2935:
		return true
	case r >= 0x2b05 && r <= 0x2b55:
		return true
	case r == 0x3030, r == 0x303d, r == 0x3297, r == 0x3299:
		return true
	case r >= 0x1f000 && r <= 0x1faff:
		return true
	case r >= 0x1fc00 && r <= 0x1fffd:
		return true
	}
	return false
}
