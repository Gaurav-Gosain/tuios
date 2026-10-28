// Ported from pele src/diagrams/ishikawa/lexer.ts at 8aff183be57021b533f319a29341a529be934b08.
//
// Offsets are byte offsets. A run of white space is measured in UTF-16 units where its length
// is a number: every JavaScript white space character is one unit.

package ishikawa

import (
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/util"
)

const (
	T_END = iota
	T_EOF
	T_SPACELINE
	T_NL
	T_ISHIKAWA
	T_SPACELIST
	T_TEXT
)

var TOKEN_NAMES = [...]string{"$end", "EOF", "SPACELINE", "NL", "ISHIKAWA", "SPACELIST", "TEXT"}

type Tokens struct {
	types  []int
	starts []int
	ends   []int
}

func byteAt(src string, i int) int {
	if i < 0 || i >= len(src) {
		return -1
	}
	return int(src[i])
}

func isWordAt(src string, i int) bool {
	c := byteAt(src, i)
	return c >= 0 && util.IsWord(rune(c))
}

func keyword(src string, at int) int {
	const word = "ishikawa-beta"
	k := 0
	for k < 13 {
		c := byteAt(src, at+k)
		w := int(word[k])
		if c != w && (c+32 != w || c < 65 || c > 90) {
			break
		}
		k++
	}
	// `ishikawa-beta`, or failing that `ishikawa`, either one ending at a word boundary.
	if k == 13 && !isWordAt(src, at+13) {
		return 13
	}
	if k >= 8 && !isWordAt(src, at+8) {
		return 8
	}
	return 0
}

// tokenize produces the tokens of Mermaid's Jison lexer for Ishikawa diagrams, where the first rule that
// matches wins: white space up to a comment or up to the last line break in it is a blank line,
// a single line break before text is a new line, other white space is indentation, and the rest
// of a line is text.
func tokenize(src string) Tokens {
	var t Tokens
	n := len(src)
	i := 0
	for i < n {
		j := i
		lastBreak := -1
		for j < n {
			c, size := utf8.DecodeRuneInString(src[j:])
			if !util.IsSpace(c) {
				break
			}
			if c == 10 {
				lastBreak = j
			}
			j += size
		}
		var typ, end int
		if byteAt(src, j) == 37 && byteAt(src, j+1) == 37 {
			typ = T_SPACELINE
			end = j + 2
			for end < n {
				c, size := utf8.DecodeRuneInString(src[end:])
				if c == 10 || c == 13 || c == 0x2028 || c == 0x2029 {
					break
				}
				end += size
			}
		} else if j == i {
			if length := keyword(src, i); length > 0 {
				typ = T_ISHIKAWA
				end = i + length
			} else {
				typ = T_TEXT
				end = strings.IndexByte(src[i:], '\n')
				if end == -1 {
					end = n
				} else {
					end += i
				}
			}
		} else if lastBreak > i {
			typ = T_SPACELINE
			end = lastBreak + 1
		} else if lastBreak == i {
			typ = T_NL
			end = i + 1
		} else {
			typ = T_SPACELIST
			end = j
		}
		t.types = append(t.types, typ)
		t.starts = append(t.starts, i)
		t.ends = append(t.ends, end)
		i = end
	}
	t.types = append(t.types, T_EOF, T_END)
	t.starts = append(t.starts, n, n)
	t.ends = append(t.ends, n, n)
	return t
}
