// Ported from pele src/diagrams/common/scan.ts at 8fad23bd51783e4df1204e177a7fb0d2a30dcd13.
//
// The port reads UTF-8 bytes. A step past a character steps the whole character. pele
// reads a character outside the BMP as two UTF-16 units, so the unit after one is a low
// surrogate: unitAfter gives 0xdc00 for it, which matches none of the tests.

package common

import (
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/util"
)

// Lexer rules that Mermaid's journey and timeline grammars have in common, ported from the
// compiled Jison rules: comments, whitespace, and the accTitle and accDescr statements.

// ScanTokens are the tokens of a lexer built on these rules: a type, the text and the
// byte offset where it starts.
type ScanTokens struct {
	Types  []int
	Texts  []string
	Starts []int
}

// LineEnd is the offset of the next line break at or after `from`, or the end of src.
func LineEnd(src string, from int) int {
	if from >= len(src) {
		return len(src)
	}
	at := strings.IndexByte(src[from:], '\n')
	if at == -1 {
		return len(src)
	}
	return from + at
}

// KeywordAt reports whether `word` is at p. `word` is lowercase ASCII letters; the grammars are case-insensitive.
func KeywordAt(src string, p int, word string) bool {
	for i := 0; i < len(word); i++ {
		c := 32
		if p+i >= 0 && p+i < len(src) {
			c = int(src[p+i]) | 32
		}
		if c != int(word[i]) {
			return false
		}
	}
	return true
}

// sizeAt is the length in bytes of the character at q.
func sizeAt(src string, q int) int {
	if q >= len(src) || src[q] < utf8.RuneSelf {
		return 1
	}
	_, s := utf8.DecodeRuneInString(src[q:])
	return s
}

// unitAfter is the UTF-16 unit after the character at q, as charCodeAt(q + 1) reads it.
func unitAfter(src string, q int) rune {
	if q < len(src) && src[q] >= 0xf0 {
		return 0xdc00
	}
	return codeAt(src, q+sizeAt(src, q))
}

// SkipTrivia skips comments and whitespace. Stops at a line break that starts a token, which is a NEWLINE.
// Jison compiles `\%%` to a single `%`, so one percent sign starts a comment, and the second
// comment rule takes any character before `%%` with it, a line break included.
func SkipTrivia(src string, from int) int {
	n := len(src)
	p := from
	for p < n {
		c := codeAt(src, p)
		if c == 37 && codeAt(src, p+1) != 123 {
			p = LineEnd(src, p)
		} else if q := p + sizeAt(src, p); c != 125 && unitAfter(src, p) == 37 && codeAt(src, q+1) == 37 {
			p = LineEnd(src, q+2)
		} else if c == 10 {
			return p
		} else if util.IsSpace(c) {
			for {
				p += sizeAt(src, p)
				if !(p < n && util.IsSpace(codeAt(src, p))) {
					break
				}
			}
		} else if c == 35 {
			p = LineEnd(src, p)
		} else {
			return p
		}
	}
	return p
}

func skipSpace(src string, from int) int {
	p := from
	for util.IsSpace(codeAt(src, p)) {
		p += sizeAt(src, p)
	}
	return p
}

// Accessibility lexes an accTitle or accDescr statement at `p` and returns where it ends, or `p` if there is none.
// The token types acc_title, acc_title_value, acc_descr, acc_descr_value and
// acc_descr_multiline_value must be numbered consecutively from `first`.
func Accessibility(src string, p int, out *ScanTokens, first int) int {
	descr := KeywordAt(src, p, "accdescr")
	if !descr && !KeywordAt(src, p, "acctitle") {
		return p
	}
	n := len(src)
	q := skipSpace(src, p+8)
	c := codeAt(src, q)
	emit := func(typ, s, e int) {
		out.Types = append(out.Types, typ)
		out.Texts = append(out.Texts, src[s:e])
		out.Starts = append(out.Starts, s)
	}
	if c == 58 {
		q = skipSpace(src, q+1)
		end := LineEnd(src, q)
		if descr {
			emit(first+2, p, q)
			emit(first+3, q, end)
		} else {
			emit(first, p, q)
			emit(first+1, q, end)
		}
		return end
	}
	if !descr || c != 123 {
		return p
	}
	q = skipSpace(src, q+1)
	for codeAt(src, q) != 125 {
		end := n
		if at := strings.IndexByte(src[q:], '}'); at != -1 {
			end = q + at
		}
		emit(first+4, q, end)
		if q == n {
			return n
		}
		q = end
	}
	return q + 1
}
