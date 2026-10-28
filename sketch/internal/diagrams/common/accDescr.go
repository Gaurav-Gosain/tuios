// Ported from pele src/diagrams/common/accDescr.ts at 8aff183be57021b533f319a29341a529be934b08.

package common

import (
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
)

// matchAccDescrHead answers as
// /[\t ]*accDescr(?:[\t ]*:(?:[^\n\r]*?(?=%%)|[^\n\r]*)|\s*({))/y: the end of the match, or -1, and
// whether the braced form matched.
func matchAccDescrHead(src string, at int) (int, bool) {
	q := blanks(src, at)
	if !strings.HasPrefix(src[q:], "accDescr") {
		return -1, false
	}
	q += len("accDescr")
	if r := blanks(src, q); byteAt(src, r) == ':' {
		return toCommentOrLineEnd(src, r+1), false
	}
	for q < len(src) {
		c, size := utf8.DecodeRuneInString(src[q:])
		if !js.IsSpace(c) {
			break
		}
		q += size
	}
	if byteAt(src, q) != '{' {
		return -1, false
	}
	return q + 1, true
}

// The braced form scans for its closing brace. Once none is found there is none further on,
// so later braced descriptions fail without scanning the rest of the text again.
func accDescrScanner() Scanner {
	unclosed := false
	return func(src string, at int) int {
		end, braced := matchAccDescrHead(src, at)
		if end < 0 {
			return -1
		}
		if !braced {
			return end
		}
		if unclosed {
			return -1
		}
		close := strings.IndexByte(src[end:], '}')
		unclosed = close == -1
		if close < 0 {
			return -1
		}
		return end + close + 1
	}
}

// AccDescr is the common accDescr terminal for grammars where lexing goes on after an `accDescr {` that never
// closes, because `accDescr` is also a name there. Each one would otherwise read to the end of the text.
// An empty name stands for pele's default, ACC_DESCR.
func AccDescr(name string) *TokenType {
	if name == "" {
		name = "ACC_DESCR"
	}
	return &TokenType{Name: name, Scanner: accDescrScanner, First: ACC_DESCR.First, Hidden: ACC_DESCR.Hidden}
}
