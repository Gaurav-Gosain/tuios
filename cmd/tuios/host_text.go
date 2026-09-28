package main

import (
	"strings"
	"unicode/utf8"
)

// Text another machine wrote reaches this terminal from a capture of a pane
// there. A terminal acts on escape sequences, and a sequence can do far more
// than colour text: OSC 52 writes the clipboard, OSC 8 hides a link target,
// a cursor move overwrites what was printed before it, including the fence.
// Bidi and zero-width characters make text read differently from what it is.

// invisibleRune reports the format characters that change how text reads
// without showing themselves: zero-width spaces and joiners, the word joiner
// and invisible operators, the byte order mark, and the bidi embeddings,
// overrides, isolates and marks.
func invisibleRune(r rune) bool {
	switch {
	case r >= 0x200B && r <= 0x200F, // zero-width space, non-joiner, joiner, LRM, RLM
		r >= 0x202A && r <= 0x202E, // bidi embeddings and overrides
		r >= 0x2060 && r <= 0x2064, // word joiner, invisible operators
		r >= 0x2066 && r <= 0x2069, // bidi isolates
		r == 0x061C,                // Arabic letter mark
		r == 0xFEFF:                // zero-width no-break space
		return true
	}
	return false
}

// hostPlainText is plainText for text from another machine: control
// characters and invisible format characters removed, newlines and tabs kept.
func hostPlainText(s string) string {
	return stripInvisible(plainText(s))
}

// stripInvisible removes the characters invisibleRune names.
func stripInvisible(s string) string {
	return strings.Map(func(r rune) rune {
		if invisibleRune(r) {
			return -1
		}
		return r
	}, s)
}

// hostStyledText is for a capture from another machine that asked for escape
// codes (--ansi, --resolved). It keeps SGR sequences (CSI ... m), which only
// colour and style text, and removes every other sequence: OSC, DCS, SOS, PM
// and APC strings, every other CSI (cursor moves, erases, modes), and the
// two-byte escapes. Other control characters and invisible format characters
// are removed as hostPlainText removes them.
func hostStyledText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == 0x1b && i+1 < len(s) && s[i+1] == '[':
			n, final, ok := scanCSI(s[i+2:])
			if ok && final == 'm' && sgrParams(s[i+2:i+2+n-1]) {
				b.WriteString(s[i : i+2+n])
			}
			i += 2 + n
		case c == 0x1b && i+1 < len(s) && (s[i+1] == ']' || s[i+1] == 'P' || s[i+1] == 'X' || s[i+1] == '^' || s[i+1] == '_'):
			i += 2 + scanString(s[i+2:])
		case c == 0x1b:
			// A two-byte escape, or a lone ESC at the end: dropped with the
			// byte after it, if there is one.
			i += min(2, len(s)-i)
		default:
			r, size := utf8.DecodeRuneInString(s[i:])
			switch {
			case r == 0x9b: // C1 CSI
				n, _, _ := scanCSI(s[i+size:])
				i += size + n
				continue
			case r == 0x9d || r == 0x90 || r == 0x98 || r == 0x9e || r == 0x9f: // C1 OSC, DCS, SOS, PM, APC
				i += size + scanString(s[i+size:])
				continue
			case r == '\n' || r == '\t':
				b.WriteRune(r)
			case r < 0x20 || (r >= 0x7f && r < 0xa0) || invisibleRune(r):
			case r == utf8.RuneError && size == 1:
			default:
				b.WriteString(s[i : i+size])
			}
			i += size
		}
	}
	return b.String()
}

// scanCSI measures a CSI sequence's body after its introducer: parameter and
// intermediate bytes, then one final byte. It returns the length consumed,
// the final byte, and whether a final byte was found. A body cut off by the
// end of the text, or broken by a byte that cannot be in a CSI, ends there.
func scanCSI(s string) (int, byte, bool) {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 0x20 && c <= 0x3f: // parameters and intermediates
		case c >= 0x40 && c <= 0x7e:
			return i + 1, c, true
		default:
			return i, 0, false
		}
	}
	return len(s), 0, false
}

// sgrParams reports whether a CSI body before its final byte is a plain SGR
// parameter list: digits, ';' and ':' only. A private marker or an
// intermediate byte makes it some other sequence that happens to end in m.
func sgrParams(p string) bool {
	for i := 0; i < len(p); i++ {
		if c := p[i]; (c < '0' || c > '9') && c != ';' && c != ':' {
			return false
		}
	}
	return true
}

// scanString measures an OSC, DCS, SOS, PM or APC string after its
// introducer, through its terminator: BEL, ST (ESC \) or C1 ST. A string with
// no terminator runs to the end of the text, all of it dropped.
func scanString(s string) int {
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == 0x07:
			return i + 1
		case s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\':
			return i + 2
		case s[i] == 0xc2 && i+1 < len(s) && s[i+1] == 0x9c: // C1 ST in UTF-8
			return i + 2
		}
	}
	return len(s)
}
