package hints

import (
	"strings"
	"unicode/utf8"
)

var urlSchemes = []string{"https://", "http://", "ftps://", "ftp://", "file://", "ssh://", "git://"}

const urlTrailing = ".,;:!?"

func urlStop(c byte) bool {
	switch c {
	case ' ', '"', '\'', '<', '>', '`', '|', '^', '{', '}', '\\':
		return true
	}
	return c < 0x20 || c == 0x7f
}

func urlStopRune(r rune) bool {
	return (r >= 0x3000 && r <= 0x303f) ||
		(r >= 0xff01 && r <= 0xff0f) ||
		(r >= 0xff1a && r <= 0xff20)
}

func urlWordByte(c byte) bool {
	return c == '_' || c == '-' || c == '.' || c == '/' || c == '+' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func urlSchemeAt(s string, i int) int {
	if i > 0 && urlWordByte(s[i-1]) {
		return 0
	}
	for _, sch := range urlSchemes {
		if strings.HasPrefix(s[i:], sch) {
			return len(sch)
		}
	}
	return 0
}

func urlEnd(s string, i, n int) int {
	e := i + n
	parens, squares := 0, 0
scan:
	for ; e < len(s); e++ {
		c := s[e]
		if urlStop(c) {
			break
		}
		if c >= utf8.RuneSelf {
			r, size := utf8.DecodeRuneInString(s[e:])
			if urlStopRune(r) {
				break
			}
			e += size - 1
			continue
		}
		switch c {
		case '(':
			parens++
		case '[':
			squares++
		case ')':
			if parens == 0 {
				break scan
			}
			parens--
		case ']':
			if squares == 0 {
				break scan
			}
			squares--
		}
	}
	for e > i+n && strings.IndexByte(urlTrailing, s[e-1]) >= 0 {
		e--
	}
	return e
}

// URLs returns the byte range of every URL on line, in order.
func URLs(line string) [][2]int {
	var out [][2]int
	for i := 0; i < len(line); i++ {
		n := urlSchemeAt(line, i)
		if n == 0 {
			continue
		}
		e := urlEnd(line, i, n)
		if e <= i+n {
			continue
		}
		out = append(out, [2]int{i, e})
		i = e - 1
	}
	return out
}

// URLAt returns the byte range of the URL covering byte offset i of line, and
// whether there is one.
func URLAt(line string, i int) (start, end int, ok bool) {
	if i < 0 || i >= len(line) {
		return 0, 0, false
	}
	for _, r := range URLs(line) {
		if r[0] > i {
			break
		}
		if i < r[1] {
			return r[0], r[1], true
		}
	}
	return 0, 0, false
}
