package hints

import (
	"strings"
	"unicode/utf8"
)

// One URL detector serves every surface that finds an address in plain text:
// the url hint, and the pointer's hover and click over a pane. They used to be
// two (a regular expression here and a hand scanner in internal/app), and they
// disagreed on where a URL ends. The regular expression ran to the next space,
// so the markdown badge
//
//	[![ko-fi](https://ko-fi.com/img/b.svg)](https://ko-fi.com/B0B8)
//
// came out as one hint from the first scheme to the last bracket but one.
//
// The rules are the ones kitty, WezTerm and ghostty settle on:
//
//   - Only the schemes below start a match, and not in the middle of a word.
//   - A match stops at whitespace, a control byte, a quote, a backtick, an
//     angle bracket, or one of the bytes a pasted URL never holds unescaped.
//   - Brackets are counted. A closing ")" or "]" with no opener inside the URL
//     closes a bracket opened before it, so it ends the URL. A balanced pair
//     stays, which is what keeps "wiki/Go_(language)" whole.
//   - Trailing sentence punctuation goes back to the sentence.
//   - CJK and full-width punctuation ends a URL: the ideographic space and
//     the CJK symbols block (U+3000-303F), and the full-width forms of ASCII
//     punctuation (U+FF01-FF0F, U+FF1A-FF20). Chinese and Japanese text
//     follows a URL with no space, so "https://a.com/x，这个" is the address
//     and then a sentence. Other non-ASCII stays: an address can hold "中文".
//   - A scheme with nothing after it is a word, not an address.

// urlSchemes are the prefixes a match may start with, longest first where one
// is a prefix of another.
var urlSchemes = []string{"https://", "http://", "ftps://", "ftp://", "file://", "ssh://", "git://"}

// urlTrailing are given back from the end of a match.
const urlTrailing = ".,;:!?"

// urlStop reports whether c ends a URL.
func urlStop(c byte) bool {
	switch c {
	case ' ', '"', '\'', '<', '>', '`', '|', '^', '{', '}', '\\':
		return true
	}
	return c < 0x20 || c == 0x7f
}

// urlStopRune reports whether r, a rune past ASCII, ends a URL. See the rules
// at the top of the file.
func urlStopRune(r rune) bool {
	return (r >= 0x3000 && r <= 0x303f) ||
		(r >= 0xff01 && r <= 0xff0f) ||
		(r >= 0xff1a && r <= 0xff20)
}

// urlWordByte reports whether c, right before a scheme, makes the scheme the
// middle of a word: "xhttps://a" and "foo.http://b" are not links.
func urlWordByte(c byte) bool {
	return c == '_' || c == '-' || c == '.' || c == '/' || c == '+' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// urlSchemeAt returns the length of the scheme that starts at i, or 0.
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

// urlEnd returns where the URL whose scheme starts at i and is n bytes long
// ends, after brackets are settled and trailing punctuation is given back.
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
