package main

import (
	"strings"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/invisible"
)

func hostStyledText(s string) string {
	s = invisible.Strip(s)
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
			i += min(2, len(s)-i)
		default:
			r, size := utf8.DecodeRuneInString(s[i:])
			switch {
			case r == 0x9b:
				n, _, _ := scanCSI(s[i+size:])
				i += size + n
				continue
			case r == 0x9d || r == 0x90 || r == 0x98 || r == 0x9e || r == 0x9f:
				i += size + scanString(s[i+size:])
				continue
			case r == '\n' || r == '\t':
				b.WriteRune(r)
			case r < 0x20 || (r >= 0x7f && r < 0xa0):
			case r == utf8.RuneError && size == 1:
			default:
				b.WriteString(s[i : i+size])
			}
			i += size
		}
	}
	return b.String()
}

func scanCSI(s string) (int, byte, bool) {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 0x20 && c <= 0x3f:
		case c >= 0x40 && c <= 0x7e:
			return i + 1, c, true
		default:
			return i, 0, false
		}
	}
	return len(s), 0, false
}

func sgrParams(p string) bool {
	for i := 0; i < len(p); i++ {
		if c := p[i]; (c < '0' || c > '9') && c != ';' && c != ':' {
			return false
		}
	}
	return true
}

func scanString(s string) int {
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == 0x07:
			return i + 1
		case s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\':
			return i + 2
		case s[i] == 0xc2 && i+1 < len(s) && s[i+1] == 0x9c:
			return i + 2
		}
	}
	return len(s)
}
