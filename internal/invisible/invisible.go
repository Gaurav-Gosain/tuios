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

// Blank reports whether r is a character Rune does not remove that still
// draws as nothing or as a blank cell: the rest of the Unicode
// Default_Ignorable_Code_Point set (the combining grapheme joiner, the Hangul
// fillers, the Khmer inherent vowels, the Mongolian free variation selectors,
// the shorthand format controls, the noncharacters U+FFF0 to U+FFF8 and the
// whole U+E0000 to U+E0FFF block), and letters and symbols that draw as a
// blank: the braille pattern blank and the object replacement character.
//
// A Hangul filler is a letter, so a program reads it as part of a name. A
// line such as `const \u3164 = req.query.\u3164; exec(\u3164)` shows as
// "const   = req.query. ; exec( )" while it runs whatever the request names.
func Blank(r rune) bool {
	switch {
	case r < 0x34f:
		return false
	case r == 0x34f, r == 0x61c, r == 0x115f, r == 0x1160, r == 0x17b4, r == 0x17b5:
		return true
	case r >= 0x180b && r <= 0x180f:
		return true
	case r == 0x2800, r == 0x3164, r == 0xffa0, r == 0xfffc:
		return true
	case r >= 0xfff0 && r <= 0xfff8:
		return true
	case r >= 0x1bca0 && r <= 0x1bca3:
		return true
	case r >= 0x1d173 && r <= 0x1d17a:
		return true
	case r >= 0xe0000 && r <= 0xe0fff:
		return true
	}
	return false
}

// MaxMarks is the most combining marks Printable takes on one character. Real
// text stacks two or three (Vietnamese, Thai, Devanagari); a longer stack
// draws over the lines around it and can hide what is under it.
const MaxMarks = 4

// Printable reports whether s reads on screen as it is: valid UTF-8, every
// rune a visible character or a plain space, none of them blank (Blank), and
// no character with more than MaxMarks combining marks. A format character
// such as a bidi override, which can make a line read differently from what
// it holds, fails.
func Printable(s string) bool {
	return shown(s, false)
}

// PrintableText is Printable for text of several lines: a tab and a line feed
// are taken too. A carriage return is not, since it can draw a line over the
// one before it.
func PrintableText(s string) bool {
	return shown(s, true)
}

func shown(s string, text bool) bool {
	if !utf8.ValidString(s) {
		return false
	}
	marks := 0
	for _, r := range s {
		if text && (r == '\t' || r == '\n') {
			marks = 0
			continue
		}
		if !unicode.IsPrint(r) || Rune(r) || Blank(r) {
			return false
		}
		if unicode.In(r, unicode.Mn, unicode.Me) {
			if marks++; marks > MaxMarks {
				return false
			}
			continue
		}
		marks = 0
	}
	return true
}
