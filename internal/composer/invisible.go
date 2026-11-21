package composer

import (
	"strings"
	"unicode/utf8"
)

func invisible(r rune) bool {
	switch {
	case r >= 0xe0000 && r <= 0xe007f:
		return true
	case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069, r == 0x200e, r == 0x200f, r == 0x061c:
		return true
	case r == 0x200b, r == 0x2060, r == 0xfeff, r == 0x180e:
		return true
	case r >= 0x2061 && r <= 0x2064:
		return true
	}
	return false
}

func hasInvisible(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return strings.IndexFunc(s[i:], invisible) >= 0
		}
	}
	return false
}

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
