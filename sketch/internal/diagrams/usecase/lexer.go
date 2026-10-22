// Ported from pele src/diagrams/usecase/lexer.ts at 8fad23bd51783e4df1204e177a7fb0d2a30dcd13.
//
// The port reads UTF-8 bytes. Every character the lexer tells apart is ASCII; any other
// character is a run of bytes of 0x80 and above, which the lexer reads as pele reads a
// run of UTF-16 units of 0x7f and above. Offsets are bytes, and turn into UTF-16 units
// where a message shows them.

package usecase

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
	"tuios.dev/sketch/internal/util"
)

type Tokens struct {
	types  []int
	starts []int
	ends   []int
}

const (
	cWORD        = 1
	cPUNCTUATION = 2
)

var charClass = func() (class [128]uint8) {
	for c := 48; c <= 57; c++ {
		class[c] = cWORD
	}
	for c := 65; c <= 90; c++ {
		class[c] = cWORD
		class[c+32] = cWORD
	}
	class[95] = cWORD
	for _, ch := range "!#$&*+/=?^|~" {
		class[ch] = cPUNCTUATION
	}
	return class
}()

var KEYWORD_LENGTH = [...]int{0, 12, 5, 14, 3, 9, 2, 2, 2, 2, 2, 4, 3, 4, 8, 5, 5, 7, 6, 4, 5}

// at is src.charCodeAt(k) for a byte, or -1 past the end, where pele gets NaN.
func at(src string, k int) int {
	if k < 0 || k >= len(src) {
		return -1
	}
	return int(src[k])
}

func isWord(c int) bool {
	return c >= 0 && c < 128 && charClass[c]&cWORD != 0
}

func isBlank(c int) bool  { return util.IsBlank(rune(c)) }
func isDigit(c int) bool  { return util.IsDigit(rune(c)) }
func isLetter(c int) bool { return util.IsLetter(rune(c)) }
func isHex(c int) bool    { return util.IsHex(rune(c)) }

// startsWithNoCase: Mermaid's INCLUDE and EXTEND are the only keywords matched without regard to case.
func startsWithNoCase(src string, from int, word string) bool {
	for k := 0; k < len(word); k++ {
		if at(src, from+k)|32 != int(word[k]) {
			return false
		}
	}
	return true
}

func startsWith(src string, from int, word string) bool {
	return from <= len(src) && strings.HasPrefix(src[from:], word)
}

// keywordAt is the keyword that is a prefix of the text at `at`, tried in the order of Mermaid's token list.
func keywordAt(src string, i int, c int) int {
	pick := func(ok bool, t int) int {
		if ok {
			return t
		}
		return 0
	}
	switch c {
	case 97:
		return pick(startsWith(src, i, "actor"), T_ACTOR)
	case 115:
		if startsWith(src, i, "systemBoundary") {
			return T_SYSTEM_BOUNDARY
		}
		return pick(startsWith(src, i, "style"), T_STYLE)
	case 101:
		if startsWith(src, i, "end") {
			return T_END
		}
		return pick(startsWithNoCase(src, i, "extend"), T_EXTEND)
	case 69:
		return pick(startsWithNoCase(src, i, "extend"), T_EXTEND)
	case 100:
		return pick(startsWith(src, i, "direction"), T_DIRECTION)
	case 84:
		if startsWith(src, i, "TD") {
			return T_TD
		}
		return pick(startsWith(src, i, "TB"), T_TB)
	case 66:
		return pick(startsWith(src, i, "BT"), T_BT)
	case 76:
		return pick(startsWith(src, i, "LR"), T_LR)
	case 82:
		return pick(startsWith(src, i, "RL"), T_RL)
	case 110:
		return pick(startsWith(src, i, "note"), T_NOTE)
	case 102:
		if startsWith(src, i, "for") {
			return T_FOR
		}
		return pick(startsWith(src, i, "false"), T_FALSE)
	case 106:
		return pick(startsWith(src, i, "json"), T_JSON)
	case 99:
		if startsWith(src, i, "classDef") {
			return T_CLASS_DEF
		}
		return pick(startsWith(src, i, "class"), T_CLASS)
	case 105, 73:
		return pick(startsWithNoCase(src, i, "include"), T_INCLUDE)
	case 116:
		return pick(startsWith(src, i, "true"), T_TRUE)
	case 117:
		return pick(startsWith(src, i, "usecase-beta"), T_USECASE)
	}
	return 0
}

// units is the length in UTF-16 units of src[from:to].
func units(src string, from, to int) int {
	n := 0
	for k := from; k < to; {
		c := src[k]
		if c < utf8.RuneSelf {
			n++
			k++
			continue
		}
		r, size := utf8.DecodeRuneInString(src[k:to])
		if r > 0xffff {
			n += 2
		} else {
			n++
		}
		k += size
	}
	return n
}

// lineColumn is the line and the column, in UTF-16 units, of a byte offset.
func lineColumn(src string, offset int) (int, int) {
	line := 1
	lineStart := 0
	for i := 0; i < offset; i++ {
		c := src[i]
		if c != 10 && c != 13 {
			continue
		}
		if c == 13 && i+1 < offset && src[i+1] == 10 {
			i++
		}
		line++
		lineStart = i + 1
	}
	return line, units(src, lineStart, offset) + 1
}

func locationText(src string, start, end int) string {
	line, column := lineColumn(src, start)
	from := units(src, 0, start)
	return "line " + strconv.Itoa(line) + ", column " + strconv.Itoa(column) + " [" + strconv.Itoa(from) + "," + strconv.Itoa(from+units(src, start, end)) + ")"
}

// stringEnd is the end of a quoted string that opens at `at`, or -1 when the line ends first.
func stringEnd(src string, from int, quote byte) int {
	for k := from + 1; k < len(src); k++ {
		c := src[k]
		if c == quote {
			return k + 1
		}
		if c == 10 || c == 13 {
			return -1
		}
	}
	return -1
}

func startsToken(src string, from int) bool {
	c := src[from]
	if c != 34 && c != 39 {
		return true
	}
	return (c == 34 && at(src, from+1) == 96) || stringEnd(src, from, c) != -1
}

// lexError: Mermaid reports the first character no token starts with, and how many characters its lexer
// skipped before one did.
func lexError(src string, offset int, length int) *types.PeleError {
	line, column := lineColumn(src, offset)
	from := units(src, 0, offset)
	skipped := units(src, offset, offset+length)
	ch := ""
	if offset < len(src) {
		r, _ := utf8.DecodeRuneInString(src[offset:])
		ch = string(r)
	}
	return types.NewPeleError("Error lexing usecase diagram: unexpected character: ->"+ch+"<- at offset: "+strconv.Itoa(from)+", skipped "+strconv.Itoa(skipped)+
		" characters. at line "+strconv.Itoa(line)+", column "+strconv.Itoa(column)+" ["+strconv.Itoa(from)+","+strconv.Itoa(from+skipped)+")",
		types.CodeSyntax, types.ErrorInfo{Type: "usecase", Line: line, Column: column})
}

// tokenize produces the tokens of Mermaid's Chevrotain lexer for use case diagrams: the first pattern in
// its list that matches wins, a keyword gives way to a longer identifier, and `json id@` and `<<`
// switch to modes that read a JSON object and a stereotype.
func tokenize(src string) Tokens {
	// A token is at least one byte, and most are longer.
	room := len(src)/2 + 4
	tk := Tokens{types: make([]int, 0, room), starts: make([]int, 0, room), ends: make([]int, 0, room)}
	push := func(t, s, e int) {
		tk.types = append(tk.types, t)
		tk.starts = append(tk.starts, s)
		tk.ends = append(tk.ends, e)
	}
	n := len(src)
	i := 0
	// Whether only blanks came before on this line, where comments and accessibility lines may start.
	lineStart := true
	noBrace := false

	for i < n {
		c := int(src[i])
		if c == 32 || c == 9 {
			i++
			continue
		}
		typ := 0
		end := i + 1
		fresh := false

		if c < 128 && charClass[c]&cWORD != 0 {
			typ = 0
			if c == 97 && lineStart && startsWith(src, i, "acc") {
				descr := startsWith(src, i+3, "Descr")
				if descr || startsWith(src, i+3, "Title") {
					j := i + 8
					for isBlank(at(src, j)) {
						j++
					}
					next := at(src, j)
					if next == 58 {
						if descr {
							typ = T_ACC_DESCR_LINE
						} else {
							typ = T_ACC_TITLE_LINE
						}
						end = j + 1
						for end < n && src[end] != 10 && src[end] != 13 {
							end++
						}
					} else if next == 123 && descr && !noBrace {
						close := -1
						if j+1 <= n {
							if k := strings.IndexByte(src[j+1:], '}'); k >= 0 {
								close = j + 1 + k
							}
						}
						if close == -1 {
							noBrace = true
						} else {
							typ = T_ACC_DESCR_BLOCK
							end = close + 1
						}
					}
				}
			} else if c == 106 && startsWith(src, i, "json") && isBlank(at(src, i+4)) {
				j := i + 5
				for isBlank(at(src, j)) {
					j++
				}
				id := j
				for isWord(at(src, j)) {
					j++
				}
				if j > id {
					for isBlank(at(src, j)) {
						j++
					}
					if at(src, j) == 64 {
						j++
						for isBlank(at(src, j)) {
							j++
						}
						if at(src, j) == 123 {
							push(T_JSON_DECLARATION_START, i, j)
							depth := 0
							quoted := false
							escaped := false
							k := j
							for ; k < n; k++ {
								ch := src[k]
								if quoted {
									if escaped {
										escaped = false
									} else if ch == 92 {
										escaped = true
									} else if ch == 34 {
										quoted = false
									}
								} else if ch == 34 {
									quoted = true
								} else if ch == 123 {
									depth++
								} else if ch == 125 {
									depth--
									if depth == 0 {
										break
									}
								}
							}
							t := T_UNCLOSED_JSON_OBJECT_LITERAL
							i = n
							if k < n {
								t = T_JSON_OBJECT_LITERAL
								i = k + 1
							}
							push(t, j, i)
							lineStart = false
							continue
						}
					}
				}
			}
			if typ == 0 {
				for end < n && isWord(int(src[end])) {
					end++
				}
				keyword := keywordAt(src, i, c)
				switch {
				case keyword != 0:
					// A keyword that is only the start of a longer word is an identifier.
					if keyword == T_USECASE || end-i == KEYWORD_LENGTH[keyword] {
						typ = keyword
						end = i + KEYWORD_LENGTH[keyword]
					} else {
						typ = T_IDENTIFIER
					}
				case (c == 111 || c == 120) && at(src, i+1) == 45 && at(src, i+2) == 45:
					typ = T_BACKWARD_CROSS
					if c == 111 {
						typ = T_BACKWARD_CIRCLE
					}
					end = i + 3
				case isDigit(c):
					// `1mg` is an identifier, and `1.5px` is a number because the identifier stops at the dot.
					digits := i + 1
					for isDigit(at(src, digits)) {
						digits++
					}
					if at(src, digits) == 46 && isDigit(at(src, digits+1)) {
						typ = T_NUMBER
						end = digits + 2
						for isDigit(at(src, end)) {
							end++
						}
						for isLetter(at(src, end)) {
							end++
						}
					} else {
						typ = T_IDENTIFIER
					}
				default:
					typ = T_IDENTIFIER
					for at(src, end) == 45 && isWord(at(src, end+1)) {
						typ = T_CSS_IDENTIFIER
						end += 2
						for isWord(at(src, end)) {
							end++
						}
					}
				}
			}
		} else if c == 10 {
			typ = T_NEWLINE
			fresh = true
		} else if c == 13 {
			typ = T_NEWLINE
			fresh = true
			if at(src, end) == 10 {
				end++
			}
		} else if c == 34 || c == 39 {
			if c == 34 && at(src, i+1) == 96 {
				close := strings.Index(src[i+2:], "`\"")
				if close == -1 {
					typ = T_UNCLOSED_MARKDOWN_STRING
					end = n
				} else {
					typ = T_MARKDOWN_STRING
					end = i + 2 + close + 2
				}
			} else {
				typ = T_PLAIN_STRING
				end = stringEnd(src, i, byte(c))
				if end == -1 {
					resume := i + 1
					for resume < n && !startsToken(src, resume) {
						resume++
					}
					panic(lexError(src, i, resume-i))
				}
			}
		} else if c == 37 {
			if lineStart && at(src, end) == 37 {
				typ = T_COMMENT
				for end < n && src[end] != 10 && src[end] != 13 {
					end++
				}
			} else {
				typ = T_PERCENT
			}
		} else if c == 45 {
			if at(src, end) != 45 {
				typ = T_DASH
			} else {
				for at(src, end) == 45 {
					end++
				}
				next := at(src, end)
				two := end-i == 2
				switch {
				case two && next == 124 && at(src, end+1) == 62:
					typ = T_GENERALIZATION
					end += 2
				case next == 62:
					typ = T_FORWARD_SOLID
					end++
				case two && (next == 111 || next == 120):
					typ = T_FORWARD_CROSS
					if next == 111 {
						typ = T_FORWARD_CIRCLE
					}
					end++
				default:
					typ = T_MARKERLESS_SOLID
				}
			}
		} else if c == 46 {
			next := at(src, end)
			if next == 46 && at(src, end+1) == 62 {
				typ = T_DEPENDENCY_ARROW
				end += 2
			} else if isDigit(next) {
				typ = T_NUMBER
				for isDigit(at(src, end)) {
					end++
				}
				for isLetter(at(src, end)) {
					end++
				}
			} else {
				typ = T_DOT
			}
		} else if c == 60 {
			if at(src, end) == 60 {
				push(T_STEREOTYPE_START, i, i+2)
				i += 2
				if i >= n {
					break
				}
				if src[i] == 62 && at(src, i+1) == 62 {
					typ = T_STEREOTYPE_END
					end = i + 2
				} else {
					close := i
					found := false
					for ; close < n; close++ {
						ch := src[close]
						if ch == 10 || ch == 13 {
							break
						}
						if ch == 62 && at(src, close+1) == 62 {
							found = true
							break
						}
					}
					if found && js.Trim(src[i:close]) != "" {
						push(T_STEREOTYPE_TEXT, i, close)
						typ = T_STEREOTYPE_END
						i = close
						end = close + 2
					} else {
						for close < n && src[close] != 10 && src[close] != 13 {
							close++
						}
						if close == i {
							for close < n && (src[close] == 10 || src[close] == 13) {
								close++
							}
							panic(lexError(src, i, close-i))
						}
						typ = T_UNCLOSED_STEREOTYPE_TEXT
						end = close
					}
				}
			} else if at(src, end) == 45 && at(src, end+1) == 45 {
				typ = T_BACKWARD_SOLID
				end += 2
				for at(src, end) == 45 {
					end++
				}
			} else {
				typ = T_LABEL_PUNCTUATION
			}
		} else if c == 58 {
			if at(src, end) == 58 && at(src, end+1) == 58 {
				typ = T_CLASS_SEPARATOR
				end += 2
			} else {
				typ = T_COLON
			}
		} else if c == 64 {
			if at(src, end) == 123 {
				typ = T_METADATA_START
				end++
			} else {
				typ = T_AT
			}
		} else if c == 123 {
			typ = T_LBRACE
		} else if c == 125 {
			typ = T_RBRACE
		} else if c == 91 {
			typ = T_LBRACKET
		} else if c == 93 {
			typ = T_RBRACKET
		} else if c == 40 {
			typ = T_LPAREN
		} else if c == 41 {
			typ = T_RPAREN
		} else if c == 44 {
			typ = T_COMMA
		} else if c == 35 {
			for isHex(at(src, end)) {
				end++
			}
			typ = T_CSS_PUNCTUATION
			if end > i+1 {
				typ = T_HASH_COLOR
			}
		} else if c == 92 {
			if at(src, end) == 44 {
				typ = T_CSS_ESCAPED_COMMA
				end++
			} else {
				typ = T_LABEL_PUNCTUATION
			}
		} else if c < 127 && c > 32 {
			typ = T_LABEL_PUNCTUATION
			if charClass[c]&cPUNCTUATION != 0 {
				typ = T_CSS_PUNCTUATION
			}
		} else {
			typ = T_LABEL_SYMBOL
			for end < n {
				ch := src[end]
				if (ch >= 32 && ch < 127) || ch == 9 || ch == 10 || ch == 13 {
					break
				}
				end++
			}
		}

		push(typ, i, end)
		i = end
		lineStart = fresh
	}

	for k := 0; k < 3; k++ {
		push(T_EOF, n, n)
	}
	return tk
}
