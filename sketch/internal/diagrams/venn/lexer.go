// Ported from pele src/diagrams/venn/lexer.ts at 8aff183be57021b533f319a29341a529be934b08.
//
// Offsets are byte offsets. pele reads UTF-16 code units; every test here is of an ASCII
// character, so a byte stands for a unit, except where JavaScript white space outside ASCII
// can stand: there the port reads the whole character.

package venn

import (
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/util"
)

const (
	T_END = iota
	T_ERROR
	T_EOF
	T_NEWLINE
	T_VENN
	T_TITLE
	T_SET
	T_UNION
	T_TEXT
	T_INDENT_TEXT
	T_STYLE
	T_BRACKET_LABEL
	T_NUMERIC
	T_HEXCOLOR
	T_RGBCOLOR
	T_RGBACOLOR
	T_IDENTIFIER
	T_STRING
	T_COMMA
	T_COLON
)

var TOKEN_NAMES = [...]string{
	"$end", "INVALID", "EOF", "NEWLINE", "VENN", "TITLE", "SET", "UNION", "TEXT", "INDENT_TEXT", "STYLE", "BRACKET_LABEL",
	"NUMERIC", "HEXCOLOR", "RGBCOLOR", "RGBACOLOR", "IDENTIFIER", "STRING", "COMMA", "COLON",
}

// IndentHost is the part of the model the lexer talks to: an indented `text` line is a different token after a
// set or union than anywhere else, and the parser is what knows which came before.
type IndentHost interface {
	getIndentMode() bool
	setIndentMode(enabled bool)
}

var KEYWORDS = [...]struct {
	word string
	typ  int
}{
	{"venn-beta", T_VENN},
	{"set", T_SET},
	{"union", T_UNION},
	{"text", T_TEXT},
	{"style", T_STYLE},
}

// VennLexer reads tokens one at a time, as Mermaid's Jison lexer for venn diagrams does: every rule
// ignores case, the first rule that matches wins, and a keyword must end at a word boundary.
type VennLexer struct {
	src  string
	host IndentHost
	// The text and position of the token `next` returned last.
	text  string
	start int
	// titleSkip is the length of `title` and the white space character after it, for a TITLE.
	titleSkip int
	at        int
	lineStart bool
	ended     bool
}

func (l *VennLexer) charAt(i int) int {
	if i < 0 || i >= len(l.src) {
		return -1
	}
	return int(l.src[i])
}

// spaceAt reports whether a JavaScript white space character starts at i, and its length.
func (l *VennLexer) spaceAt(i int) (bool, int) {
	if i < 0 || i >= len(l.src) {
		return false, 0
	}
	c, size := utf8.DecodeRuneInString(l.src[i:])
	return util.IsSpace(c), size
}

func (l *VennLexer) word(at int, word string) bool {
	for k := 0; k < len(word); k++ {
		c := l.charAt(at + k)
		w := int(word[k])
		if c != w && (c+32 != w || c < 65 || c > 90) {
			return false
		}
	}
	return true
}

func (l *VennLexer) keyword(at int, word string) bool {
	if !l.word(at, word) {
		return false
	}
	c := l.charAt(at + len(word))
	return c < 0 || !util.IsWord(rune(c))
}

func isDigitAt(c int) bool { return c >= 48 && c <= 57 }

// color is the end of `rgb(1, 2, 3)` or `rgba(1, 2, 3, 0.5)` whose parenthesis opens at `at`, or -1.
func (l *VennLexer) color(at int, parts int) int {
	i := at + 1
	skip := func() {
		for {
			ok, size := l.spaceAt(i)
			if !ok {
				return
			}
			i += size
		}
	}
	for part := 0; part < parts; part++ {
		skip()
		from := i
		for isDigitAt(l.charAt(i)) || l.charAt(i) == 46 {
			i++
		}
		if i == from {
			return -1
		}
		skip()
		want := 44
		if part == parts-1 {
			want = 41
		}
		if l.charAt(i) != want {
			return -1
		}
		i++
	}
	return i
}

func (l *VennLexer) afterLineFeed(at int) bool {
	k := at - 1
	for l.charAt(k) == 32 || l.charAt(k) == 9 {
		k--
	}
	return l.charAt(k) == 10
}

func (l *VennLexer) lineEnd(from int) int {
	if from >= len(l.src) {
		return len(l.src)
	}
	if end := strings.IndexByte(l.src[from:], '\n'); end >= 0 {
		return from + end
	}
	return len(l.src)
}

func isHexAt(c int) bool { return c >= 0 && util.IsHex(rune(c)) }

func (l *VennLexer) next() int {
	src := l.src
	n := len(src)
	for {
		i := l.at
		l.start = i
		l.text = ""
		if i >= n {
			if l.ended {
				return T_END
			}
			l.ended = true
			return T_EOF
		}
		c := int(src[i])
		if c == 37 && l.charAt(i+1) == 37 && l.charAt(i+2) != 123 {
			l.at = l.lineEnd(i + 2)
			continue
		}
		if l.lineStart && l.word(i, "title") && l.afterLineFeed(i) && js.Trim(src[min(i+5, n):l.lineEnd(i+5)]) == "" {
			// Skip a bare title like a comment, so it doesn't end indented text.
			j := l.lineEnd(i + 5)
			for l.charAt(j) == 10 || l.charAt(j) == 13 {
				j++
			}
			l.at = j
			continue
		}
		if l.lineStart {
			if c == 32 || c == 9 {
				j := i + 1
				for l.charAt(j) == 32 || l.charAt(j) == 9 {
					j++
				}
				l.at = j
				if l.keyword(j, "text") && l.host.getIndentMode() {
					// The word `text` itself is dropped; the indentation stands for it.
					l.at = j + 4
					l.lineStart = false
					l.text = src[i:j]
					return T_INDENT_TEXT
				}
				continue
			}
			if c != 10 && c != 13 {
				l.host.setIndentMode(false)
				l.lineStart = false
			}
		}
		if c == 10 || c == 13 {
			i++
			for l.charAt(i) == 10 || l.charAt(i) == 13 {
				i++
			}
			l.lineStart = true
			return l.token(T_NEWLINE, i)
		}
		if c == 37 && l.charAt(i+1) == 37 {
			l.at = l.lineEnd(i)
			continue
		}
		if c == 32 || c == 9 {
			i++
			for l.charAt(i) == 32 || l.charAt(i) == 9 {
				i++
			}
			l.at = i
			continue
		}

		if ((c|32) >= 97 && (c|32) <= 122) || c == 95 {
			if sep := l.charAt(i + 5); l.word(i, "title") && sep != 10 && sep != 13 {
				if ok, size := l.spaceAt(i + 5); ok {
					start := i + 5 + size
					end := start
					for end < n {
						ch := src[end]
						if ch == 35 || ch == 10 || ch == 59 {
							break
						}
						end++
					}
					if end > start {
						l.titleSkip = 5 + size
						return l.token(T_TITLE, end)
					}
				}
			}
			for _, kw := range KEYWORDS {
				// A hyphen continues a name, as in `set-theory`.
				if l.keyword(i, kw.word) && l.charAt(i+len(kw.word)) != 45 {
					return l.token(kw.typ, i+len(kw.word))
				}
			}
			if l.word(i, "rgb") {
				alpha := (l.charAt(i+3) | 32) == 97
				open := i + 3
				if alpha {
					open = i + 4
				}
				end := -1
				if l.charAt(open) == 40 {
					parts := 3
					if alpha {
						parts = 4
					}
					end = l.color(open, parts)
				}
				if end != -1 {
					if alpha {
						return l.token(T_RGBACOLOR, end)
					}
					return l.token(T_RGBCOLOR, end)
				}
			}
			i++
			for {
				ch := l.charAt(i)
				if ch == 45 || (ch >= 0 && util.IsWord(rune(ch))) {
					i++
					continue
				}
				break
			}
			return l.token(T_IDENTIFIER, i)
		}
		if c == 91 {
			if l.charAt(i+1) == 34 {
				quote := -1
				if i+2 <= n {
					if q := strings.IndexByte(src[i+2:], '"'); q >= 0 {
						quote = i + 2 + q
					}
				}
				if quote == -1 || l.charAt(quote+1) != 93 {
					return T_ERROR
				}
				l.at = quote + 2
				l.text = src[i+2 : quote]
				return T_BRACKET_LABEL
			}
			end := i + 1
			for end < n && src[end] != 93 && src[end] != 34 {
				end++
			}
			if end == i+1 || l.charAt(end) != 93 {
				return T_ERROR
			}
			l.at = end + 1
			l.text = js.Trim(src[i+1 : end])
			return T_BRACKET_LABEL
		}
		if isDigitAt(c) || c == 46 || c == 43 || c == 45 {
			if c == 43 || c == 45 {
				i++
			}
			from := i
			for isDigitAt(l.charAt(i)) {
				i++
			}
			if i > from {
				if l.charAt(i) == 46 && isDigitAt(l.charAt(i+1)) {
					i += 2
					for isDigitAt(l.charAt(i)) {
						i++
					}
				}
			} else if l.charAt(i) == 46 && isDigitAt(l.charAt(i+1)) {
				i += 2
				for isDigitAt(l.charAt(i)) {
					i++
				}
			} else {
				return T_ERROR
			}
			return l.token(T_NUMERIC, i)
		}
		if c == 35 {
			end := i + 1
			for end < i+9 && isHexAt(l.charAt(end)) {
				end++
			}
			if end >= i+4 {
				return l.token(T_HEXCOLOR, end)
			}
			return T_ERROR
		}
		if c == 34 {
			if q := strings.IndexByte(src[i+1:], '"'); q >= 0 {
				return l.token(T_STRING, i+1+q+1)
			}
			return T_ERROR
		}
		if c == 44 {
			return l.token(T_COMMA, i+1)
		}
		if c == 58 {
			return l.token(T_COLON, i+1)
		}
		return T_ERROR
	}
}

func (l *VennLexer) token(typ int, end int) int {
	l.text = l.src[l.at:end]
	l.at = end
	return typ
}
