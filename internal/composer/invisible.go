package composer

import (
	"strings"
	"unicode/utf8"
)

// invisible reports whether r is a character a person cannot see and an
// agent still reads: Unicode tag characters, bidirectional controls, and
// zero-width characters. Text pasted from a web page can carry instructions
// in them. The zero-width joiner and non-joiner stay, since emoji sequences
// and several scripts need them.
func invisible(r rune) bool {
	switch {
	case r >= 0xe0000 && r <= 0xe007f: // tags
		return true
	case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069, r == 0x200e, r == 0x200f, r == 0x061c: // bidi
		return true
	case r == 0x200b, r == 0x2060, r == 0xfeff, r == 0x180e: // zero width
		return true
	case r >= 0x2061 && r <= 0x2064: // invisible operators
		return true
	}
	return false
}

// hasInvisible reports whether s holds an invisible character. Every
// invisible character is outside ASCII, so a string of ASCII is answered
// without decoding.
func hasInvisible(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return strings.IndexFunc(s[i:], invisible) >= 0
		}
	}
	return false
}

// stripInvisible returns s without its invisible characters and how many
// went.
func stripInvisible(s string) (string, int) {
	if !hasInvisible(s) {
		return s, 0
	}
	n := 0
	out := strings.Map(func(r rune) rune {
		if invisible(r) {
			n++
			return -1
		}
		return r
	}, s)
	return out, n
}
