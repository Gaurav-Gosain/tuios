// Ported from pele src/diagrams/sankey/lexer.ts at 8fad23bd51783e4df1204e177a7fb0d2a30dcd13.
//
// The port reads UTF-8 bytes. Every character the lexer takes is ASCII, so a byte offset
// stands for pele's unit offset, except in the keyword test: see keywordLength.

package sankey

import (
	"unicode/utf8"

	"tuios.dev/sketch/internal/util"
)

// Token types.
const (
	T_END = iota
	// T_STUCK is not a Jison token: the reference lexer would return the same empty token forever from here.
	T_STUCK
	T_SANKEY
	T_NEWLINE
	T_COMMA
	T_DQUOTE
	T_NON_ESCAPED_TEXT
	T_ESCAPED_TEXT
	T_EOF
)

var TOKEN_NAMES = [...]string{
	"$end",
	"STUCK",
	"SANKEY",
	"NEWLINE",
	"COMMA",
	"DQUOTE",
	"NON_ESCAPED_TEXT",
	"ESCAPED_TEXT",
	"EOF",
}

type Tokens struct {
	types  []int
	texts  []string
	starts []int
}

const (
	S_INITIAL = iota
	S_csv
	S_escaped_text
)

// isTextData is RFC 4180 TEXTDATA: printable ASCII except the double quote and the comma.
func isTextData(c int) bool {
	return c >= 32 && c <= 126 && c != 34 && c != 44
}

func byteAt(src string, q int) int {
	if q < 0 || q >= len(src) {
		return -1
	}
	return int(src[q])
}

// keywordLength is the length in bytes of `word` at p, if src.slice(p, p + word.length).toLowerCase()
// is word and no word character follows, or 0. word is lowercase ASCII. The only character
// outside ASCII that toLowerCase turns into an ASCII letter is the Kelvin sign, U+212A, which
// becomes k.
func keywordLength(src string, p int, word string) int {
	q := p
	for i := 0; i < len(word); i++ {
		if q >= len(src) {
			return 0
		}
		c := src[q]
		if c >= utf8.RuneSelf {
			r, size := utf8.DecodeRuneInString(src[q:])
			if r != 0x212a || word[i] != 'k' {
				return 0
			}
			q += size
			continue
		}
		if c >= 'A' && c <= 'Z' {
			c += 32
		}
		if c != word[i] {
			return 0
		}
		q++
	}
	if q < len(src) && src[q] < utf8.RuneSelf && util.IsWord(rune(src[q])) {
		return 0
	}
	return q - p
}

func tokenize(src string) Tokens {
	n := len(src)
	var out Tokens
	var stack []int
	state := S_INITIAL
	p := 0

	emit := func(typ, s, e int) {
		out.types = append(out.types, typ)
		out.texts = append(out.texts, src[s:e])
		out.starts = append(out.starts, s)
		p = e
	}

	for p < n {
		c := int(src[p])
		start := p
		q := p
		if state == S_escaped_text {
			if c == 34 && byteAt(src, p+1) != 34 {
				if len(stack) > 0 {
					state = stack[len(stack)-1]
					stack = stack[:len(stack)-1]
				}
				emit(T_DQUOTE, p, p+1)
				continue
			}
			for q < n {
				d := int(src[q])
				if isTextData(d) || d == 44 || d == 13 || d == 10 {
					q++
				} else if d == 34 && byteAt(src, q+1) == 34 {
					q += 2
				} else {
					break
				}
			}
			emit(T_ESCAPED_TEXT, p, q)
		} else {
			if state == S_INITIAL && c|32 == 115 {
				length := keywordLength(src, p, "sankey-beta")
				if length == 0 {
					length = keywordLength(src, p, "sankey")
				}
				if length > 0 {
					stack = append(stack, state)
					state = S_csv
					emit(T_SANKEY, p, p+length)
					continue
				}
			}
			if c == 10 || (c == 13 && byteAt(src, p+1) == 10) {
				if c == 10 {
					emit(T_NEWLINE, p, p+1)
				} else {
					emit(T_NEWLINE, p, p+2)
				}
				continue
			}
			if c == 44 {
				emit(T_COMMA, p, p+1)
				continue
			}
			if c == 34 {
				stack = append(stack, state)
				state = S_escaped_text
				emit(T_DQUOTE, p, p+1)
				continue
			}
			for q < n && isTextData(int(src[q])) {
				q++
			}
			emit(T_NON_ESCAPED_TEXT, p, q)
		}
		// A character outside the CSV alphabet matches as empty text and is never consumed.
		if q == start {
			emit(T_STUCK, p, p)
			return out
		}
	}

	if state == S_escaped_text {
		emit(T_ESCAPED_TEXT, n, n)
	} else {
		emit(T_EOF, n, n)
	}
	emit(T_END, n, n)
	return out
}
